package middleware

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeKlingV3(t *testing.T, body []byte, ok bool) map[string]any {
	t.Helper()
	require.True(t, ok)
	var out map[string]any
	require.NoError(t, common.Unmarshal(body, &out))
	return out
}

// 提交响应只能暴露本网关生成的公开任务 ID，且时间戳要换成官方的毫秒口径。
func TestKlingV3SubmitResponseShape(t *testing.T) {
	body, ok := convertKlingV3SubmitResponse([]byte(
		`{"id":"pub-1","task_id":"pub-1","object":"video.task","status":"queued","created_at":1787842125}`))
	out := decodeKlingV3(t, body, ok)

	assert.Equal(t, float64(0), out["code"])
	data := out["data"].(map[string]any)
	assert.Equal(t, "pub-1", data["id"])
	assert.Equal(t, klingV3StatusSubmitted, data["status"])
	assert.Equal(t, float64(1787842125000), data["create_time"])
}

// 查询响应的 data 是数组，成功时带 outputs。内部秒级时间戳必须换算成毫秒，
// 差三个数量级会让调用方把 2026 年的任务显示成 1970 年。
func TestKlingV3FetchResponseShape(t *testing.T) {
	body, ok := convertKlingV3FetchResponse([]byte(`{"code":"success","message":"","data":{
		"task_id":"pub-1","status":"SUCCESS","result_url":"https://example.com/v.mp4",
		"submit_time":1787842125,"updated_at":1787842184}}`))
	out := decodeKlingV3(t, body, ok)

	assert.Equal(t, float64(0), out["code"])
	entries := out["data"].([]any)
	require.Len(t, entries, 1)

	entry := entries[0].(map[string]any)
	assert.Equal(t, "pub-1", entry["id"])
	assert.Equal(t, klingV3StatusSucceeded, entry["status"])
	assert.Equal(t, float64(1787842125000), entry["create_time"])
	assert.Equal(t, float64(1787842184000), entry["update_time"])

	outputs := entry["outputs"].([]any)
	require.Len(t, outputs, 1)
	output := outputs[0].(map[string]any)
	assert.Equal(t, "video", output["type"])
	assert.Equal(t, "https://example.com/v.mp4", output["url"])
}

// 内部状态机比官方的四态细，映射错会让调用方把还在跑的任务当成终态而停止轮询。
func TestKlingV3StatusMapping(t *testing.T) {
	for internal, want := range map[string]string{
		"NOT_START":   klingV3StatusSubmitted,
		"SUBMITTED":   klingV3StatusSubmitted,
		"QUEUED":      klingV3StatusSubmitted,
		"UNKNOWN":     klingV3StatusSubmitted,
		"IN_PROGRESS": klingV3StatusProcessing,
		"SUCCESS":     klingV3StatusSucceeded,
		"FAILURE":     klingV3StatusFailed,
	} {
		assert.Equal(t, want, klingV3TaskStatus(internal), internal)
	}
}

// 失败时官方用 message 承载原因；未完成的任务不应该带 outputs。
func TestKlingV3FetchFailureCarriesReason(t *testing.T) {
	body, ok := convertKlingV3FetchResponse([]byte(`{"code":"success","data":{
		"task_id":"pub-1","status":"FAILURE","fail_reason":"content moderation blocked",
		"submit_time":1787842125,"updated_at":1787842184}}`))
	entry := decodeKlingV3(t, body, ok)["data"].([]any)[0].(map[string]any)

	assert.Equal(t, klingV3StatusFailed, entry["status"])
	assert.Equal(t, "content moderation blocked", entry["message"])
	assert.NotContains(t, entry, "outputs")
}

// 转换不出官方形状时必须 fail-closed。内部形状里带着上游原始响应，直接回退会把上游
// 任务 ID 泄露给调用方。
func TestKlingV3ConversionFailsClosed(t *testing.T) {
	for _, body := range []string{
		`{"code":"success","data":{"status":"SUCCESS"}}`, // 没有 task_id
		`not json`,
	} {
		_, ok := convertKlingV3FetchResponse([]byte(body))
		assert.False(t, ok, body)
	}
	_, ok := convertKlingV3SubmitResponse([]byte(`{"object":"video.task"}`))
	assert.False(t, ok)
}

// 错误响应默认归一化、显式放行。上游的自由文本会被字面回显进 TaskError.Message，
// 无法逐字段推断里面有什么，放行 allowlist 之外的错误码就等于打开泄露路径。
func TestKlingV3ErrorRedaction(t *testing.T) {
	safe := klingV3ErrorBody(400, []byte(`{"code":"invalid_request","message":"duration must be 3..15"}`))
	assert.Contains(t, string(safe), "duration must be 3..15")

	// fail_to_fetch_task 的 message 是上游响应体的字面回显。
	leaky := klingV3ErrorBody(500, []byte(
		`{"code":"fail_to_fetch_task","message":"upstream task 1500062068-AigcVideoTask-abc failed"}`))
	assert.NotContains(t, string(leaky), "1500062068")
	assert.Contains(t, string(leaky), klingV3GenericErrorMessage)

	// 鉴权/分发失败走 {"error":{...}}，由本网关本地产生。
	local := klingV3ErrorBody(401, []byte(`{"error":{"message":"invalid token","type":"one_api_error"}}`))
	assert.Contains(t, string(local), "invalid token")
}
