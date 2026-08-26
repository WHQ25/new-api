package middleware

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newArkTestEngine wires ArkRequestConvert in front of a stub relay handler so
// tests can assert both the inbound rewrite and the outbound Ark shape without
// touching the database or an upstream provider.
func newArkTestEngine(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group(ArkVideoTaskPath)
	group.Use(ArkRequestConvert())
	group.POST("", handler)
	group.GET("/:task_id", handler)
	return engine
}

// arkSubmit posts an Ark-shaped body and returns the converted request the relay
// layer would see, plus the response handed back to the client.
func arkSubmit(t *testing.T, body string, handler gin.HandlerFunc) (map[string]any, string, *httptest.ResponseRecorder) {
	t.Helper()
	var converted map[string]any
	var rewrittenPath string
	engine := newArkTestEngine(func(c *gin.Context) {
		require.NoError(t, common.UnmarshalBodyReusable(c, &converted))
		rewrittenPath = c.Request.URL.Path
		handler(c)
	})

	req := httptest.NewRequest(http.MethodPost, ArkVideoTaskPath, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return converted, rewrittenPath, recorder
}

func okHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"id": "task_abc", "task_id": "task_abc", "object": "video", "status": "queued"})
}

func TestArkRequestConvertRewritesTextOnlySubmit(t *testing.T) {
	body := `{
		"model": "doubao-seedance-2-5-260628",
		"content": [{"type": "text", "text": "a cat walking"}],
		"resolution": "1080p",
		"ratio": "16:9",
		"duration": 5,
		"seed": 42,
		"camera_fixed": false,
		"watermark": false,
		"generate_audio": true
	}`
	converted, path, _ := arkSubmit(t, body, okHandler)

	assert.Equal(t, "/v1/video/generations", path)
	assert.Equal(t, "doubao-seedance-2-5-260628", converted["model"])
	assert.Equal(t, "a cat walking", converted["prompt"])
	assert.Equal(t, float64(5), converted["duration"])

	metadata, ok := converted["metadata"].(map[string]any)
	require.True(t, ok, "metadata must carry the original Ark body")
	// 官方顶层字段原样进 metadata，豆包适配器会把它们反序列化进上游请求体顶层。
	assert.Equal(t, "1080p", metadata["resolution"])
	assert.Equal(t, "16:9", metadata["ratio"])
	assert.Equal(t, float64(42), metadata["seed"])
	assert.Equal(t, false, metadata["camera_fixed"])
	assert.Equal(t, false, metadata["watermark"])
	assert.Equal(t, true, metadata["generate_audio"])
}

func TestArkRequestConvertPreservesMediaRolesInMetadata(t *testing.T) {
	body := `{
		"model": "dreamina-seedance-2-0-hc",
		"content": [
			{"type": "image_url", "image_url": {"url": "https://example.com/first.png"}, "role": "first_frame"},
			{"type": "image_url", "image_url": {"url": "https://example.com/last.png"}, "role": "last_frame"},
			{"type": "video_url", "video_url": {"url": "https://example.com/motion.mp4"}, "role": "reference_video"},
			{"type": "audio_url", "audio_url": {"url": "https://example.com/voice.wav"}, "role": "reference_audio"},
			{"type": "text", "text": "keep the reference style"}
		],
		"duration": 8
	}`
	converted, _, _ := arkSubmit(t, body, okHandler)

	assert.Equal(t, "keep the reference style", converted["prompt"])
	// 顶层 images 会与 metadata.content 在同一个切片上按下标合并并交叉污染条目，
	// 因此素材只走 metadata.content 这一条通道。
	assert.NotContains(t, converted, "images")

	metadata := converted["metadata"].(map[string]any)
	content, ok := metadata["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 5)

	first := content[0].(map[string]any)
	assert.Equal(t, "image_url", first["type"])
	assert.Equal(t, "first_frame", first["role"])
	assert.Equal(t, "https://example.com/first.png", first["image_url"].(map[string]any)["url"])

	assert.Equal(t, "last_frame", content[1].(map[string]any)["role"])
	assert.Equal(t, "reference_video", content[2].(map[string]any)["role"])
	assert.Equal(t, "https://example.com/motion.mp4", content[2].(map[string]any)["video_url"].(map[string]any)["url"])
	assert.Equal(t, "reference_audio", content[3].(map[string]any)["role"])
	assert.Equal(t, "https://example.com/voice.wav", content[3].(map[string]any)["audio_url"].(map[string]any)["url"])
}

func TestArkRequestConvertJoinsMultipleTextItems(t *testing.T) {
	body := `{"model":"m","content":[{"type":"text","text":"first"},{"type":"image_url","image_url":{"url":"u"}},{"type":"text","text":"second"}]}`
	converted, _, _ := arkSubmit(t, body, okHandler)
	assert.Equal(t, "first\nsecond", converted["prompt"])
}

func TestArkRequestConvertKeepsModelDecidedDurationInMetadataOnly(t *testing.T) {
	// 方舟允许显式 -1 / 0 表示「由模型决定」。这类值提到顶层会被时长上界校验拒绝，
	// 所以只留在 metadata 里透传给上游，不参与计费估算。
	for _, duration := range []string{"-1", "0"} {
		converted, _, recorder := arkSubmit(t, `{"model":"m","content":[{"type":"text","text":"p"}],"duration":`+duration+`}`, okHandler)
		require.Equal(t, http.StatusOK, recorder.Code)
		assert.NotContains(t, converted, "duration", "duration=%s must not reach the top level", duration)
		assert.Equal(t, duration, fmt.Sprint(int(converted["metadata"].(map[string]any)["duration"].(float64))))
	}
}

// duration 与 frames 都会决定输出时长、进而成为计费乘数，必须在协议边界以 400 拦下。
// 尤其是浮点 duration：内部 dto.IntValue 只接受整数和数字字符串，放行的话要到构建
// 上游请求体时才失败，对调用方表现为 500。
func TestArkRequestConvertRejectsOutOfContractNumbers(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"duration below -1", `{"model":"m","content":[{"type":"text","text":"p"}],"duration":-2}`},
		{"duration above the task cap", `{"model":"m","content":[{"type":"text","text":"p"}],"duration":3601}`},
		{"fractional duration", `{"model":"m","content":[{"type":"text","text":"p"}],"duration":5.5}`},
		{"string duration", `{"model":"m","content":[{"type":"text","text":"p"}],"duration":"5"}`},
		{"huge string duration", `{"model":"m","content":[{"type":"text","text":"p"}],"duration":"999999999999"}`},
		{"frames below range", `{"model":"m","content":[{"type":"text","text":"p"}],"frames":25}`},
		{"frames above range", `{"model":"m","content":[{"type":"text","text":"p"}],"frames":293}`},
		{"frames off the 25+4n grid", `{"model":"m","content":[{"type":"text","text":"p"}],"frames":30}`},
		{"fractional frames", `{"model":"m","content":[{"type":"text","text":"p"}],"frames":29.5}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := newArkTestEngine(func(c *gin.Context) {
				t.Fatal("handler must not run for an out-of-contract request")
			})
			req := httptest.NewRequest(http.MethodPost, ArkVideoTaskPath, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestArkRequestConvertAcceptsContractBoundaries(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","content":[{"type":"text","text":"p"}],"duration":3600}`,
		`{"model":"m","content":[{"type":"text","text":"p"}],"frames":29}`,
		`{"model":"m","content":[{"type":"text","text":"p"}],"frames":289}`,
	} {
		_, _, recorder := arkSubmit(t, body, okHandler)
		assert.Equal(t, http.StatusOK, recorder.Code, "body %s must be accepted", body)
	}
}

func TestArkRequestConvertRejectsInvalidBody(t *testing.T) {
	engine := newArkTestEngine(func(c *gin.Context) {
		t.Fatal("handler must not run for an unparsable body")
	})
	req := httptest.NewRequest(http.MethodPost, ArkVideoTaskPath, bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestArkSubmitResponseExposesOnlyPublicTaskID(t *testing.T) {
	_, _, recorder := arkSubmit(t, `{"model":"m","content":[{"type":"text","text":"p"}]}`, okHandler)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, map[string]any{"id": "task_abc"}, response)
}

// 转换失败必须 fail-closed：内部形状里带着上游原始响应，直接回退会把上游任务 ID
// 漏给调用方。
func TestArkResponseFailsClosedWhenNotConvertible(t *testing.T) {
	_, _, recorder := arkSubmit(t, `{"model":"m","content":[{"type":"text","text":"p"}]}`, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"upstream": gin.H{"id": "cgt-20260826-secret"}})
	})

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "cgt-20260826-secret")
}

func TestArkSubmitErrorResponsePassesThrough(t *testing.T) {
	_, _, recorder := arkSubmit(t, `{"model":"m","content":[{"type":"text","text":"p"}]}`, func(c *gin.Context) {
		c.JSON(http.StatusForbidden, gin.H{"code": "insufficient_user_quota", "message": "quota not enough"})
	})

	require.Equal(t, http.StatusForbidden, recorder.Code)
	assert.JSONEq(t, `{"code":"insufficient_user_quota","message":"quota not enough"}`, recorder.Body.String())
}

// arkFetch drives a GET through the middleware with a stub relay response and
// returns the rewritten internal path plus the Ark-shaped client response.
func arkFetch(t *testing.T, taskID string, internalResponse string) (string, map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	var rewrittenPath string
	engine := newArkTestEngine(func(c *gin.Context) {
		rewrittenPath = c.Request.URL.Path
		c.Data(http.StatusOK, "application/json", []byte(internalResponse))
	})

	req := httptest.NewRequest(http.MethodGet, ArkVideoTaskPath+"/"+taskID, nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return rewrittenPath, response, recorder
}

func TestArkFetchConvertsSucceededTask(t *testing.T) {
	internal := `{"code":"success","data":{
		"task_id":"task_abc",
		"status":"SUCCESS",
		"result_url":"https://cdn.example.com/out.mp4",
		"submit_time":1787274123,
		"updated_at":1787274184,
		"properties":{"origin_model_name":"doubao-seedance-2-5-260628"},
		"data":{
			"id":"cgt-20260826-secret",
			"status":"succeeded",
			"content":{"video_url":"https://cdn.example.com/out.mp4","last_frame_url":"https://cdn.example.com/last.jpg"},
			"resolution":"720p","ratio":"16:9","duration":8,"framespersecond":24,
			"usage":{"completion_tokens":100,"total_tokens":120}
		}
	}}`
	path, response, recorder := arkFetch(t, "task_abc", internal)

	assert.Equal(t, "/v1/video/generations/task_abc", path)
	require.Equal(t, http.StatusOK, recorder.Code)

	assert.Equal(t, "task_abc", response["id"])
	assert.Equal(t, "succeeded", response["status"])
	assert.Equal(t, "doubao-seedance-2-5-260628", response["model"])
	assert.Equal(t, "720p", response["resolution"])
	assert.Equal(t, "16:9", response["ratio"])
	assert.Equal(t, float64(8), response["duration"])
	assert.Equal(t, float64(1787274123), response["created_at"])
	assert.Equal(t, float64(1787274184), response["updated_at"])

	content := response["content"].(map[string]any)
	assert.Equal(t, "https://cdn.example.com/out.mp4", content["video_url"])
	assert.Equal(t, "https://cdn.example.com/last.jpg", content["last_frame_url"])

	usage := response["usage"].(map[string]any)
	assert.Equal(t, float64(120), usage["total_tokens"])

	assert.NotContains(t, recorder.Body.String(), "cgt-20260826-secret")
	assert.NotContains(t, response, "error")
}

// content 只按官方文档定义的两个键取值：整体复制会把上游后续新增的任何字段
// （包括可能的上游任务 ID）一并转发出去。
func TestArkFetchContentIsWhitelisted(t *testing.T) {
	internal := `{"code":"success","data":{"task_id":"task_abc","status":"SUCCESS","data":{
		"status":"succeeded",
		"content":{"video_url":"https://cdn/o.mp4","last_frame_url":"https://cdn/l.jpg","upstream_task_id":"cgt-leak"}
	}}}`
	_, response, recorder := arkFetch(t, "task_abc", internal)

	assert.Equal(t, map[string]any{
		"video_url":      "https://cdn/o.mp4",
		"last_frame_url": "https://cdn/l.jpg",
	}, response["content"])
	assert.NotContains(t, recorder.Body.String(), "cgt-leak")
}

// 官方协议约定 error 仅在失败时出现；cancelled / expired 是「没跑完」而非「跑失败」。
func TestArkFetchOmitsErrorForNonFailedTerminalStates(t *testing.T) {
	for _, upstreamStatus := range []string{"cancelled", "expired"} {
		internal := `{"code":"success","data":{"task_id":"task_abc","status":"FAILURE","data":{"status":"` + upstreamStatus + `"}}}`
		_, response, _ := arkFetch(t, "task_abc", internal)
		assert.Equal(t, upstreamStatus, response["status"])
		assert.NotContains(t, response, "error")
	}
}

func TestArkFetchFallsBackToLocalStatusBeforeFirstPoll(t *testing.T) {
	// 首次轮询之前 task.Data 存的是提交响应，没有 status 字段，
	// 此时必须用 new-api 自己的任务状态兜底，且不能泄露上游 ID。
	internal := `{"code":"success","data":{
		"task_id":"task_abc","status":"SUBMITTED","submit_time":1787274123,"updated_at":1787274123,
		"data":{"id":"cgt-20260826-secret"}
	}}`
	_, response, recorder := arkFetch(t, "task_abc", internal)

	assert.Equal(t, "task_abc", response["id"])
	assert.Equal(t, "queued", response["status"])
	assert.NotContains(t, response, "content")
	assert.NotContains(t, recorder.Body.String(), "cgt-20260826-secret")
}

func TestArkFetchMapsTaskStatuses(t *testing.T) {
	cases := []struct {
		name           string
		taskStatus     string
		upstreamStatus string
		expected       string
	}{
		{"not started", "NOT_START", "", "queued"},
		{"queued", "QUEUED", "queued", "queued"},
		{"running", "IN_PROGRESS", "running", "running"},
		{"succeeded", "SUCCESS", "succeeded", "succeeded"},
		{"failed", "FAILURE", "failed", "failed"},
		{"cancelled keeps upstream terminal state", "FAILURE", "cancelled", "cancelled"},
		{"expired keeps upstream terminal state", "FAILURE", "expired", "expired"},
		{"unknown local state falls back to upstream", "UNKNOWN", "succeeded", "succeeded"},
		{"unknown on both sides stays non terminal", "UNKNOWN", "", "running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			internal := `{"code":"success","data":{"task_id":"task_abc","status":"` + tc.taskStatus +
				`","data":{"status":"` + tc.upstreamStatus + `"}}}`
			_, response, _ := arkFetch(t, "task_abc", internal)
			assert.Equal(t, tc.expected, response["status"])
		})
	}
}

func TestArkFetchReportsFailureError(t *testing.T) {
	internal := `{"code":"success","data":{
		"task_id":"task_abc","status":"FAILURE","fail_reason":"local reason",
		"data":{"status":"failed","error":{"code":"task_failed","message":"video generation failed"}}
	}}`
	_, response, _ := arkFetch(t, "task_abc", internal)

	assert.Equal(t, "failed", response["status"])
	assert.Equal(t, map[string]any{"code": "task_failed", "message": "video generation failed"}, response["error"])
}

func TestArkFetchUsesFailReasonWhenUpstreamErrorMissing(t *testing.T) {
	internal := `{"code":"success","data":{"task_id":"task_abc","status":"FAILURE","fail_reason":"upstream timeout","data":{}}}`
	_, response, _ := arkFetch(t, "task_abc", internal)

	assert.Equal(t, map[string]any{"code": "task_failed", "message": "upstream timeout"}, response["error"])
}

func TestArkFetchRequiresTaskID(t *testing.T) {
	engine := newArkTestEngine(func(c *gin.Context) {
		t.Fatal("handler must not run without a task id")
	})
	req := httptest.NewRequest(http.MethodGet, ArkVideoTaskPath+"/%20", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

// 内层 defer 先于外层 Recovery 的 defer 执行。若 panic 时仍提交缓冲区，调用方会拿到
// 一个 200 加半截 JSON，Recovery 再也改不成 500。
func TestArkResponseDoesNotCommitOnPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(gin.RecoveryWithWriter(io.Discard))
	group := engine.Group(ArkVideoTaskPath)
	group.Use(ArkRequestConvert())
	group.POST("", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": "task_abc"})
		panic("relay exploded after writing")
	})

	req := httptest.NewRequest(http.MethodPost, ArkVideoTaskPath, bytes.NewBufferString(`{"model":"m","content":[{"type":"text","text":"p"}]}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "task_abc")
}
