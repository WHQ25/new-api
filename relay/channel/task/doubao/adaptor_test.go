package doubao

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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
