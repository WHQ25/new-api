package doubao

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 上游请求体的时长必须和 relay/helper 的计费估算取同一个来源。任何一侧漏读一种写法，
// 都会让我们按 A 秒计费而上游按自己的默认时长生成。
func TestConvertToRequestPayloadDurationMatchesBillingSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want *dto.IntValue
	}{
		{
			name: "top-level duration",
			req:  relaycommon.TaskSubmitReq{Prompt: "p", Duration: 4},
			want: ptrIntValue(4),
		},
		{
			name: "seconds string",
			req:  relaycommon.TaskSubmitReq{Prompt: "p", Seconds: "8"},
			want: ptrIntValue(8),
		},
		{
			name: "metadata duration",
			req:  relaycommon.TaskSubmitReq{Prompt: "p", Metadata: map[string]any{"duration": 6}},
			want: ptrIntValue(6),
		},
		{
			name: "metadata seconds",
			req:  relaycommon.TaskSubmitReq{Prompt: "p", Metadata: map[string]any{"seconds": "12"}},
			want: ptrIntValue(12),
		},
		{
			name: "top-level wins over metadata",
			req:  relaycommon.TaskSubmitReq{Prompt: "p", Duration: 4, Metadata: map[string]any{"duration": 30}},
			want: ptrIntValue(4),
		},
		{
			name: "unspecified leaves upstream default",
			req:  relaycommon.TaskSubmitReq{Prompt: "p"},
			want: nil,
		},
		{
			name: "metadata duration is bounded",
			req:  relaycommon.TaskSubmitReq{Prompt: "p", Metadata: map[string]any{"duration": 999999}},
			want: ptrIntValue(relaycommon.MaxTaskDurationSeconds),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := &TaskAdaptor{}
			payload, err := a.convertToRequestPayload(&tc.req, false)
			require.NoError(t, err)

			assert.Equal(t, tc.want, payload.Duration)
			assert.Equal(t, tc.req.RequestedOutputSeconds(), intValueOrZero(payload.Duration))
		})
	}
}

// 上游请求体的分辨率/宽高比必须和 relay/helper 的计费估算取同一个来源。统一协议入站的
// 调用方只给顶层 size，漏读它就会按 size 计费、却让方舟用它自己的默认分辨率生成。
func TestConvertToRequestPayloadResolutionMatchesBillingSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		req           relaycommon.TaskSubmitReq
		wantResolutio string
		wantRatio     string
	}{
		{
			name:          "unified size maps to ark tier and ratio",
			req:           relaycommon.TaskSubmitReq{Prompt: "p", Size: "1280x720"},
			wantResolutio: "720p",
			wantRatio:     "16:9",
		},
		{
			name:          "portrait size keeps short side tier",
			req:           relaycommon.TaskSubmitReq{Prompt: "p", Size: "720x1280"},
			wantResolutio: "720p",
			wantRatio:     "9:16",
		},
		{
			name:          "ark metadata still wins",
			req:           relaycommon.TaskSubmitReq{Prompt: "p", Size: "3840x2160", Metadata: map[string]any{"resolution": "480p", "ratio": "1:1"}},
			wantResolutio: "480p",
			wantRatio:     "1:1",
		},
		{
			name:          "metadata alias is normalized for upstream",
			req:           relaycommon.TaskSubmitReq{Prompt: "p", Metadata: map[string]any{"resolution": "HD"}},
			wantResolutio: "720p",
			wantRatio:     "",
		},
		{
			name:          "unsupported ratio passes through untouched",
			req:           relaycommon.TaskSubmitReq{Prompt: "p", Metadata: map[string]any{"resolution": "1080p", "ratio": "21:9"}},
			wantResolutio: "1080p",
			wantRatio:     "21:9",
		},
		{
			name:          "unspecified leaves upstream default",
			req:           relaycommon.TaskSubmitReq{Prompt: "p"},
			wantResolutio: "",
			wantRatio:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := &TaskAdaptor{}
			payload, err := a.convertToRequestPayload(&tc.req, false)
			require.NoError(t, err)

			assert.Equal(t, tc.wantResolutio, payload.Resolution)
			assert.Equal(t, tc.wantRatio, payload.Ratio)
			// 计费估算读的是同一个入口，两边不可能算出不同的档位。
			assert.Equal(t, tc.req.RequestedResolution(), payload.Resolution)
		})
	}
}

func ptrIntValue(v int) *dto.IntValue {
	iv := dto.IntValue(v)
	return &iv
}

func intValueOrZero(v *dto.IntValue) int {
	if v == nil {
		return 0
	}
	return int(*v)
}

// 终态判定驱动退款：把 cancelled/expired 当成"还在跑"会让任务一直轮询到超时清理，
// 客户要等 TASK_TIMEOUT_MINUTES 才拿到退款。
func TestParseTaskResultTerminalStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		body       string
		wantStatus model.TaskStatus
		wantReason string
		wantTokens int
		wantURL    string
	}{
		{
			name:       "succeeded carries url and usage",
			body:       `{"id":"task_x","status":"succeeded","content":{"video_url":"https://cdn/v.mp4"},"usage":{"completion_tokens":40594,"total_tokens":40594}}`,
			wantStatus: model.TaskStatusSuccess,
			wantTokens: 40594,
			wantURL:    "https://cdn/v.mp4",
		},
		{
			name:       "failed keeps upstream message",
			body:       `{"id":"task_x","status":"failed","error":{"code":"task_failed","message":"video generation failed"}}`,
			wantStatus: model.TaskStatusFailure,
			wantReason: "video generation failed",
		},
		{
			name:       "cancelled is terminal",
			body:       `{"id":"task_x","status":"cancelled"}`,
			wantStatus: model.TaskStatusFailure,
			wantReason: "task cancelled",
		},
		{
			name:       "expired is terminal",
			body:       `{"id":"task_x","status":"expired"}`,
			wantStatus: model.TaskStatusFailure,
			wantReason: "task expired",
		},
		{
			name:       "queued keeps polling",
			body:       `{"id":"task_x","status":"queued"}`,
			wantStatus: model.TaskStatusQueued,
		},
		{
			name:       "unknown status keeps polling",
			body:       `{"id":"task_x","status":"something_new"}`,
			wantStatus: model.TaskStatusInProgress,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := &TaskAdaptor{}
			info, err := a.ParseTaskResult([]byte(tc.body))
			require.NoError(t, err)

			assert.Equal(t, tc.wantStatus, model.TaskStatus(info.Status))
			assert.Equal(t, tc.wantReason, info.Reason)
			assert.Equal(t, tc.wantTokens, info.TotalTokens)
			assert.Equal(t, tc.wantURL, info.Url)
		})
	}
}

// 方舟官方协议入站时（middleware.ArkRequestConvert），整个官方请求体原样进 metadata。
// 素材的 role（first_frame / last_frame / reference_*）以及参考视频、参考音频必须
// 一路透传到上游请求体，否则首尾帧会退化成无标注参考图。
func TestConvertToRequestPayloadPreservesArkContentRoles(t *testing.T) {
	t.Parallel()

	req := relaycommon.TaskSubmitReq{
		Prompt: "keep the reference style",
		Model:  "doubao-seedance-2-5-260628",
		Metadata: map[string]any{
			"resolution": "720p",
			"content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/first.png"}, "role": "first_frame"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/last.png"}, "role": "last_frame"},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://x/motion.mp4"}, "role": "reference_video"},
				map[string]any{"type": "audio_url", "audio_url": map[string]any{"url": "https://x/voice.wav"}, "role": "reference_audio"},
				map[string]any{"type": "text", "text": "ignored, replaced by prompt"},
			},
		},
	}

	a := &TaskAdaptor{}
	payload, err := a.convertToRequestPayload(&req, false)
	require.NoError(t, err)

	require.Len(t, payload.Content, 5)
	assert.Equal(t, ContentItem{Type: "image_url", ImageURL: &MediaURL{URL: "https://x/first.png"}, Role: "first_frame"}, payload.Content[0])
	assert.Equal(t, ContentItem{Type: "image_url", ImageURL: &MediaURL{URL: "https://x/last.png"}, Role: "last_frame"}, payload.Content[1])
	assert.Equal(t, ContentItem{Type: "video_url", VideoURL: &MediaURL{URL: "https://x/motion.mp4"}, Role: "reference_video"}, payload.Content[2])
	assert.Equal(t, ContentItem{Type: "audio_url", AudioURL: &MediaURL{URL: "https://x/voice.wav"}, Role: "reference_audio"}, payload.Content[3])
	// 上游只认顶层 prompt：metadata.content 里的 text 项被剔除后重新追加到末尾。
	assert.Equal(t, ContentItem{Type: "text", Text: "keep the reference style"}, payload.Content[4])
	assert.Equal(t, "720p", payload.Resolution)
}

// 方舟官方契约保证 content 按数组顺序处理并保留文本、素材及其 role。统一协议入站的
// 口径（剔除 text 项、把顶层 prompt 追加到末尾）会移动文本位置，因此官方协议入站时
// 由 ContextKeyNativeTaskContent 切换成原样下发。
func TestConvertToRequestPayloadKeepsNativeContentOrder(t *testing.T) {
	t.Parallel()

	newReq := func() relaycommon.TaskSubmitReq {
		return relaycommon.TaskSubmitReq{
			Prompt: "joined prompt",
			Metadata: map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": "lead in"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/a.png"}, "role": "first_frame"},
				},
			},
		}
	}

	a := &TaskAdaptor{}

	native, err := a.convertToRequestPayload(lo.ToPtr(newReq()), true)
	require.NoError(t, err)
	require.Len(t, native.Content, 2)
	assert.Equal(t, ContentItem{Type: "text", Text: "lead in"}, native.Content[0])
	assert.Equal(t, ContentItem{Type: "image_url", ImageURL: &MediaURL{URL: "https://x/a.png"}, Role: "first_frame"}, native.Content[1])

	unified, err := a.convertToRequestPayload(lo.ToPtr(newReq()), false)
	require.NoError(t, err)
	require.Len(t, unified.Content, 2)
	assert.Equal(t, "image_url", unified.Content[0].Type)
	assert.Equal(t, ContentItem{Type: "text", Text: "joined prompt"}, unified.Content[1])
}

// 官方请求体里的 omni_reference_task_type 与 output_format 只能经 metadata 到达
// 适配器；typed 请求体漏掉它们就会静默丢弃，官方协议入站的调用方无从察觉。
func TestConvertToRequestPayloadCarriesFullArkFieldSet(t *testing.T) {
	t.Parallel()

	req := relaycommon.TaskSubmitReq{
		Prompt: "p",
		Metadata: map[string]any{
			"omni_reference_task_type": "i2v",
			"output_format":            "mp4",
			"frames":                   121,
			"safety_identifier":        "user-1",
			"service_tier":             "default",
		},
	}

	a := &TaskAdaptor{}
	payload, err := a.convertToRequestPayload(&req, false)
	require.NoError(t, err)

	assert.Equal(t, lo.ToPtr("i2v"), payload.OmniReferenceTaskType)
	assert.Equal(t, lo.ToPtr("mp4"), payload.OutputFormat)
	assert.Equal(t, ptrIntValue(121), payload.Frames)
	assert.Equal(t, "user-1", payload.SafetyIdentifier)
	assert.Equal(t, "default", payload.ServiceTier)
}

// 可选标量必须保留 presence：显式空串是调用方的选择，要原样下发给上游，
// 不能和「未提交」折叠成同一种结果。
func TestConvertToRequestPayloadPreservesExplicitEmptyOptionalStrings(t *testing.T) {
	t.Parallel()

	a := &TaskAdaptor{}

	absent, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Prompt: "p"}, false)
	require.NoError(t, err)
	assert.Nil(t, absent.OutputFormat)

	explicit, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "p",
		Metadata: map[string]any{"output_format": ""},
	}, false)
	require.NoError(t, err)
	require.NotNil(t, explicit.OutputFormat)
	assert.Equal(t, "", *explicit.OutputFormat)

	body, err := common.Marshal(explicit)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"output_format":""`)
}

// resolveVideoBilling 走完整链路：真实 JSON 请求体 → 通用校验 → 模型映射后的计费解析。
// 直接给 context 塞一个手工构造的 TaskSubmitReq 会绕过通用 validator，
// 而 duration=-1 这类取值恰恰是先被它拦下的。
func resolveVideoBilling(t *testing.T, body string, originModel, upstreamModel string) (float64, *taskdto.TaskError, relaycommon.TaskSubmitReq) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: upstreamModel},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	info.OriginModelName = originModel

	adaptor := &TaskAdaptor{}
	if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
		return 0, taskErr, relaycommon.TaskSubmitReq{}
	}
	seconds, taskErr := adaptor.ResolveVideoBilling(c, info)
	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	return seconds, taskErr, req
}

// 方舟的实际生成秒数不总等于请求里的 duration：frames 优先级更高，duration=-1 由模型自选。
// 只认 duration 会按未指定时长的 5 秒默认值收费，而上游生成的可能是 12 秒或 30 秒。
func TestResolveVideoBillingSeconds(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		model    string
		upstream string
		want     float64
	}{
		{
			// 289/24 = 12.0417 秒。取整到 13 秒会凭空多收 8%，官方换算就是 frames/24。
			name:  "frames wins over duration and bills the exact frame count",
			body:  `{"prompt":"a cat","duration":5,"metadata":{"frames":289}}`,
			model: "doubao-seedance-1-0-pro-250528",
			want:  289.0 / 24,
		},
		{
			name:  "an explicit self-selected duration bills the generation ceiling",
			body:  `{"prompt":"a cat","duration":-1}`,
			model: "doubao-seedance-2-0-260128",
			want:  15,
		},
		{
			// metadata 里的 -1 同样是自选：方舟官方协议入站时整个请求体都在 metadata 里。
			name:  "a self-selected duration in metadata bills the ceiling too",
			body:  `{"prompt":"a cat","metadata":{"duration":-1}}`,
			model: "doubao-seedance-2-0-260128",
			want:  15,
		},
		{
			// Seedance 2.5 的 duration 默认值就是 -1：不传也由模型自选。
			name:  "seedance 2.5 self-selects even when duration is absent",
			body:  `{"prompt":"a cat"}`,
			model: "doubao-seedance-2-5-260814",
			want:  30,
		},
		{
			name:  "an absent duration on other generations uses the generic default",
			body:  `{"prompt":"a cat"}`,
			model: "doubao-seedance-2-0-260128",
			want:  0,
		},
		{
			name:  "an explicit duration is left to the generic rule",
			body:  `{"prompt":"a cat","duration":8}`,
			model: "doubao-seedance-2-5-260814",
			want:  0,
		},
		{
			// 认不出模型时显式时长仍可用：不需要猜上游的默认值。
			name:  "an unknown model with an explicit duration keeps the generic rule",
			body:  `{"prompt":"a cat","duration":8}`,
			model: "some-custom-video-model",
			want:  0,
		},
		{
			name:  "the generation floor is accepted",
			body:  `{"prompt":"a cat","duration":4}`,
			model: "doubao-seedance-2-0-260128",
			want:  0,
		},
		{
			name:  "the generation ceiling is accepted",
			body:  `{"prompt":"a cat","duration":15}`,
			model: "doubao-seedance-2-0-260128",
			want:  0,
		},
		{
			// 1.0 的下界是 2 秒，与 1.5 及以后的 4 秒不同。
			name:  "the 1.0 floor is lower than later generations",
			body:  `{"prompt":"a cat","duration":2}`,
			model: "doubao-seedance-1-0-pro-250528",
			want:  0,
		},
		{
			// 渠道把 alias 映射到另一代 Seedance 时，契约要按最终请求的那个模型判断，
			// 否则 2.5 的自选时长会按通用的 5 秒预扣，少收到六分之一。
			name:     "the mapped upstream model decides the contract",
			body:     `{"prompt":"a cat"}`,
			model:    "my-video-alias",
			upstream: "doubao-seedance-2-5-260814",
			want:     30,
		},
		{
			// 上游模型名是 endpoint ID 时认不出代次，退回客户端请求的模型名。
			name:     "an endpoint id falls back to the requested model",
			body:     `{"prompt":"a cat"}`,
			model:    "doubao-seedance-2-5-260814",
			upstream: "ep-20260101120000-abcde",
			want:     30,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seconds, taskErr, _ := resolveVideoBilling(t, tc.body, tc.model, tc.upstream)
			require.Nil(t, taskErr)
			assert.InDelta(t, tc.want, seconds, 1e-9)
		})
	}
}

// frames 是计费乘数：越界值不能原样丢给上游去 400，预扣会先按那个数字扣走额度。
func TestResolveVideoBillingRejectsInvalidFrames(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		model string
	}{
		{
			name:  "a frame count far past the official ceiling",
			body:  `{"prompt":"a cat","metadata":{"frames":1000000000}}`,
			model: "doubao-seedance-1-0-pro-250528",
		},
		{
			name:  "below the official floor",
			body:  `{"prompt":"a cat","metadata":{"frames":24}}`,
			model: "doubao-seedance-1-0-pro-250528",
		},
		{
			// 官方要求 frames = 25 + 4n。
			name:  "off the official 25+4n grid",
			body:  `{"prompt":"a cat","metadata":{"frames":30}}`,
			model: "doubao-seedance-1-0-pro-250528",
		},
		{
			name:  "negative frames",
			body:  `{"prompt":"a cat","metadata":{"frames":-289}}`,
			model: "doubao-seedance-1-0-pro-250528",
		},
		{
			name:  "frames on a generation that does not support it",
			body:  `{"prompt":"a cat","metadata":{"frames":289}}`,
			model: "doubao-seedance-2-0-260128",
		},
		{
			// frames 只有 1.0 pro / pro fast 支持，同代的 lite 不支持。
			name:  "frames on the lite variant of a generation that supports it",
			body:  `{"prompt":"a cat","metadata":{"frames":289}}`,
			model: "doubao-seedance-1-0-lite-t2v-250428",
		},
		{
			// 截断后 29.9 会变成合法的 29 帧，预扣之后请求体才因为类型不符失败。
			name:  "a fractional frame count that truncates onto the grid",
			body:  `{"prompt":"a cat","metadata":{"frames":29.9}}`,
			model: "doubao-seedance-1-0-pro-250528",
		},
		{
			name:  "frames on an unknown model",
			body:  `{"prompt":"a cat","metadata":{"frames":289}}`,
			model: "some-custom-video-model",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, taskErr, _ := resolveVideoBilling(t, tc.body, tc.model, "")
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

// 时长与帧数是计费乘数：模型不认的取值不能按通用默认值预扣再原样下发，
// 那等于「按 5 秒收费、让上游拿一个我们没看懂的值去生成」。
func TestResolveVideoBillingRejectsUnsupportedDuration(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		model    string
		upstream string
	}{
		{
			// 官方只有 1.5 及以后的代次支持 duration = -1。
			name:  "self-selected duration on a generation that does not support it",
			body:  `{"prompt":"a cat","duration":-1}`,
			model: "doubao-seedance-1-0-pro-250528",
		},
		{
			// 认不出模型就不知道它认不认 -1，按一个猜的上界预扣比拒绝更糟。
			name:  "self-selected duration on an unknown model",
			body:  `{"prompt":"a cat","duration":-1}`,
			model: "some-custom-video-model",
		},
		{
			// 上游是另一个认不出的模型时不套 origin 的契约。
			name:     "self-selected duration when the mapping lands on an unknown model",
			body:     `{"prompt":"a cat","duration":-1}`,
			model:    "doubao-seedance-2-5-260814",
			upstream: "some-other-video-model",
		},
		{
			// 通用校验只看顶层字段，metadata 里的非法时长会照样进上游请求体。
			name:  "an invalid duration hidden in metadata",
			body:  `{"prompt":"a cat","metadata":{"duration":-5}}`,
			model: "doubao-seedance-2-5-260814",
		},
		{
			// -1.5 截断后会变成哨兵 -1。时长必须严格取整。
			name:  "a fractional duration that truncates onto the sentinel",
			body:  `{"prompt":"a cat","metadata":{"duration":-1.5}}`,
			model: "doubao-seedance-2-5-260814",
		},
		{
			// 通用校验只拦到 3600 秒。不按各档区间拦，2.0 会先按 3600 秒预扣再等上游拒绝。
			name:  "a duration far past the generation ceiling",
			body:  `{"prompt":"a cat","duration":3600}`,
			model: "doubao-seedance-2-0-260128",
		},
		{
			name:  "a duration past the generation ceiling in metadata",
			body:  `{"prompt":"a cat","metadata":{"duration":30}}`,
			model: "doubao-seedance-2-0-260128",
		},
		{
			// 官方区间下界：2.0 是 [4, 15]。
			name:  "a duration below the generation floor",
			body:  `{"prompt":"a cat","duration":3}`,
			model: "doubao-seedance-2-0-260128",
		},
		{
			// 认不出模型就不知道它的默认时长，按通用的 5 秒猜会少收五分之六。
			name:  "an absent duration on an unknown model",
			body:  `{"prompt":"a cat"}`,
			model: "some-custom-video-model",
		},
		{
			// 方舟允许直接拿接入点 ID 当模型名，串里不含型号信息。
			name:  "an absent duration on a direct ark endpoint id",
			body:  `{"prompt":"a cat"}`,
			model: "ep-20260101120000-abcde",
		},
		{
			// 同样常见：别名不带代次，映射到接入点 ID。
			name:     "an absent duration on an opaque alias mapped to an endpoint",
			body:     `{"prompt":"a cat"}`,
			model:    "my-video",
			upstream: "ep-20260101120000-abcde",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, taskErr, _ := resolveVideoBilling(t, tc.body, tc.model, tc.upstream)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

// 计费读到的音频档必须与上游实际生成的一致。方舟的 generate_audio 官方默认 true，
// 而请求体只下发 generate_audio——只写 audio 别名会让两边各读各的。
func TestResolveVideoBillingPinsAudio(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		model string
		want  interface{}
	}{
		{
			// 最常见的情况：什么都不传。1.5 pro 有声 16 元、无声 8 元。
			name:  "an absent preference becomes the upstream default",
			body:  `{"prompt":"a cat"}`,
			model: "doubao-seedance-1-5-pro-251215",
			want:  true,
		},
		{
			name:  "an explicit false is left alone",
			body:  `{"prompt":"a cat","metadata":{"generate_audio":false}}`,
			model: "doubao-seedance-1-5-pro-251215",
			want:  false,
		},
		{
			// audio 别名不会下发，不对齐就会变成上游按默认的有声档生成、计费按无声档。
			name:  "the audio alias is projected onto the native field",
			body:  `{"prompt":"a cat","metadata":{"audio":false}}`,
			model: "doubao-seedance-1-5-pro-251215",
			want:  false,
		},
		{
			// 上游只听 generate_audio，别名冲突时计费必须跟着它，否则会按有声档多收。
			name:  "the native field wins over a conflicting alias",
			body:  `{"prompt":"a cat","metadata":{"generate_audio":false,"audio":true}}`,
			model: "doubao-seedance-1-5-pro-251215",
			want:  false,
		},
		{
			// Seedance 1.0 不支持 generate_audio，不能凭空塞一个上游不认的字段。
			name:  "generations without audio support are untouched",
			body:  `{"prompt":"a cat"}`,
			model: "doubao-seedance-1-0-pro-250528",
			want:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, taskErr, req := resolveVideoBilling(t, tc.body, tc.model, "")
			require.Nil(t, taskErr)
			// 计费与下发必须读到同一个值：metadata 是计费的入口，payload 是上游收到的。
			assert.Equal(t, tc.want, req.Metadata["generate_audio"])
			assert.Equal(t, tc.want, req.Metadata["audio"])

			payload, err := (&TaskAdaptor{}).convertToRequestPayload(&req, false)
			require.NoError(t, err)
			if tc.want == nil {
				assert.Nil(t, payload.GenerateAudio)
				return
			}
			require.NotNil(t, payload.GenerateAudio)
			assert.Equal(t, tc.want, bool(*payload.GenerateAudio))
		})
	}
}

// duration=-1 必须显式下发。计费已按该模型时长区间的上界预扣，漏发就变成
// 「按上界收费、按上游默认时长生成」。
func TestConvertToRequestPayloadForwardsSelfSelectedDuration(t *testing.T) {
	_, taskErr, req := resolveVideoBilling(t, `{"prompt":"a cat","duration":-1}`, "doubao-seedance-2-0-260128", "")
	require.Nil(t, taskErr)

	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&req, false)
	require.NoError(t, err)
	require.NotNil(t, payload.Duration)
	assert.Equal(t, -1, int(*payload.Duration))
}

// frames 在场时不能再下发 duration：官方规定 frames 优先，同时给两个字段会让
// 「下发的」和「计费的」各自解释一遍。
func TestConvertToRequestPayloadOmitsDurationWhenFramesPresent(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt:   "a cat",
		Duration: 5,
		Metadata: map[string]interface{}{"frames": 289},
	}
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&req, true)

	require.NoError(t, err)
	require.NotNil(t, payload.Frames)
	assert.Equal(t, 289, int(*payload.Frames))
	assert.Nil(t, payload.Duration)
}

// generate_audio 的官方默认值是 true：客户端什么都不传，上游也会生成有声视频。

// frames 优先时必须清掉 duration。metadata 会整体反序列化进请求体，
// 同时下发两个字段等于让上游和计费各自解释一遍时长。
func TestConvertToRequestPayloadDropsMetadataDurationWhenFramesPresent(t *testing.T) {
	_, taskErr, req := resolveVideoBilling(t,
		`{"prompt":"a cat","metadata":{"frames":289,"duration":5}}`,
		"doubao-seedance-1-0-pro-250528", "")
	require.Nil(t, taskErr)

	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&req, false)
	require.NoError(t, err)
	require.NotNil(t, payload.Frames)
	assert.Equal(t, 289, int(*payload.Frames))
	assert.Nil(t, payload.Duration)
}

// 认不出上游模型时，音频维度没有普适默认值：接入点背后可能是默认有声的 Seedance 1.5，
// 也可能是根本不认识 generate_audio 的 1.0。凭空注入这个字段会让后者被上游直接拒绝，
// 不注入又会在配了 _audio 档的价目表下少收。只能按价目表分情况处理。
func TestResolveVideoBillingAudioOnUnidentifiedModel(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.video_token_price": `{
			"ep-audio-priced":{"1080p":70,"1080p_audio":77},
			"ep-no-audio":{"1080p":70}
		}`,
	}))

	// 价目表没有 _audio 档：这个维度本来就不参与计价，注入一个上游可能不认的字段
	// 只会把原本能跑的请求打挂。
	_, taskErr, req := resolveVideoBilling(t,
		`{"prompt":"a cat","duration":5,"metadata":{"resolution":"1080p"}}`, "ep-no-audio", "")
	require.Nil(t, taskErr)
	assert.Nil(t, req.Metadata["generate_audio"])

	// 价目表配了 _audio 档：上游语义和计费档位没法同时保住，必须让调用方说清楚。
	_, taskErr, _ = resolveVideoBilling(t,
		`{"prompt":"a cat","duration":5,"metadata":{"resolution":"1080p"}}`, "ep-audio-priced", "")
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)

	// 显式给了偏好就不需要猜，两种表都照常放行。
	for _, model := range []string{"ep-audio-priced", "ep-no-audio"} {
		_, taskErr, req := resolveVideoBilling(t,
			`{"prompt":"a cat","duration":5,"metadata":{"resolution":"1080p","generate_audio":false}}`, model, "")
		require.Nil(t, taskErr, model)
		assert.Equal(t, false, req.Metadata["generate_audio"], model)
	}
}
