package doubao

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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
			payload, err := a.convertToRequestPayload(&tc.req)
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
