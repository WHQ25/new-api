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
	"slices"
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

		common.SetContextKey(c, constant.ContextKeyInboundRequestPath, c.Request.URL.Path)
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

// 方舟官方协议的数值取值域。frames 另有 frames = 25 + 4n 的网格约束。
const (
	arkFramesBase = 25
	arkFramesStep = 4
)

// arkIntRanges 是官方文档给出的整数字段取值域。duration 与 frames 决定输出时长、
// 进而成为计费乘数，必须在这里以 400 拦下，不能留给下游钳制或让上游决定。
var arkIntRanges = map[string][2]int{
	"duration":                {-1, relaycommon.MaxTaskDurationSeconds},
	"frames":                  {29, 289},
	"seed":                    {-1, math.MaxInt32},
	"execution_expires_after": {3600, 259200},
	"priority":                {0, 9},
}

var (
	arkStringFields = []string{"model", "omni_reference_task_type", "resolution", "ratio", "output_format", "safety_identifier", "service_tier", "callback_url"}
	arkBoolFields   = []string{"generate_audio", "watermark", "camera_fixed", "return_last_frame", "draft"}
)

// arkRequestBoundsError 在协议边界上按官方文档校验字段类型与取值域，返回空串表示通过。
// 放行错误类型的代价不只是「上游拒绝」：metadata 要到构建上游请求体时才反序列化，
// 那时预扣费已经发生，失败会表现成 500 build_request_failed，进而触发跨渠道重试并把
// 客户端的格式错误记到渠道账上。
func arkRequestBoundsError(req map[string]any) string {
	content, ok := req["content"].([]any)
	if !ok || len(content) == 0 {
		return "content must be a non-empty array"
	}
	if message := arkContentError(content); message != "" {
		return message
	}
	for _, field := range arkStringFields {
		if raw, ok := req[field]; ok && raw != nil {
			if _, ok := raw.(string); !ok {
				return field + " must be a string"
			}
		}
	}
	for _, field := range arkBoolFields {
		if raw, ok := req[field]; ok && raw != nil {
			if _, ok := raw.(bool); !ok {
				return field + " must be a boolean"
			}
		}
	}
	if raw, ok := req["tools"]; ok && raw != nil {
		items, ok := raw.([]any)
		if !ok {
			return "tools must be an array"
		}
		for index, item := range items {
			itemMap, ok := item.(map[string]any)
			if !ok {
				return fmt.Sprintf("tools[%d] must be an object", index)
			}
			if _, ok := itemMap["type"].(string); !ok {
				return fmt.Sprintf("tools[%d].type must be a string", index)
			}
		}
	}
	for _, field := range []string{"duration", "frames", "seed", "execution_expires_after", "priority"} {
		raw, present := req[field]
		if !present || raw == nil {
			continue
		}
		bounds := arkIntRanges[field]
		value, ok := arkInt(raw)
		if !ok || value < bounds[0] || value > bounds[1] {
			return fmt.Sprintf("%s must be an integer between %d and %d", field, bounds[0], bounds[1])
		}
		if field == "frames" && (value-arkFramesBase)%arkFramesStep != 0 {
			return fmt.Sprintf("frames must satisfy frames = %d + %dn", arkFramesBase, arkFramesStep)
		}
	}
	return ""
}

// arkMediaContentTypes 是方舟官方 content 支持的媒体条目类型，每类的 type 名同时
// 就是它必需的 URL 字段名。官方文档给出的是这三类媒体加上 text，共四类；
// 「由所选模型决定」
// 说的是 media 项的 role 取值，不是 content 的 JSON 形状。上游 DTO 也只承载这四类，
// 放行未知 type 不会带来前向兼容，只会让该条目的载荷在 typed round-trip 中被静默丢掉。
var arkMediaContentTypes = []string{"image_url", "video_url", "audio_url"}

// arkContentError 校验 content 每一项的形状。形状错误若放行，要到预扣费之后
// 反序列化上游请求体时才暴露，那时既已扣费、又会被当成上游故障重试其它渠道。
func arkContentError(content []any) string {
	for index, raw := range content {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Sprintf("content[%d] must be an object", index)
		}
		itemType, ok := item["type"].(string)
		if !ok {
			return fmt.Sprintf("content[%d].type must be a string", index)
		}
		if role, present := item["role"]; present && role != nil {
			if _, ok := role.(string); !ok {
				return fmt.Sprintf("content[%d].role must be a string", index)
			}
		}
		// text 无论出现在哪种条目上都会参与上游 DTO 反序列化，所以只要出现就要检查；
		// type 为 text 时再额外要求它必须存在。
		if _, present := item["text"]; present || itemType == "text" {
			if _, ok := item["text"].(string); !ok {
				return fmt.Sprintf("content[%d].text must be a string", index)
			}
		}
		if itemType != "text" && !slices.Contains(arkMediaContentTypes, itemType) {
			return fmt.Sprintf("content[%d].type must be one of text, %s", index, strings.Join(arkMediaContentTypes, ", "))
		}
		// 逐个检查出现过的 URL 字段，包括与条目 type 不匹配的那些：它们照样会参与
		// 上游 DTO 的反序列化，写坏了一样会在构建请求体时失败。
		for _, field := range arkMediaContentTypes {
			if _, present := item[field]; !present && field != itemType {
				continue
			}
			media, ok := item[field].(map[string]any)
			if !ok {
				return fmt.Sprintf("content[%d].%s must be an object", index, field)
			}
			if _, ok := media["url"].(string); !ok {
				return fmt.Sprintf("content[%d].%s.url must be a string", index, field)
			}
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
// 本地产生的非 200 响应（校验失败、额度不足、模型不可用）原样透传，它们正是调用方
// 需要的诊断信息；只有回显了上游原始响应体的那一类会被归一化。200 但转换不出方舟
// 形状时 fail-closed — 内部形状里带着上游原始响应，直接回退会泄露上游任务 ID。
func (w *arkResponseWriter) flush(method string) {
	status, body := w.Status(), w.body.Bytes()
	if status != http.StatusOK {
		body = redactArkUpstreamError(body)
	}
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

// arkSafeErrorCodes 是可以把原始 message 透传给调用方的错误码：它们全部由本网关
// 本地产生，内容是调用方需要、且能据此修正请求的诊断信息。
//
// 采用 allowlist 而非 denylist 是刻意的。上游的响应体会在多处被字面回显进
// TaskError.Message（上游非 200 的 fail_to_fetch_task、HTTP 200 但 JSON 非法的
// unmarshal_response_body_failed 等），那是第三方自由文本，无法逐字段推断里面有什么。
// 官方协议承诺不向调用方暴露任何内部任务 ID，只有「默认归一化、显式放行」才能保证
// 后续新增的错误码不会重新打开泄露路径。
var arkSafeErrorCodes = map[string]bool{
	// 请求校验
	"invalid_request":          true,
	"invalid_seconds":          true,
	"invalid_multipart_form":   true,
	"invalid_api_platform":     true,
	"read_request_body_failed": true,
	// 定价与模型
	"missing_resolution":      true,
	"video_token_price_error": true,
	"model_price_error":       true,
	"model_mapping_failed":    true,
	"model_not_found":         true,
	// 额度
	"insufficient_user_quota":        true,
	"pre_consume_token_quota_failed": true,
	// 任务查询
	"task_not_exist":       true,
	"invalid_relay_mode":   true,
	"task_channel_disable": true,
}

const arkGenericErrorMessage = "the upstream video provider rejected or failed this request"

// redactArkUpstreamError 归一化所有可能夹带上游自由文本的错误响应。
// 原文通过 SysError 留在服务端日志，调用方拿到的 X-Oneapi-Request-Id 足以关联回去。
func redactArkUpstreamError(body []byte) []byte {
	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return arkSafeErrorBody("")
	}
	// abortWithOpenAiMessage 写的是 {"error": {...}} 形状，全部由鉴权、分发和本中间件
	// 本地产生，不含上游响应体。
	if _, ok := raw["error"]; ok {
		return body
	}
	code, _ := raw["code"].(string)
	if arkSafeErrorCodes[code] {
		return body
	}
	if message, _ := raw["message"].(string); message != "" {
		common.SysError(fmt.Sprintf("ark video: redacted error response, code=%s message=%s", code, message))
	}
	return arkSafeErrorBody(code)
}

// arkSafeErrorBody 构造不含任何上游文本的错误响应。marshal 失败时回退到预置的字面量，
// 而不是回退原始 body——那样就不是 fail-closed 了。
func arkSafeErrorBody(code string) []byte {
	if code == "" {
		code = "upstream_error"
	}
	// Data 一并清空：relay 层将来若把上游原始结构放进 data，就会重新打开嵌套泄露路径。
	safe, err := common.Marshal(dto.TaskError{Code: code, Message: arkGenericErrorMessage})
	if err != nil {
		return []byte(`{"code":"upstream_error","message":"` + arkGenericErrorMessage + `","data":null}`)
	}
	return safe
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
	// 上游元数据逐字段按官方文档的类型投影，而不是按顶层键复制值：整体复制 usage /
	// tools 这类嵌套结构，会把上游后续在里面新增的任何字段（包括嵌套形式的上游任务 ID）
	// 一并转发出去，保密性就不是 fail-closed 的。
	for _, key := range []string{"resolution", "ratio", "service_tier"} {
		if value, ok := upstream[key].(string); ok && value != "" {
			out[key] = value
		}
	}
	for _, key := range []string{"duration", "framespersecond", "seed"} {
		if value, ok := upstream[key].(float64); ok {
			out[key] = value
		}
	}
	if usage := arkTaskUsage(upstream); len(usage) > 0 {
		out["usage"] = usage
	}
	if tools := arkTaskTools(upstream); len(tools) > 0 {
		out["tools"] = tools
	}
	if content := arkTaskContent(upstream, task.ResultURL); len(content) > 0 {
		out["content"] = content
	}
	// 官方协议约定 error 仅在失败时出现。cancelled / expired 是「没跑完」而非「跑失败」，
	// 只有上游确实给了 error 时才带上，不再自造 task_failed。
	if taskError := arkTaskError(upstream, out["status"] == arkStatusFailed); taskError != nil {
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

// arkTaskUsage 只投影官方文档定义的两个用量字段。
func arkTaskUsage(upstream map[string]any) map[string]any {
	upstreamUsage, ok := upstream["usage"].(map[string]any)
	if !ok {
		return nil
	}
	usage := map[string]any{}
	for _, key := range []string{"completion_tokens", "total_tokens"} {
		if value, ok := upstreamUsage[key].(float64); ok {
			usage[key] = value
		}
	}
	return usage
}

// arkTaskTools 只投影官方文档定义的 type 字段。
func arkTaskTools(upstream map[string]any) []map[string]any {
	items, ok := upstream["tools"].([]any)
	if !ok {
		return nil
	}
	tools := make([]map[string]any, 0, len(items))
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if toolType, ok := itemMap["type"].(string); ok && toolType != "" {
			tools = append(tools, map[string]any{"type": toolType})
		}
	}
	return tools
}

// arkTaskError 返回失败任务的错误对象。
//
// 这里刻意不回传上游的 code/message。上游任务 ID 可以出现在这两段自由文本的任意位置，
// 而按 task.Data 顶层 id 做精确替换并不可靠——ParseTaskResult 不要求上游响应带 id，
// 一份只有 status 和 error 的响应同样会被记成 FAILURE，那时就没有可替换的串了。
// 官方协议把「不暴露内部任务 ID」列为硬约束，这里只能 fail-closed。
// 具体失败原因不会丢：轮询入库时已经写进任务记录的 fail_reason 和 data，管理员可查。
// 这里不再另外记日志——查询是可重复的，轮询会把同一条失败放大成无数行。
func arkTaskError(upstream map[string]any, failed bool) map[string]any {
	if _, hasUpstreamError := upstream["error"].(map[string]any); !failed && !hasUpstreamError {
		return nil
	}
	return map[string]any{"code": "task_failed", "message": "video generation failed"}
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
