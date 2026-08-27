package common

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayInfoGetFinalRequestRelayFormatPrefersExplicitFinal(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToConversionChain(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToRelayFormat(t *testing.T) {
	info := &RelayInfo{
		RelayFormat: types.RelayFormatGemini,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatGemini), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatNilReceiver(t *testing.T) {
	var info *RelayInfo
	require.Equal(t, types.RelayFormat(""), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoMetaTypedNilReceiver(t *testing.T) {
	var info *RelayInfo
	var meta convmeta.Meta = info

	assert.Empty(t, meta.GetOriginModelName())
	assert.Empty(t, meta.GetUpstreamModelName())
	assert.False(t, meta.HasChannelMeta())
	assert.Zero(t, meta.GetChannelID())
	assert.Zero(t, meta.GetChannelType())
	assert.False(t, meta.GetIsStream())
	assert.Empty(t, meta.GetReasoningEffort())
	assert.Zero(t, meta.GetEstimatePromptTokens())
	assert.Zero(t, meta.GetSendResponseCount())

	assert.NotPanics(t, func() {
		meta.SetReasoningEffort("high")
		meta.IncrSendResponseCount()
		meta.AppendRequestConversion(types.RelayFormatClaude)
	})

	firstState := meta.EnsureClaudeConvertInfo()
	secondState := meta.EnsureClaudeConvertInfo()
	require.NotNil(t, firstState)
	require.NotNil(t, secondState)
	assert.Equal(t, convmeta.LastMessageTypeNone, firstState.LastMessagesType)
	assert.NotSame(t, firstState, secondState)

	firstOptions := meta.ConvOptions()
	secondOptions := meta.ConvOptions()
	require.NotNil(t, firstOptions)
	require.NotNil(t, secondOptions)
	assert.NotSame(t, firstOptions, secondOptions)
	assert.NotNil(t, firstOptions.Claude.DefaultMaxTokens)
	assert.NotNil(t, firstOptions.Gemini.SupportsImagine)
	assert.NotNil(t, firstOptions.Gemini.SafetySetting)
	assert.NotNil(t, firstOptions.PreserveThinkingSuffix)
}

func TestGenRelayInfoCapturesRequestReasoningEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		path        string
		relayFormat types.RelayFormat
		request     dto.Request
		expected    string
	}{
		{
			name:        "OpenAI chat top-level effort",
			path:        "/v1/chat/completions",
			relayFormat: types.RelayFormatOpenAI,
			request:     &dto.GeneralOpenAIRequest{Model: "gpt-5.6-sol", ReasoningEffort: " high "},
			expected:    "high",
		},
		{
			name:        "OpenRouter nested chat effort",
			path:        "/v1/chat/completions",
			relayFormat: types.RelayFormatOpenAI,
			request:     &dto.GeneralOpenAIRequest{Model: "anthropic/claude", Reasoning: json.RawMessage(`{"effort":"xhigh"}`)},
			expected:    "xhigh",
		},
		{
			name:        "OpenAI Responses effort",
			path:        "/v1/responses",
			relayFormat: types.RelayFormatOpenAIResponses,
			request:     &dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", Reasoning: &dto.Reasoning{Effort: "max"}},
			expected:    "max",
		},
		{
			name:        "explicit none is preserved",
			path:        "/v1/responses",
			relayFormat: types.RelayFormatOpenAIResponses,
			request:     &dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", Reasoning: &dto.Reasoning{Effort: "none"}},
			expected:    "none",
		},
		{
			name:        "non-string nested effort is ignored",
			path:        "/v1/chat/completions",
			relayFormat: types.RelayFormatOpenAI,
			request:     &dto.GeneralOpenAIRequest{Model: "anthropic/claude", Reasoning: json.RawMessage(`{"effort":42}`)},
			expected:    "",
		},
		{
			name:        "Claude output config effort",
			path:        "/v1/messages",
			relayFormat: types.RelayFormatClaude,
			request:     &dto.ClaudeRequest{Model: "claude-opus-4-7", OutputConfig: json.RawMessage(`{"effort":"medium"}`)},
			expected:    "medium",
		},
		{
			name:        "Gemini thinking level",
			path:        "/v1beta/models/gemini-3-pro:generateContent",
			relayFormat: types.RelayFormatGemini,
			request: &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{
				ThinkingConfig: &dto.GeminiThinkingConfig{ThinkingLevel: "low"},
			}},
			expected: "low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", tt.path, nil)

			info, err := GenRelayInfo(ctx, tt.relayFormat, tt.request, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, info.ReasoningEffort)
		})
	}
}

func TestInitChannelMetaRestoresRequestReasoningEffortForRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	request := &dto.OpenAIResponsesRequest{
		Model:     "gpt-5.6-sol",
		Reasoning: &dto.Reasoning{Effort: "max"},
	}
	info, err := GenRelayInfo(ctx, types.RelayFormatOpenAIResponses, request, nil)
	require.NoError(t, err)

	info.SetReasoningEffort("high")
	info.InitChannelMeta(ctx)
	assert.Equal(t, "max", info.ReasoningEffort)

	info.SetReasoningEffort("low")
	info.InitChannelMeta(ctx)
	assert.Equal(t, "max", info.ReasoningEffort)
}

// 统一协议入站的调用方只给顶层 size，方舟只认 resolution/ratio 档位。计费估算和上游
// 请求体都从这里取值，所以这张表就是「按哪个档位收费 = 按哪个档位生成」的契约本身。
func TestTaskSubmitReqRequestedResolution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  TaskSubmitReq
		want string
	}{
		{"metadata tier", TaskSubmitReq{Metadata: map[string]any{"resolution": "1080p"}}, "1080p"},
		{"metadata alias normalized", TaskSubmitReq{Metadata: map[string]any{"resolution": "HD"}}, "720p"},
		{"size pixels landscape", TaskSubmitReq{Size: "1280x720"}, "720p"},
		{"size pixels portrait keeps short side", TaskSubmitReq{Size: "720x1280"}, "720p"},
		{"size pixels square", TaskSubmitReq{Size: "480x480"}, "480p"},
		{"size pixels 4k", TaskSubmitReq{Size: "3840x2160"}, "4k"},
		{"size pixels star separator", TaskSubmitReq{Size: "1920*1080"}, "1080p"},
		{"size tier label", TaskSubmitReq{Size: "480p"}, "480p"},
		{"metadata wins over size", TaskSubmitReq{Size: "3840x2160", Metadata: map[string]any{"resolution": "480p"}}, "480p"},
		{"unknown label kept for accurate error", TaskSubmitReq{Metadata: map[string]any{"resolution": "1440p"}}, "1440p"},
		{"absent", TaskSubmitReq{}, ""},
		{"non-string metadata ignored", TaskSubmitReq{Metadata: map[string]any{"resolution": 720}}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.req.RequestedResolution())
		})
	}
}

// 只识别计费像素表覆盖的比例。认出一个表外比例并下发给上游，会让上游按该比例生成、
// 计费却回退按 16:9 估算——空串保持「交给上游默认」，不制造新的计费偏差。
func TestTaskSubmitReqRequestedAspectRatio(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  TaskSubmitReq
		want string
	}{
		{"metadata ratio", TaskSubmitReq{Metadata: map[string]any{"ratio": "9:16"}}, "9:16"},
		{"metadata aspect_ratio fallback", TaskSubmitReq{Metadata: map[string]any{"aspect_ratio": "1:1"}}, "1:1"},
		{"size pixels 16:9", TaskSubmitReq{Size: "1280x720"}, "16:9"},
		{"size pixels 9:16", TaskSubmitReq{Size: "720x1280"}, "9:16"},
		{"size pixels square", TaskSubmitReq{Size: "480x480"}, "1:1"},
		{"metadata wins over size", TaskSubmitReq{Size: "1280x720", Metadata: map[string]any{"ratio": "1:1"}}, "1:1"},
		{"metadata ratio 21:9", TaskSubmitReq{Metadata: map[string]any{"ratio": "21:9"}}, "21:9"},
		{"metadata ratio 4:3", TaskSubmitReq{Metadata: map[string]any{"ratio": "4:3"}}, "4:3"},
		{"metadata ratio 3:4", TaskSubmitReq{Metadata: map[string]any{"ratio": "3:4"}}, "3:4"},
		// 方舟按输入内容自动选比例，是 Seedance 1.5 及以上的默认值。
		{"adaptive is forwarded verbatim", TaskSubmitReq{Metadata: map[string]any{"ratio": "adaptive"}}, "adaptive"},
		{"size pixels 4:3", TaskSubmitReq{Size: "1440x1080"}, "4:3"},
		{"size pixels 3:4", TaskSubmitReq{Size: "1080x1440"}, "3:4"},
		{"unsupported pixels left to upstream", TaskSubmitReq{Size: "1000x333"}, ""},
		{"absent", TaskSubmitReq{}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.req.RequestedAspectRatio())
		})
	}
}

// duration = -1 是「由上游模型自选时长」，与「未指定时长」不是一回事：未指定时上游按
// 自己的默认时长生成，自选时会在该模型的时长区间内挑一个，最长可到上界。
// 计费必须能区分，否则会按默认时长预扣、按上界生成。
func TestRequestsSelfSelectedDuration(t *testing.T) {
	cases := []struct {
		name string
		req  TaskSubmitReq
		want bool
	}{
		{name: "a top-level sentinel", req: TaskSubmitReq{Duration: DurationSelfSelect}, want: true},
		{name: "the sentinel as a seconds string", req: TaskSubmitReq{Seconds: "-1"}, want: true},
		{
			// 方舟官方协议入站时整个请求体都在 metadata 里。
			name: "the sentinel in metadata",
			req:  TaskSubmitReq{Metadata: map[string]interface{}{"duration": -1}},
			want: true,
		},
		{
			name: "the sentinel as a metadata string",
			req:  TaskSubmitReq{Metadata: map[string]interface{}{"duration": "-1"}},
			want: true,
		},
		{
			// 显式时长优先于 metadata，与 RequestedOutputSeconds 的取值顺序一致。
			name: "an explicit duration is not self-selection",
			req:  TaskSubmitReq{Duration: 5, Metadata: map[string]interface{}{"duration": -1}},
			want: false,
		},
		{name: "an absent duration is not self-selection", req: TaskSubmitReq{}, want: false},
		{
			name: "other negatives are not the sentinel",
			req:  TaskSubmitReq{Metadata: map[string]interface{}{"duration": -5}},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.req.RequestsSelfSelectedDuration())
			// 自选时长没有具体秒数，不能被当成一个可计费的时长读出来。
			if tc.want {
				assert.Equal(t, 0, tc.req.RequestedOutputSeconds())
			}
		})
	}
}

// StrictIntFromAny 与 BoundedIntFromAny 的区别是刻意的：后者为用量饱和转换而生，
// 会截断也会钳位。用在契约校验上，29.9 会被当成合法的 29 帧收下、-1.5 会被截成
// 「由模型自选时长」的哨兵 -1。
func TestStrictIntFromAny(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want int
		ok   bool
	}{
		{name: "an int", raw: 289, want: 289, ok: true},
		{name: "a whole float", raw: float64(289), want: 289, ok: true},
		{name: "a negative whole float", raw: float64(-1), want: -1, ok: true},
		{name: "an integer string", raw: " -1 ", want: -1, ok: true},
		{name: "a fractional float is refused", raw: 29.9},
		{name: "a fraction that truncates onto the sentinel is refused", raw: -1.5},
		{name: "a fractional string is refused", raw: "29.9"},
		{name: "NaN is refused", raw: math.NaN()},
		{name: "infinity is refused", raw: math.Inf(1)},
		{name: "a value past int32 is refused", raw: float64(math.MaxInt32) + 1},
		{name: "an int32-overflowing string is refused", raw: "2147483648"},
		{name: "an int32-overflowing uint32 is refused", raw: uint32(math.MaxInt32) + 1},
		{name: "an int32-overflowing int64 is refused", raw: int64(math.MaxInt32) + 1},
		{name: "a wrapped negative arriving as uint64 is refused", raw: uint64(18446744073686646784)},
		{name: "a bool is refused", raw: true},
		{name: "nil is refused", raw: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := StrictIntFromAny(tc.raw)
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
