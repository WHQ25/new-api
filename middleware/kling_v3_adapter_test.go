package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// submitContext 构造一次官方形状的提交请求，返回改写后的内部请求体。
func submitContext(t *testing.T, path, body string) (map[string]any, *httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ok := rewriteKlingV3Request(c)
	if !ok {
		return nil, recorder, false
	}
	var converted map[string]any
	require.NoError(t, common.UnmarshalBodyReusable(c, &converted))
	return converted, recorder, true
}

// 官方 settings 是嵌套的三态枚举，内部计费读的是扁平的布尔与档位字面量。映射错一处
// 就会出现按一个档位收费、按另一个档位生成。
func TestKlingV3SettingsMapToUnifiedDimensions(t *testing.T) {
	converted, _, ok := submitContext(t, KlingV3TextToVideoPath, `{
		"prompt": "a cat",
		"settings": {"resolution": "4k", "aspect_ratio": "9:16", "duration": 10, "audio": "native"}
	}`)
	require.True(t, ok)

	assert.Equal(t, "kling-v3", converted["model"])
	assert.Equal(t, "a cat", converted["prompt"])
	assert.Equal(t, float64(10), converted["duration"])

	metadata := converted["metadata"].(map[string]any)
	assert.Equal(t, "4k", metadata["resolution"])
	assert.Equal(t, "9:16", metadata["ratio"])
	assert.Equal(t, true, metadata["generate_audio"])
}

// 官方允许省略 resolution（默认 720p），内部计费却要求它必须是结构化字段，缺失直接
// 400。不补默认值，一个完全合法的官方请求会被我们拒掉。
func TestKlingV3FillsDefaultResolution(t *testing.T) {
	converted, _, ok := submitContext(t, KlingV3TextToVideoPath, `{"prompt": "a cat"}`)
	require.True(t, ok)

	metadata := converted["metadata"].(map[string]any)
	assert.Equal(t, klingV3DefaultResolution, metadata["resolution"])
	// 官方 audio 默认 off，上游默认也是不出声：不写这个键两边一致，写错才会出事。
	assert.NotContains(t, metadata, "generate_audio")
}

// audio 是三态枚举，native 与 original 都出声，只有 off 不出声。
func TestKlingV3AudioModes(t *testing.T) {
	for _, tc := range []struct {
		audio string
		want  bool
	}{{"native", true}, {"original", true}, {"off", false}} {
		t.Run(tc.audio, func(t *testing.T) {
			converted, _, ok := submitContext(t, KlingV3OmniVideoPath,
				`{"contents":[{"type":"prompt","text":"a cat"}],"settings":{"audio":"`+tc.audio+`"}}`)
			require.True(t, ok)
			metadata := converted["metadata"].(map[string]any)
			assert.Equal(t, tc.want, metadata["generate_audio"])
		})
	}
}

// contents 的素材标识必须落到统一协议的类型与角色上——计费按 video_url 判视频输入档，
// 适配器按 role 决定首帧还是参考。
func TestKlingV3ContentsMapToUnifiedMedia(t *testing.T) {
	converted, _, ok := submitContext(t, KlingV3OmniVideoPath, `{
		"contents": [
			{"type": "prompt", "text": "a parrot"},
			{"type": "first_frame", "url": "https://example.com/a.png"},
			{"type": "last_frame", "url": "https://example.com/b.png"},
			{"type": "refer_image", "url": "https://example.com/c.png"},
			{"type": "feature_video", "url": "https://example.com/d.mp4"},
			{"type": "element", "element_id": "858477278396170315", "id": "e1"}
		]
	}`)
	require.True(t, ok)

	assert.Equal(t, "a parrot", converted["prompt"])
	metadata := converted["metadata"].(map[string]any)

	content := metadata["content"].([]any)
	require.Len(t, content, 4)
	wantRoles := []struct{ mediaType, role string }{
		{relaycommon.TaskMediaTypeImage, relaycommon.TaskMediaRoleFirstFrame},
		{relaycommon.TaskMediaTypeImage, relaycommon.TaskMediaRoleLastFrame},
		{relaycommon.TaskMediaTypeImage, relaycommon.TaskMediaRoleReferenceImage},
		{relaycommon.TaskMediaTypeVideo, relaycommon.TaskMediaRoleReferenceVideo},
	}
	for i, want := range wantRoles {
		item := content[i].(map[string]any)
		assert.Equal(t, want.mediaType, item["type"])
		assert.Equal(t, want.role, item["role"])
		assert.NotEmpty(t, item[want.mediaType].(map[string]any)["url"])
	}

	subjects := metadata["subject_infos"].([]any)
	require.Len(t, subjects, 1)
	assert.Equal(t, "858477278396170315", subjects[0].(map[string]any)["id"])

	// 计费判视频输入档读的就是这个入口，必须能从转换结果里读出来。
	req := relaycommon.TaskSubmitReq{Metadata: metadata}
	assert.True(t, req.HasMediaType(relaycommon.TaskMediaTypeVideo))
}

// 官方把型号编在路径里，请求体没有 model 字段。推导错了会选到别的渠道或别的价目表。
func TestKlingV3ModelComesFromPath(t *testing.T) {
	for path, want := range map[string]string{
		KlingV3TextToVideoPath:  "kling-v3",
		KlingV3ImageToVideoPath: "kling-v3",
		KlingV3OmniVideoPath:    "kling-v3-omni",
	} {
		body := `{"contents":[{"type":"prompt","text":"a cat"}]}`
		if path == KlingV3TextToVideoPath {
			body = `{"prompt":"a cat"}`
		}
		converted, _, ok := submitContext(t, path, body)
		require.True(t, ok, path)
		assert.Equal(t, want, converted["model"], path)
	}
}

// 协议边界上的越界值必须以 400 拦下。放行的话要到构建上游请求体时才暴露，那时预扣费
// 已经发生，失败会被记成渠道故障并触发跨渠道重试。
func TestKlingV3RejectsOutOfContract(t *testing.T) {
	cases := []struct{ name, path, body string }{
		{"resolution outside the enum", KlingV3TextToVideoPath,
			`{"prompt":"a","settings":{"resolution":"480p"}}`},
		{"aspect_ratio outside the enum", KlingV3TextToVideoPath,
			`{"prompt":"a","settings":{"aspect_ratio":"21:9"}}`},
		{"audio outside the enum", KlingV3TextToVideoPath,
			`{"prompt":"a","settings":{"audio":"on"}}`},
		{"duration below the range", KlingV3TextToVideoPath,
			`{"prompt":"a","settings":{"duration":2}}`},
		{"duration above the range", KlingV3TextToVideoPath,
			`{"prompt":"a","settings":{"duration":16}}`},
		{"text-to-video without a prompt", KlingV3TextToVideoPath, `{}`},
		{"omni with empty contents", KlingV3OmniVideoPath, `{"contents":[]}`},
		{"unknown content type", KlingV3OmniVideoPath,
			`{"contents":[{"type":"refer_audio","url":"https://example.com/a.mp3"}]}`},
		{"media item without a url", KlingV3OmniVideoPath,
			`{"contents":[{"type":"first_frame"}]}`},
		{"element without an id", KlingV3OmniVideoPath,
			`{"contents":[{"type":"element"}]}`},
		// 收下一个永远不会被调用的回调地址，调用方会一直等下去。
		{"callback_url has no implementation", KlingV3TextToVideoPath,
			`{"prompt":"a","options":{"callback_url":"https://example.com/cb"}}`},
		// 关闭多镜头在上游没有对应参数，静默收下会给出调用方明确要求避免的结果。
		{"multi_shot cannot be disabled", KlingV3TextToVideoPath,
			`{"prompt":"a","settings":{"multi_shot":false}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, recorder, ok := submitContext(t, tc.path, tc.body)
			require.False(t, ok)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

// options 能映射的部分要真的映射过去，不能静默丢掉。
func TestKlingV3MapsOptions(t *testing.T) {
	converted, _, ok := submitContext(t, KlingV3TextToVideoPath, `{
		"prompt": "a cat",
		"options": {"external_task_id": "job-1", "watermark_info": {"enabled": true}}
	}`)
	require.True(t, ok)

	metadata := converted["metadata"].(map[string]any)
	assert.Equal(t, "job-1", metadata["session_context"])
	assert.Equal(t, "Enabled", metadata["logo_add"])
}

func fetchContext(t *testing.T, rawQuery string) (*gin.Context, *httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, KlingV3TasksPath+"?"+rawQuery, nil)
	return c, recorder, rewriteKlingV3Request(c)
}

// 查询要把 query 参数搬到内部认的位置上：内部先读 c.Param("task_id")，取不到才回退
// c.GetString("task_id")，而这条路由没有路径参数。
func TestKlingV3FetchInjectsTaskID(t *testing.T) {
	c, _, ok := fetchContext(t, "task_ids=task-123")
	require.True(t, ok)
	assert.Equal(t, "task-123", c.GetString("task_id"))
	assert.Equal(t, klingV3UnifiedVideoPath+"/task-123", c.Request.URL.Path)
}

// 批量查询与按自定义 ID 查询都不能静默降级：前者会丢掉除第一个以外的所有 ID，
// 后者会稳定查不到，两种情况调用方拿到的都是一个看起来正常的响应。
func TestKlingV3FetchRejectsUnsupportedQueries(t *testing.T) {
	for _, query := range []string{"", "task_ids=a,b", "external_task_ids=job-1"} {
		_, recorder, ok := fetchContext(t, query)
		require.False(t, ok, query)
		assert.Equal(t, http.StatusBadRequest, recorder.Code, query)
	}
}

// 中间件的产物要能被内部统一协议的公开取值口读出来——计费和适配器都从这几个入口取值，
// 读不到就意味着按默认档计费、按另一套参数生成。
func TestKlingV3OutputSatisfiesUnifiedContract(t *testing.T) {
	converted, _, ok := submitContext(t, KlingV3ImageToVideoPath, `{
		"contents": [
			{"type": "prompt", "text": "a cat"},
			{"type": "first_frame", "url": "https://example.com/a.png"}
		],
		"settings": {"resolution": "1080p", "duration": 8, "aspect_ratio": "16:9"}
	}`)
	require.True(t, ok)

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var req relaycommon.TaskSubmitReq
	require.NoError(t, common.Unmarshal(encoded, &req))

	assert.Equal(t, "1080p", req.RequestedResolution())
	assert.Equal(t, 8, req.RequestedOutputSeconds())
	assert.Equal(t, "16:9", req.RequestedAspectRatio())
	assert.False(t, req.HasMediaType(relaycommon.TaskMediaTypeVideo))
	require.Len(t, req.MediaItems(), 1)
	assert.Equal(t, relaycommon.TaskMediaRoleFirstFrame, req.MediaItems()[0].Role)
}
