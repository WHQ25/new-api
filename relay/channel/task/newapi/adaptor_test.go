package newapi

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			name:       "completed carries url and usage",
			body:       `{"task_id":"task_x","status":"completed","metadata":{"url":"https://cdn/v.mp4"},"usage":{"total_tokens":40594}}`,
			wantStatus: model.TaskStatusSuccess,
			wantTokens: 40594,
			wantURL:    "https://cdn/v.mp4",
		},
		{
			name:       "failed keeps upstream message",
			body:       `{"task_id":"task_x","status":"failed","error":{"code":"task_failed","message":"video generation failed"}}`,
			wantStatus: model.TaskStatusFailure,
			wantReason: "video generation failed",
		},
		{
			name:       "cancelled is terminal",
			body:       `{"task_id":"task_x","status":"cancelled"}`,
			wantStatus: model.TaskStatusFailure,
			wantReason: "task cancelled",
		},
		{
			name:       "expired is terminal",
			body:       `{"task_id":"task_x","status":"expired"}`,
			wantStatus: model.TaskStatusFailure,
			wantReason: "task expired",
		},
		{
			name:       "queued keeps polling",
			body:       `{"task_id":"task_x","status":"queued"}`,
			wantStatus: model.TaskStatusSubmitted,
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
