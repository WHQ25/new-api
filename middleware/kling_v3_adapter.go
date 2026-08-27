package middleware

// 可灵（Kling）3.0 / 3.0 Omni 官方视频协议的入站兼容层。
//
// 客户端可以只把 base URL 从 api-beijing.klingai.com 改成本网关：
//
//	POST /text-to-video/kling-3.0        文生视频
//	POST /image-to-video/kling-3.0       图生视频
//	POST /omni-video/kling-3.0-omni      Omni 视频生成
//	GET  /tasks?task_ids={id}            查询任务
//
// 本中间件把官方形状改写成 new-api 内部统一的 /v1/video/generations 形状，响应写回
// 时再还原成官方形状（见 kling_v3_response.go）。
//
// 与 KlingRequestConvert 的区别：那个对接的是可灵旧版协议（/v1/videos/text2video，
// model_name + mode + aspect_ratio），3.0 是官方全面升级后的另一套契约，请求体、
// 端点、响应形状都不一样，因此单独成层而不是在旧层里加分支。

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const (
	KlingV3TextToVideoPath  = "/text-to-video/kling-3.0"
	KlingV3ImageToVideoPath = "/image-to-video/kling-3.0"
	KlingV3OmniVideoPath    = "/omni-video/kling-3.0-omni"
	KlingV3TasksPath        = "/tasks"

	klingV3UnifiedVideoPath = "/v1/video/generations"
)

// klingV3Models 把官方端点映射到本网关的模型名。官方把型号编在路径里，请求体没有
// model 字段，所以模型只能从路径推导。
var klingV3Models = map[string]string{
	KlingV3TextToVideoPath:  "kling-v3",
	KlingV3ImageToVideoPath: "kling-v3",
	KlingV3OmniVideoPath:    "kling-v3-omni",
}

// klingV3MediaTypes 把官方的素材标识映射到统一协议的类型与角色。
// feature_video（特征参考）与 base_video（待编辑）在计费上是同一个维度——都是视频
// 输入——所以都落到 reference_video；两者的语义差别体现在官方各自的音频约束上，
// 由适配器按模型契约校验。
var klingV3MediaTypes = map[string]struct{ mediaType, role string }{
	"first_frame":   {relaycommon.TaskMediaTypeImage, relaycommon.TaskMediaRoleFirstFrame},
	"last_frame":    {relaycommon.TaskMediaTypeImage, relaycommon.TaskMediaRoleLastFrame},
	"refer_image":   {relaycommon.TaskMediaTypeImage, relaycommon.TaskMediaRoleReferenceImage},
	"feature_video": {relaycommon.TaskMediaTypeVideo, relaycommon.TaskMediaRoleReferenceVideo},
	"base_video":    {relaycommon.TaskMediaTypeVideo, relaycommon.TaskMediaRoleReferenceVideo},
}

// 官方 settings 的取值域。越界值必须在协议边界以 400 拦下：放行的话要到构建上游
// 请求体时才暴露，那时预扣费已经发生，失败会被记成渠道故障并触发跨渠道重试。
var (
	klingV3Resolutions  = []string{"720p", "1080p", "4k"}
	klingV3AspectRatios = []string{"16:9", "9:16", "1:1"}
	klingV3AudioModes   = []string{"native", "original", "off"}
)

const (
	klingV3MinDuration = 3
	klingV3MaxDuration = 15
	// klingV3DefaultResolution 是官方 settings.resolution 的默认值。必须显式补上：
	// 内部计费要求分辨率是结构化字段，缺失会直接 400 missing_resolution，而官方契约
	// 允许省略它。
	klingV3DefaultResolution = "720p"
)

// KlingV3RequestConvert 把可灵 3.0 官方协议的请求/响应与 new-api 内部协议互相转换。
func KlingV3RequestConvert() gin.HandlerFunc {
	return func(c *gin.Context) {
		original := c.Writer
		writer := &klingV3ResponseWriter{ResponseWriter: original}
		c.Writer = writer
		completed := false
		defer func() {
			c.Writer = original
			if !completed {
				// panic 上抛时本 defer 先于 Recovery 执行。此时提交缓冲区会抢先写出
				// 200 和半截 body，Recovery 再也改不成 500。丢弃缓冲即可。
				return
			}
			writer.flush(c.Request.Method)
		}()

		common.SetContextKey(c, constant.ContextKeyInboundRequestPath, c.Request.URL.Path)
		if rewriteKlingV3Request(c) {
			c.Next()
		}
		completed = true
	}
}

func rewriteKlingV3Request(c *gin.Context) bool {
	if c.Request.Method == http.MethodGet {
		return rewriteKlingV3Fetch(c)
	}
	return rewriteKlingV3Submit(c)
}

// rewriteKlingV3Fetch 处理 GET /tasks?task_ids={id}。
func rewriteKlingV3Fetch(c *gin.Context) bool {
	if external := strings.TrimSpace(c.Query("external_task_ids")); external != "" {
		// 官方允许按自定义任务 ID 查询，本网关没有为它建索引，按 ID 查会静默落空。
		abortKlingV3(c, http.StatusBadRequest, "external_task_ids is not supported by this gateway, query by task_ids instead")
		return false
	}
	taskID := strings.TrimSpace(c.Query("task_ids"))
	if taskID == "" {
		abortKlingV3(c, http.StatusBadRequest, "task_ids is required")
		return false
	}
	if strings.Contains(taskID, ",") {
		// 官方支持逗号分隔的批量查询，内部查询接口一次只认一个任务。放行会让第二个
		// 之后的 ID 被静默丢弃，调用方拿到的却是一个看起来正常的数组。
		abortKlingV3(c, http.StatusBadRequest, "batch query is not supported by this gateway, request one task_id at a time")
		return false
	}
	// 内部按 c.Param("task_id") 取值，取不到时回退 c.GetString("task_id")；
	// 这条路由没有路径参数，只能从这里注入。
	c.Set("task_id", taskID)
	c.Request.URL.Path = klingV3UnifiedVideoPath + "/" + url.PathEscape(taskID)
	return true
}

// rewriteKlingV3Submit 把官方提交请求改写成内部统一形状。
func rewriteKlingV3Submit(c *gin.Context) bool {
	modelName, known := klingV3Models[c.Request.URL.Path]
	if !known {
		abortKlingV3(c, http.StatusNotFound, "unknown kling endpoint")
		return false
	}

	var originalReq map[string]any
	if err := common.UnmarshalBodyReusable(c, &originalReq); err != nil {
		abortKlingV3(c, http.StatusBadRequest, "invalid request body")
		return false
	}
	if message := klingV3BoundsError(originalReq, c.Request.URL.Path); message != "" {
		abortKlingV3(c, http.StatusBadRequest, message)
		return false
	}

	prompt, media, subjects := klingV3Contents(originalReq)
	settings, _ := originalReq["settings"].(map[string]any)

	metadata := map[string]any{"resolution": klingV3DefaultResolution}
	if resolution, ok := klingV3String(settings, "resolution"); ok {
		metadata["resolution"] = resolution
	}
	if ratio, ok := klingV3String(settings, "aspect_ratio"); ok {
		metadata["ratio"] = ratio
	}
	if audio, ok := klingV3String(settings, "audio"); ok {
		// native / original 都是「出声」，只有 off 不出声。计费与上游请求体读的都是
		// 这个布尔，不再关心官方的三态枚举。
		metadata["generate_audio"] = audio != "off"
	}
	if len(media) > 0 {
		metadata["content"] = media
	}
	if len(subjects) > 0 {
		metadata["subject_infos"] = subjects
	}
	if options, ok := originalReq["options"].(map[string]any); ok {
		klingV3ApplyOptions(metadata, options)
	}

	unifiedReq := map[string]any{
		"model":    modelName,
		"prompt":   prompt,
		"metadata": metadata,
	}
	// duration 提到顶层才能走到 ValidateBasicTaskRequest 的时长校验，并保证计费估算
	// 与下发给上游的时长同源。
	if seconds, ok := klingV3Int(settings, "duration"); ok {
		unifiedReq["duration"] = seconds
	}

	jsonData, err := common.Marshal(unifiedReq)
	if err == nil {
		err = common.ReplaceRequestBody(c, jsonData)
	}
	if err != nil {
		abortKlingV3(c, http.StatusInternalServerError, "failed to convert request body")
		return false
	}
	c.Request.URL.Path = klingV3UnifiedVideoPath
	return true
}

// klingV3ApplyOptions 映射官方的 options。callback_url 没有对应实现，显式给了就要
// 报错——静默收下会让调用方一直等一个永远不会到达的回调。
func klingV3ApplyOptions(metadata map[string]any, options map[string]any) {
	if externalID, ok := klingV3String(options, "external_task_id"); ok {
		// 上游会在回调里原样带回它。本网关不支持按它查询（见 rewriteKlingV3Fetch）。
		metadata["session_context"] = externalID
	}
	watermark, ok := options["watermark_info"].(map[string]any)
	if !ok {
		return
	}
	enabled, ok := watermark["enabled"].(bool)
	if !ok {
		return
	}
	if enabled {
		metadata["logo_add"] = "Enabled"
		return
	}
	metadata["logo_add"] = "Disabled"
}

// klingV3Contents 拆开官方的素材数组，返回提示词、统一协议的素材项和主体列表。
// 文生视频没有 contents，提示词在顶层 prompt 上。
func klingV3Contents(req map[string]any) (prompt string, media []any, subjects []any) {
	if text, ok := req["prompt"].(string); ok {
		prompt = text
	}
	items, ok := req["contents"].([]any)
	if !ok {
		return prompt, nil, nil
	}

	texts := make([]string, 0, 1)
	if prompt != "" {
		texts = append(texts, prompt)
	}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		itemType, _ := item["type"].(string)
		if itemType == "prompt" {
			if text, ok := item["text"].(string); ok && text != "" {
				texts = append(texts, text)
			}
			continue
		}
		if itemType == "element" {
			if elementID, ok := item["element_id"].(string); ok && elementID != "" {
				subjects = append(subjects, map[string]any{"id": elementID})
			}
			continue
		}
		shape, known := klingV3MediaTypes[itemType]
		if !known {
			continue
		}
		mediaURL, _ := item["url"].(string)
		media = append(media, map[string]any{
			"type":          shape.mediaType,
			"role":          shape.role,
			shape.mediaType: map[string]any{"url": mediaURL},
		})
	}
	return strings.Join(texts, "\n"), media, subjects
}

// klingV3BoundsError 按官方文档校验形状与取值域，返回空串表示通过。
func klingV3BoundsError(req map[string]any, path string) string {
	if path == KlingV3TextToVideoPath {
		if _, ok := req["prompt"].(string); !ok {
			return "prompt must be a string"
		}
	} else if message := klingV3ContentsError(req); message != "" {
		return message
	}

	if message := klingV3SettingsError(req); message != "" {
		return message
	}
	return klingV3OptionsError(req)
}

func klingV3SettingsError(req map[string]any) string {
	raw, present := req["settings"]
	if !present || raw == nil {
		return ""
	}
	settings, ok := raw.(map[string]any)
	if !ok {
		return "settings must be an object"
	}
	for field, allowed := range map[string][]string{
		"resolution":   klingV3Resolutions,
		"aspect_ratio": klingV3AspectRatios,
		"audio":        klingV3AudioModes,
	} {
		value, present := settings[field]
		if !present || value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok || !slices.Contains(allowed, text) {
			return fmt.Sprintf("settings.%s must be one of %s", field, strings.Join(allowed, ", "))
		}
	}
	if value, present := settings["duration"]; present && value != nil {
		seconds, ok := klingV3Int(settings, "duration")
		if !ok || seconds < klingV3MinDuration || seconds > klingV3MaxDuration {
			return fmt.Sprintf("settings.duration must be an integer between %d and %d", klingV3MinDuration, klingV3MaxDuration)
		}
	}
	if value, present := settings["multi_shot"]; present && value != nil {
		enabled, ok := value.(bool)
		if !ok {
			return "settings.multi_shot must be a boolean"
		}
		// 官方默认 true，且 3.0 的多镜头是靠 prompt 里的「镜头 n, m, words」格式表达的。
		// 关闭开关在上游没有对应参数，收下却做不到会让调用方拿到一个它明确要求避免的
		// 多镜头结果。
		if !enabled {
			return "settings.multi_shot = false is not supported by this gateway"
		}
	}
	return ""
}

func klingV3OptionsError(req map[string]any) string {
	raw, present := req["options"]
	if !present || raw == nil {
		return ""
	}
	options, ok := raw.(map[string]any)
	if !ok {
		return "options must be an object"
	}
	if callback, present := options["callback_url"]; present && callback != nil {
		text, ok := callback.(string)
		if !ok {
			return "options.callback_url must be a string"
		}
		// 收下一个不会被调用的回调地址，调用方会一直等下去。
		if strings.TrimSpace(text) != "" {
			return "options.callback_url is not supported by this gateway, poll GET /tasks instead"
		}
	}
	if externalID, present := options["external_task_id"]; present && externalID != nil {
		if _, ok := externalID.(string); !ok {
			return "options.external_task_id must be a string"
		}
	}
	if watermark, present := options["watermark_info"]; present && watermark != nil {
		info, ok := watermark.(map[string]any)
		if !ok {
			return "options.watermark_info must be an object"
		}
		if enabled, present := info["enabled"]; present && enabled != nil {
			if _, ok := enabled.(bool); !ok {
				return "options.watermark_info.enabled must be a boolean"
			}
		}
	}
	return ""
}

// klingV3ContentsError 校验 contents 每一项的形状。
func klingV3ContentsError(req map[string]any) string {
	items, ok := req["contents"].([]any)
	if !ok || len(items) == 0 {
		return "contents must be a non-empty array"
	}
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Sprintf("contents[%d] must be an object", index)
		}
		itemType, ok := item["type"].(string)
		if !ok {
			return fmt.Sprintf("contents[%d].type must be a string", index)
		}
		switch {
		case itemType == "prompt":
			if _, ok := item["text"].(string); !ok {
				return fmt.Sprintf("contents[%d].text must be a string", index)
			}
		case itemType == "element":
			if id, ok := item["element_id"].(string); !ok || strings.TrimSpace(id) == "" {
				return fmt.Sprintf("contents[%d].element_id must be a non-empty string", index)
			}
		default:
			if _, known := klingV3MediaTypes[itemType]; !known {
				return fmt.Sprintf("contents[%d].type must be one of prompt, element, %s",
					index, strings.Join(klingV3MediaTypeNames(), ", "))
			}
			if mediaURL, ok := item["url"].(string); !ok || strings.TrimSpace(mediaURL) == "" {
				return fmt.Sprintf("contents[%d].url must be a non-empty string", index)
			}
		}
	}
	return ""
}

func klingV3MediaTypeNames() []string {
	names := make([]string, 0, len(klingV3MediaTypes))
	for name := range klingV3MediaTypes {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func klingV3String(source map[string]any, key string) (string, bool) {
	if source == nil {
		return "", false
	}
	value, ok := source[key].(string)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

// klingV3Int 解析 JSON 数字为整数。浮点小数、字符串、布尔一律返回 false。
func klingV3Int(source map[string]any, key string) (int, bool) {
	if source == nil {
		return 0, false
	}
	value, ok := source[key].(float64)
	if !ok || value != float64(int(value)) {
		return 0, false
	}
	return int(value), true
}
