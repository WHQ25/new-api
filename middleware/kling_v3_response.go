package middleware

// 可灵 3.0 官方协议的响应还原层，与 kling_v3_adapter.go 配套。

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 官方任务状态枚举。
const (
	klingV3StatusSubmitted  = "submitted"
	klingV3StatusProcessing = "processing"
	klingV3StatusSucceeded  = "succeeded"
	klingV3StatusFailed     = "failed"
)

// klingV3ResponseWriter 缓冲下游响应，等 handler 链结束后再按官方形状写回。
// 提交与查询两条路径的响应分别由 relay 层的适配器和 RelayTaskFetch 直接写出，
// 没有可供改写的中间钩子，因此在这里统一拦截。
type klingV3ResponseWriter struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
}

func (w *klingV3ResponseWriter) WriteHeader(status int) { w.status = status }

func (w *klingV3ResponseWriter) Write(b []byte) (int, error) { return w.body.Write(b) }

func (w *klingV3ResponseWriter) WriteString(s string) (int, error) { return w.body.WriteString(s) }

func (w *klingV3ResponseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *klingV3ResponseWriter) Size() int { return w.body.Len() }

func (w *klingV3ResponseWriter) Written() bool { return w.status != 0 || w.body.Len() > 0 }

// WriteHeaderNow 与 Flush 必须显式覆写：内嵌实现会直接提交真实连接上的响应头，
// 绕过缓冲让形状转换失去意义。这两条路由只回小段 JSON，不需要流式下发。
func (w *klingV3ResponseWriter) WriteHeaderNow() {}

func (w *klingV3ResponseWriter) Flush() {}

// flush 把缓冲的响应转换成官方形状后写入真实的 ResponseWriter。
// 200 但转换不出官方形状时 fail-closed——内部形状里带着上游原始响应，直接回退会把
// 上游任务 ID 泄露给调用方。
func (w *klingV3ResponseWriter) flush(method string) {
	status, body := w.Status(), w.body.Bytes()
	if status != http.StatusOK {
		body = klingV3ErrorBody(status, body)
	} else {
		converted, ok := convertKlingV3Response(method, body)
		if !ok {
			common.SysError(fmt.Sprintf("kling v3: cannot convert internal response, body: %s", body))
			status = http.StatusInternalServerError
			converted = klingV3ErrorBody(status, nil)
		}
		body = converted
	}
	w.ResponseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.ResponseWriter.Write(body)
	}
}

func convertKlingV3Response(method string, body []byte) ([]byte, bool) {
	if method == http.MethodPost {
		return convertKlingV3SubmitResponse(body)
	}
	return convertKlingV3FetchResponse(body)
}

// convertKlingV3SubmitResponse 把内部提交响应转成官方提交形状。
// 只暴露本网关生成的公开任务 ID，绝不回传上游任务 ID。
func convertKlingV3SubmitResponse(body []byte) ([]byte, bool) {
	var submitted struct {
		ID        string `json:"id"`
		TaskID    string `json:"task_id"`
		CreatedAt int64  `json:"created_at"`
	}
	if err := common.Unmarshal(body, &submitted); err != nil {
		return nil, false
	}
	taskID := submitted.TaskID
	if taskID == "" {
		taskID = submitted.ID
	}
	if taskID == "" {
		return nil, false
	}
	createdAt := klingV3Millis(submitted.CreatedAt)
	return klingV3Success(map[string]any{
		"id":          taskID,
		"status":      klingV3StatusSubmitted,
		"create_time": createdAt,
		"update_time": createdAt,
		"external_id": "",
	})
}

// convertKlingV3FetchResponse 把内部查询响应 {"code":"success","data":TaskDto}
// 转成官方查询形状。官方的 data 是数组（支持批量），这里恒为单元素。
func convertKlingV3FetchResponse(body []byte) ([]byte, bool) {
	var fetched dto.TaskResponse[dto.TaskDto]
	if err := common.Unmarshal(body, &fetched); err != nil {
		return nil, false
	}
	task := fetched.Data
	if task.TaskID == "" {
		return nil, false
	}

	status := klingV3TaskStatus(task.Status)
	entry := map[string]any{
		"id":          task.TaskID,
		"status":      status,
		"create_time": klingV3Millis(klingV3SubmitTime(task)),
		"update_time": klingV3Millis(task.UpdatedAt),
		"external_id": "",
	}
	// 官方在失败时用 message 承载失败原因。FailReason 由本网关和适配器写入，
	// 不是上游响应体的字面回显。
	if status == klingV3StatusFailed && task.FailReason != "" {
		entry["message"] = task.FailReason
	}
	if status == klingV3StatusSucceeded && task.ResultURL != "" {
		entry["outputs"] = []any{map[string]any{
			"type": "video",
			"url":  task.ResultURL,
		}}
	}
	return klingV3Success([]any{entry})
}

// klingV3SubmitTime 取任务的创建时刻，SubmitTime 缺失时回退到记录创建时间。
func klingV3SubmitTime(task dto.TaskDto) int64 {
	if task.SubmitTime != 0 {
		return task.SubmitTime
	}
	return task.CreatedAt
}

// klingV3TaskStatus 把内部任务状态映射成官方枚举。
func klingV3TaskStatus(status string) string {
	switch status {
	case string(model.TaskStatusSuccess):
		return klingV3StatusSucceeded
	case string(model.TaskStatusFailure):
		return klingV3StatusFailed
	case string(model.TaskStatusInProgress):
		return klingV3StatusProcessing
	default:
		// NOT_START / SUBMITTED / QUEUED / UNKNOWN 都还没开跑，官方只有 submitted
		// 表达这一段。
		return klingV3StatusSubmitted
	}
}

// klingV3Millis 把内部的秒级时间戳转成官方的毫秒时间戳。
func klingV3Millis(seconds int64) int64 {
	if seconds <= 0 {
		return 0
	}
	return seconds * 1000
}

func klingV3Success(data any) ([]byte, bool) {
	converted, err := common.Marshal(map[string]any{
		"code":    0,
		"message": "",
		"data":    data,
	})
	if err != nil {
		return nil, false
	}
	return converted, true
}

// klingV3SafeErrorCodes 是可以把原始 message 透传给调用方的内部错误码：它们全部由
// 本网关本地产生，内容是调用方需要、且能据此修正请求的诊断信息。
//
// 采用 allowlist 而非 denylist 是刻意的：上游响应体会在多处被字面回显进
// TaskError.Message，那是第三方自由文本，无法逐字段推断里面有什么。官方协议承诺
// 不向调用方暴露任何内部任务 ID，只有「默认归一化、显式放行」才能保证后续新增的
// 错误码不会重新打开泄露路径。
var klingV3SafeErrorCodes = map[string]bool{
	"invalid_request":                true,
	"invalid_metadata":               true,
	"invalid_content":                true,
	"invalid_model":                  true,
	"invalid_seconds":                true,
	"invalid_resolution":             true,
	"invalid_multipart_form":         true,
	"invalid_api_platform":           true,
	"read_request_body_failed":       true,
	"missing_resolution":             true,
	"video_token_price_error":        true,
	"model_price_error":              true,
	"model_mapping_failed":           true,
	"model_not_found":                true,
	"insufficient_user_quota":        true,
	"pre_consume_token_quota_failed": true,
	"task_not_exist":                 true,
	"invalid_relay_mode":             true,
	"task_channel_disable":           true,
}

const klingV3GenericErrorMessage = "the upstream video provider rejected or failed this request"

// klingV3ErrorBody 把内部错误响应归一化成官方错误形状 {"code","message"}。
//
// code 用 HTTP 状态码而不是可灵的业务错误码：官方错误码表有它自己的语义，把内部
// 错误硬映射过去会让调用方按错误的原因去重试或改参数。
func klingV3ErrorBody(status int, body []byte) []byte {
	message := klingV3GenericErrorMessage
	if len(body) > 0 {
		message = klingV3ErrorMessage(body)
	}
	encoded, err := common.Marshal(map[string]any{"code": status, "message": message})
	if err != nil {
		return []byte(`{"code":500,"message":"` + klingV3GenericErrorMessage + `"}`)
	}
	return encoded
}

// klingV3ErrorMessage 取出可以安全外传的错误文本，取不到就返回通用文案。
func klingV3ErrorMessage(body []byte) string {
	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return klingV3GenericErrorMessage
	}
	// abortKlingV3 写的就是 {"code": <int>, "message": ...}，由本中间件本地产生。
	if _, isLocal := raw["code"].(float64); isLocal {
		if message, ok := raw["message"].(string); ok && message != "" {
			return message
		}
		return klingV3GenericErrorMessage
	}
	// 鉴权与分发失败走 {"error": {"message": ...}}，同样是本地产生的。
	if errObj, ok := raw["error"].(map[string]any); ok {
		if message, ok := errObj["message"].(string); ok && message != "" {
			return message
		}
		return klingV3GenericErrorMessage
	}
	code, _ := raw["code"].(string)
	message, _ := raw["message"].(string)
	if klingV3SafeErrorCodes[code] && message != "" {
		return message
	}
	if message != "" {
		common.SysError(fmt.Sprintf("kling v3: redacted error response, code=%s message=%s", code, message))
	}
	return klingV3GenericErrorMessage
}

// abortKlingV3 直接以官方错误形状终止请求。
func abortKlingV3(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"code":       status,
		"message":    message,
		"request_id": c.GetString(common.RequestIdKey),
	})
	c.Abort()
}
