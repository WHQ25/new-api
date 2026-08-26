package middleware

// 火山引擎方舟（Ark / 豆包 Seedance）官方视频协议的入站兼容层。
//
// 客户端可以直接把官方 SDK 指向本网关：
//
//	POST /api/v3/contents/generations/tasks       提交任务
//	GET  /api/v3/contents/generations/tasks/{id}  查询任务
//
// 本中间件把官方形状的请求改写成 new-api 内部统一的 /v1/video/generations 形状，
// 再在响应写回时把内部形状还原成官方形状。转换全部集中在这里，relay 层不感知
// 该协议；`c.Request.URL.Path` 被改写而 `c.Request.RequestURI` 保持原样，便于排查。

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const (
	// ArkVideoTaskPath 是方舟官方的视频任务路径，同时用作本兼容层的注册前缀。
	ArkVideoTaskPath = "/api/v3/contents/generations/tasks"
	// arkUnifiedVideoPath 是 new-api 内部统一的视频任务路径。
	arkUnifiedVideoPath = "/v1/video/generations"
)

// ArkRequestConvert 把方舟官方协议的请求/响应与 new-api 内部协议互相转换。
func ArkRequestConvert() gin.HandlerFunc {
	return func(c *gin.Context) {
		original := c.Writer
		writer := &arkResponseWriter{ResponseWriter: original}
		c.Writer = writer
		completed := false
		defer func() {
			// 还原后，外层中间件看到的仍是真实 writer 上记录的状态码与响应大小。
			c.Writer = original
			if !completed {
				// panic 沿调用栈上抛时，本 defer 先于外层 Recovery 的 defer 执行。
				// 此时提交缓冲区会抢先写出 200 和半截 body，Recovery 再也改不成 500。
				// 丢弃缓冲，把尚未提交的真实 writer 留给 Recovery。
				return
			}
			writer.flush(c.Request.Method)
		}()

		if rewriteArkRequest(c) {
			c.Next()
		}
		completed = true
	}
}

// rewriteArkRequest 把方舟官方形状的请求改写成 new-api 内部统一的
// /v1/video/generations 形状。校验失败时它已经写出了错误响应，返回 false。
func rewriteArkRequest(c *gin.Context) bool {
	if c.Request.Method == http.MethodGet {
		taskID := c.Param("task_id")
		if strings.TrimSpace(taskID) == "" {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "task_id is required")
			return false
		}
		c.Request.URL.Path = arkUnifiedVideoPath + "/" + url.PathEscape(taskID)
		return true
	}

	var originalReq map[string]any
	if err := common.UnmarshalBodyReusable(c, &originalReq); err != nil {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid request body")
		return false
	}
	if message := arkRequestBoundsError(originalReq); message != "" {
		abortWithOpenAiMessage(c, http.StatusBadRequest, message)
		return false
	}

	modelName, _ := originalReq["model"].(string)
	unifiedReq := map[string]any{
		"model":  modelName,
		"prompt": arkPromptFromContent(originalReq["content"]),
		// 整个官方请求体原样进 metadata：豆包适配器会把 metadata 反序列化进上游
		// 请求体顶层，因此 resolution / ratio / seed / camera_fixed / watermark /
		// generate_audio / return_last_frame / frames 等字段无需逐个映射即可生效。
		// content 数组（含 role、video_url、audio_url）也由此原样透传，所以这里
		// 不再额外填顶层 images —— 那会与 metadata.content 在同一个切片上按下标
		// 合并，产生 type 与 *_url 交叉污染的条目。
		"metadata": originalReq,
	}
	// 官方契约保证按数组顺序处理并保留文本、素材及其 role，因此告诉适配器 content
	// 已是上游原生形状，不要按统一协议的口径重建它。标记走 gin context，不走 metadata：
	// metadata 完全由客户端控制，可以伪造。
	common.SetContextKey(c, constant.ContextKeyNativeTaskContent, true)
	// duration 提到顶层，才能走到 ValidateBasicTaskRequest 的时长上界校验，
	// 并保证计费估算与下发给上游的时长同源。方舟允许显式 -1 / 0 表示「由模型决定」，
	// 这类值留在 metadata 里透传，不参与计费。
	if seconds, ok := arkInt(originalReq["duration"]); ok && seconds > 0 {
		unifiedReq["duration"] = seconds
	}

	jsonData, err := common.Marshal(unifiedReq)
	if err == nil {
		err = common.ReplaceRequestBody(c, jsonData)
	}
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusInternalServerError, "failed to convert request body")
		return false
	}
	c.Request.URL.Path = arkUnifiedVideoPath
	return true
}

// 方舟官方协议的帧数取值域：frames ∈ [29, 289] 且 frames = 25 + 4n。
const (
	arkFramesBase = 25
	arkFramesStep = 4
	arkMinFrames  = 29
	arkMaxFrames  = 289
)

// arkRequestBoundsError 在协议边界上拒绝越界的数值，返回空串表示通过。
// duration 与 frames 都会成为输出时长、进而成为计费乘数，必须在这里以 400 拦下，
// 而不是留给下游钳制或让上游决定。非整数同样拒绝：内部 dto.IntValue 只接受整数
// 和可解析的数字字符串，浮点值会一路走到构建上游请求体时才失败成 500。
func arkRequestBoundsError(req map[string]any) string {
	if raw, ok := req["duration"]; ok && raw != nil {
		seconds, ok := arkInt(raw)
		if !ok || seconds < -1 || seconds > relaycommon.MaxTaskDurationSeconds {
			return fmt.Sprintf("duration must be an integer between -1 and %d", relaycommon.MaxTaskDurationSeconds)
		}
	}
	if raw, ok := req["frames"]; ok && raw != nil {
		frames, ok := arkInt(raw)
		if !ok || frames < arkMinFrames || frames > arkMaxFrames || (frames-arkFramesBase)%arkFramesStep != 0 {
			return fmt.Sprintf("frames must be an integer between %d and %d satisfying frames = %d + %dn",
				arkMinFrames, arkMaxFrames, arkFramesBase, arkFramesStep)
		}
	}
	return ""
}

// arkInt 解析 JSON 数字为整数。字符串、布尔、浮点小数一律返回 false。
func arkInt(raw any) (int, bool) {
	value, ok := raw.(float64)
	if !ok || value != math.Trunc(value) || value < math.MinInt32 || value > math.MaxInt32 {
		return 0, false
	}
	return int(value), true
}

// arkPromptFromContent 取出 content 数组里的全部 text 条目作为提示词。
// 素材条目留在 metadata.content 中透传，不在这里拆分。
func arkPromptFromContent(content any) string {
	items, ok := content.([]any)
	if !ok {
		return ""
	}
	texts := make([]string, 0, len(items))
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if itemMap["type"] != "text" {
			continue
		}
		if text, ok := itemMap["text"].(string); ok && text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// ============================
// 响应转换
// ============================

// arkResponseWriter 缓冲下游响应，等 handler 链结束后再按方舟官方形状写回。
// 提交与查询两条路径的响应分别由 relay 层的适配器和 RelayTaskFetch 直接写出，
// 没有可供改写的中间钩子，因此在这里统一拦截。
type arkResponseWriter struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
}

func (w *arkResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *arkResponseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *arkResponseWriter) WriteString(s string) (int, error) {
	return w.body.WriteString(s)
}

func (w *arkResponseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *arkResponseWriter) Size() int {
	return w.body.Len()
}

func (w *arkResponseWriter) Written() bool {
	return w.status != 0 || w.body.Len() > 0
}

// WriteHeaderNow 与 Flush 必须显式覆写：内嵌 gin.ResponseWriter 的实现会直接提交
// 真实连接上的响应头，绕过缓冲，让后续的形状转换失去意义。这两条路由只回小段 JSON，
// 不存在需要流式下发的场景，因此这里都退化成空操作，真正的提交由 flush 完成。
func (w *arkResponseWriter) WriteHeaderNow() {}

func (w *arkResponseWriter) Flush() {}

// flush 把缓冲的响应转换成方舟形状后写入真实的 ResponseWriter。
// 非 200 响应（校验失败、额度不足、上游错误）原样透传：它们是 relay 层统一的错误
// 形状，截断反而会让上游诊断信息丢失。200 但转换不出方舟形状时则 fail-closed —
// 内部形状里带着上游原始响应，直接回退会把上游任务 ID 漏给调用方。
func (w *arkResponseWriter) flush(method string) {
	status, body := w.Status(), w.body.Bytes()
	if status == http.StatusOK {
		converted, ok := convertArkResponse(method, body)
		if !ok {
			common.SysError(fmt.Sprintf("ark video: cannot convert internal response, body: %s", body))
			status = http.StatusInternalServerError
			converted, _ = common.Marshal(dto.TaskError{Code: "invalid_response", Message: "failed to convert upstream response"})
		}
		body = converted
		w.ResponseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.ResponseWriter.Write(body)
	}
}

func convertArkResponse(method string, body []byte) ([]byte, bool) {
	if method == http.MethodPost {
		return convertArkSubmitResponse(body)
	}
	return convertArkFetchResponse(body)
}

// convertArkSubmitResponse 把内部提交响应压成官方形状 {"id": "task_xxx"}。
// 官方协议只暴露本网关生成的公开任务 ID，绝不回传上游任务 ID。
func convertArkSubmitResponse(body []byte) ([]byte, bool) {
	var submitted struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
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
	converted, err := common.Marshal(map[string]any{"id": taskID})
	if err != nil {
		return nil, false
	}
	return converted, true
}

// convertArkFetchResponse 把内部查询响应 {"code":"success","data":TaskDto}
// 转成官方查询形状。任务的本地状态是权威来源，上游原始响应只用于补充
// resolution / duration / usage 等元数据，以及把 FAILURE 细分成 cancelled / expired。
func convertArkFetchResponse(body []byte) ([]byte, bool) {
	var fetched dto.TaskResponse[dto.TaskDto]
	if err := common.Unmarshal(body, &fetched); err != nil {
		return nil, false
	}
	task := fetched.Data
	if task.TaskID == "" {
		return nil, false
	}

	var upstream map[string]any
	_ = common.Unmarshal(task.Data, &upstream)
	upstreamStatus, _ := upstream["status"].(string)

	out := map[string]any{
		"id":     task.TaskID,
		"status": arkTaskStatus(task.Status, upstreamStatus),
	}
	if modelName := arkOriginModelName(task.Properties); modelName != "" {
		out["model"] = modelName
	}
	// 上游元数据按白名单复制，避免把 cgt-xxx 之类的上游任务 ID 泄露给调用方。
	for _, key := range []string{"resolution", "ratio", "duration", "framespersecond", "seed", "service_tier", "tools", "usage"} {
		if value, ok := upstream[key]; ok && value != nil {
			out[key] = value
		}
	}
	if content := arkTaskContent(upstream, task.ResultURL); len(content) > 0 {
		out["content"] = content
	}
	// 官方协议约定 error 仅在失败时出现。cancelled / expired 是「没跑完」而非「跑失败」，
	// 只有上游确实给了 error 时才带上，不再自造 task_failed。
	if taskError := arkTaskError(upstream, task.FailReason, out["status"] == arkStatusFailed); taskError != nil {
		out["error"] = taskError
	}
	out["created_at"] = task.SubmitTime
	if task.SubmitTime == 0 {
		out["created_at"] = task.CreatedAt
	}
	out["updated_at"] = task.UpdatedAt

	converted, err := common.Marshal(out)
	if err != nil {
		return nil, false
	}
	return converted, true
}

const (
	arkStatusQueued    = "queued"
	arkStatusRunning   = "running"
	arkStatusSucceeded = "succeeded"
	arkStatusFailed    = "failed"
	arkStatusCancelled = "cancelled"
	arkStatusExpired   = "expired"
)

// arkTaskStatus 把 new-api 的任务状态映射回方舟官方状态。
// new-api 把 cancelled / expired 都并进 FAILURE，这里借上游原始状态还原出来。
func arkTaskStatus(taskStatus, upstreamStatus string) string {
	switch taskStatus {
	case "SUCCESS":
		return arkStatusSucceeded
	case "FAILURE":
		if upstreamStatus == arkStatusCancelled || upstreamStatus == arkStatusExpired {
			return upstreamStatus
		}
		return arkStatusFailed
	case "NOT_START", "SUBMITTED", "QUEUED":
		return arkStatusQueued
	case "IN_PROGRESS":
		return arkStatusRunning
	}
	// 本地状态未知（UNKNOWN 或历史数据）：回落到上游状态，仍无法判定时按运行中处理，
	// 让调用方继续轮询而不是误判成终态。
	switch upstreamStatus {
	case arkStatusQueued, "pending":
		return arkStatusQueued
	case arkStatusSucceeded, arkStatusFailed, arkStatusCancelled, arkStatusExpired:
		return upstreamStatus
	}
	return arkStatusRunning
}

// arkTaskContent 组装官方的 content 对象。官方文档只定义了 video_url 和
// last_frame_url 两个键，这里按白名单取：整体复制会把上游后续新增的任何字段
// （包括可能的上游任务 ID）一并转发出去，保密性就不再是 fail-closed 的。
func arkTaskContent(upstream map[string]any, resultURL string) map[string]any {
	content := map[string]any{}
	if upstreamContent, ok := upstream["content"].(map[string]any); ok {
		for _, key := range []string{"video_url", "last_frame_url"} {
			if value, ok := upstreamContent[key].(string); ok && value != "" {
				content[key] = value
			}
		}
	}
	if _, ok := content["video_url"]; !ok && resultURL != "" {
		content["video_url"] = resultURL
	}
	return content
}

func arkTaskError(upstream map[string]any, failReason string, failed bool) map[string]any {
	code, message := "", ""
	if upstreamError, ok := upstream["error"].(map[string]any); ok {
		code, _ = upstreamError["code"].(string)
		message, _ = upstreamError["message"].(string)
	}
	if code == "" && message == "" && !failed {
		return nil
	}
	if code == "" {
		code = "task_failed"
	}
	if message == "" {
		message = failReason
	}
	if message == "" {
		message = "video generation failed"
	}
	return map[string]any{"code": code, "message": message}
}

// arkOriginModelName 取任务记录里的对外模型名。TaskDto.Properties 是 any，
// JSON 往返后是 map。
func arkOriginModelName(properties any) string {
	propertiesMap, ok := properties.(map[string]any)
	if !ok {
		return ""
	}
	modelName, _ := propertiesMap["origin_model_name"].(string)
	return modelName
}
