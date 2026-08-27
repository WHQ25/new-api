package doubao

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	SafetyIdentifier string        `json:"safety_identifier,omitempty"`
	Priority         *dto.IntValue `json:"priority,omitempty"`
	// OmniReferenceTaskType 与 OutputFormat 是方舟官方请求体的字段。它们只能经
	// metadata 到达这里，缺了就会被静默丢弃，官方协议入站的调用方无从察觉。
	// 用指针保留 presence：非指针 string + omitempty 会把「未提交」和「显式空串」
	// 折叠成同一种下发结果。
	OmniReferenceTaskType *string        `json:"omni_reference_task_type,omitempty"`
	OutputFormat          *string        `json:"output_format,omitempty"`
	Resolution            string         `json:"resolution,omitempty"`
	Ratio                 string         `json:"ratio,omitempty"`
	Duration              *dto.IntValue  `json:"duration,omitempty"`
	Frames                *dto.IntValue  `json:"frames,omitempty"`
	Seed                  *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed           *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark             *dto.BoolValue `json:"watermark,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Seed            int    `json:"seed"`
	Resolution      string `json:"resolution"`
	Duration        int    `json:"duration"`
	Ratio           string `json:"ratio"`
	FramesPerSecond int    `json:"framespersecond"`
	ServiceTier     string `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *taskdto.TaskError) {
	// Accept only POST /v1/video/generations as "generate" action.
	// 方舟允许 duration = -1（由模型自选时长），通用校验默认拒绝这个上游私有约定。
	if taskErr = relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate, relaycommon.AllowSelfSelectedDuration()); taskErr != nil {
		return taskErr
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "get_task_request_failed", http.StatusBadRequest)
	}
	// 别名对齐必须在计价之前跑，也必须在这里而不是 ResolveVideoBilling 里：它改的是
	// 下发内容，按次价等其它计费方式下同样要生效。Metadata 非空时是 map，就地改写
	// 对 context 里的同一份请求生效。
	alignAudioAlias(req.Metadata)
	return nil
}

// alignAudioAlias 把统一协议的 audio 别名对齐到方舟原生的 generate_audio。
// 请求体只下发 generate_audio，只写 audio: false 会变成上游按官方默认值生成有声视频、
// 计费却读到无声档。两者冲突时以原生键为准——上游听的就是它。
func alignAudioAlias(metadata map[string]interface{}) {
	preference, found := audioPreference(metadata)
	if !found {
		return
	}
	metadata["generate_audio"] = preference
	metadata["audio"] = preference
}

// audioPreference 读出客户端有没有明确要不要声音。原生的 generate_audio 优先于统一协议的
// audio 别名：只有前者会下发给上游。
func audioPreference(metadata map[string]interface{}) (bool, bool) {
	for _, key := range []string{"generate_audio", "audio"} {
		raw, ok := relaycommon.MetadataValue(metadata, key)
		if !ok {
			continue
		}
		switch value := raw.(type) {
		case bool:
			return value, true
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				continue
			}
			return parsed, true
		}
	}
	return false, false
}

// ResolveVideoBilling 让计费读到方舟实际会生成的时长与音频档，见 channel.VideoBillingResolver。
func (a *TaskAdaptor) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (float64, *taskdto.TaskError) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return 0, service.TaskErrorWrapperLocal(err, "get_task_request_failed", http.StatusBadRequest)
	}
	limit, known := videoContractFor(info)

	changed, taskErr := pinGenerateAudio(&req, limit, known, info.OriginModelName)
	if taskErr != nil {
		return 0, taskErr
	}
	if changed {
		// Metadata 可能是这里新分配的，就地改写不够，必须把请求整体写回。
		c.Set("task_request", req)
	}

	frames, taskErr := requestedFrames(req, limit, known)
	if taskErr != nil {
		return 0, taskErr
	}
	intent, taskErr := requestedDuration(req)
	if taskErr != nil {
		return 0, taskErr
	}

	// frames 的优先级高于 duration，且不一定整除帧率：289 帧是 12.0417 秒，
	// 取整成 13 秒会凭空多收 8%。官方给的换算就是 frames/24，按精确值计费。
	if frames > 0 {
		return float64(frames) / videoFramesPerSecond, nil
	}

	switch {
	case intent.selfSelect:
		// duration=-1 表示由模型在取值区间内自选时长，下单时无从得知具体秒数。
		// 按区间上界预扣，差额靠上游回报的 usage 结算退回；按通用的 5 秒预扣，
		// Seedance 2.5 最多会少收到六分之一。
		if !known || !limit.supportsSelfSelect {
			return 0, service.TaskErrorWrapperLocal(
				fmt.Errorf("duration = %d (model-selected duration) is not supported by this model", relaycommon.DurationSelfSelect),
				"invalid_seconds", http.StatusBadRequest)
		}
		return float64(limit.maxSeconds), nil

	case intent.seconds > 0:
		// 区间外的时长在这里拦下。通用校验只拦到 3600 秒，Seedance 2.0 请求
		// duration=3600 会先按 3600 秒预扣，再等上游拒绝退款。
		if known && (intent.seconds < limit.minSeconds || intent.seconds > limit.maxSeconds) {
			return 0, service.TaskErrorWrapperLocal(
				fmt.Errorf("duration must be between %d and %d seconds for this model", limit.minSeconds, limit.maxSeconds),
				"invalid_seconds", http.StatusBadRequest)
		}
		return 0, nil

	default:
		// 没给时长就只能按上游的默认值计费，而认不出模型时那个默认值无从得知。
		// 按通用的 5 秒猜，绑着 Seedance 2.5 的接入点会自选到 30 秒，少收五分之六。
		if !known {
			return 0, service.TaskErrorWrapperLocal(
				fmt.Errorf("duration is required for this model: the upstream model behind it cannot be identified, so its default duration is unknown"),
				"invalid_seconds", http.StatusBadRequest)
		}
		// 官方默认值就是 -1 的代次，客户端什么都不传就等同于自选。
		if limit.selfSelectByDefault {
			return float64(limit.maxSeconds), nil
		}
		return 0, nil
	}
}

// videoContractFor 返回判断上游契约时该用的那一档，第二个返回值表示是否认出了模型。
//
// 渠道的模型映射可能把请求改投到另一档 Seedance，时长区间、frames 支持和音频默认值
// 都跟着变，只看客户端请求的模型名会按错的一档计费。只有上游模型名是 endpoint ID
// （方舟允许拿它当模型名，里面不含型号信息）时才退回客户端请求的模型名——
// 上游是另一个认不出的名字时不套任何契约，按一个猜的上界预扣比少收更糟。
func videoContractFor(info *relaycommon.RelayInfo) (videoDurationLimit, bool) {
	upstream := info.UpstreamModelName
	if limit, known := videoDurationLimitFor(upstream); known {
		return limit, true
	}
	if upstream == "" || upstream == info.OriginModelName || strings.HasPrefix(strings.ToLower(upstream), arkEndpointIDPrefix) {
		return videoDurationLimitFor(info.OriginModelName)
	}
	return videoDurationLimit{}, false
}

// pinGenerateAudio 把上游的隐式音频默认值写成显式值，返回请求是否被改动。
//
// 方舟的 generate_audio 官方默认值就是 true：客户端什么都不传，上游也会生成有声视频。
// 而计费只在请求带了这个信号时才进 _audio 档——Seedance 1.5 pro 有声 16 元、无声 8 元，
// 不写死就是「不传参数」这一最常见的情况下少收一半。
//
// 认不出模型时没有一个普适的默认值可写：接入点背后可能是默认有声的 1.5，也可能是
// 根本不认识 generate_audio 的 1.0——凭空注入这个字段会让后者直接被上游拒绝。
// 此时看价目表：没配 _audio 档就不改写（那个维度本来就不参与计价，查表时会被变体
// 交集去掉），配了就必须让调用方自己说清楚，否则上游语义和计费档位保不住其中一个。
func pinGenerateAudio(req *relaycommon.TaskSubmitReq, limit videoDurationLimit, known bool, model string) (bool, *taskdto.TaskError) {
	if _, found := audioPreference(req.Metadata); found {
		return false, nil
	}
	if !known {
		if !lo.Contains(billing_setting.VideoTokenPricedVariants(model), billing_setting.VideoTokenVariantAudio) {
			return false, nil
		}
		return false, service.TaskErrorWrapperLocal(
			fmt.Errorf("generate_audio is required for this model: the upstream model behind it cannot be identified, so its audio default is unknown"),
			"invalid_request", http.StatusBadRequest)
	}
	if !limit.generatesAudioByDefault {
		return false, nil
	}
	if req.Metadata == nil {
		req.Metadata = map[string]interface{}{}
	}
	req.Metadata["generate_audio"] = true
	req.Metadata["audio"] = true
	return true, nil
}

// requestedFrames 读出并校验 frames。官方规定 frames ∈ [29, 289] 且 frames = 25 + 4n，
// 只有 Seedance 1.0 pro / 1.0 pro fast 支持。
//
// frames 是计费乘数，越界值不能原样丢给上游去 400：预扣会先按那个数字扣走额度。
// 认不出模型时也拒绝——不知道这个上游认不认 frames，按它预扣等于凭空定一个乘数。
func requestedFrames(req relaycommon.TaskSubmitReq, limit videoDurationLimit, known bool) (int, *taskdto.TaskError) {
	raw, ok := relaycommon.MetadataValue(req.Metadata, "frames")
	if !ok || raw == nil {
		return 0, nil
	}
	if !known || !limit.supportsFrames {
		return 0, service.TaskErrorWrapperLocal(
			fmt.Errorf("frames is not supported by this model, use duration instead"),
			"invalid_frames", http.StatusBadRequest)
	}
	// 必须严格取整：BoundedIntFromAny 会把 29.9 截成合法的 29 帧收下，
	// 预扣之后请求体才因为类型不符失败。
	frames, ok := relaycommon.StrictIntFromAny(raw)
	if !ok || frames < videoFramesMin || frames > videoFramesMax || (frames-videoFramesBase)%videoFramesStep != 0 {
		return 0, service.TaskErrorWrapperLocal(
			fmt.Errorf("frames must be an integer between %d and %d satisfying frames = %d + %d*n", videoFramesMin, videoFramesMax, videoFramesBase, videoFramesStep),
			"invalid_frames", http.StatusBadRequest)
	}
	return frames, nil
}

// durationIntent 是这次请求对时长的表达：显式秒数、由模型自选、或者什么都没给。
type durationIntent struct {
	seconds    int
	selfSelect bool
}

// requestedDuration 解析最终会生效的时长意图。取值顺序与 RequestedOutputSeconds 一致
// （顶层压 metadata），但取整是严格的，并且区分「没给」与「给了个非法值」：
// 通用校验只看顶层字段，metadata 里的时长同样会被反序列化进上游请求体，
// 按「解析后为 0」当没给，就成了「按默认值收费、让上游拿一个非法值去生成」。
func requestedDuration(req relaycommon.TaskSubmitReq) (durationIntent, *taskdto.TaskError) {
	if req.Duration != 0 || req.Seconds != "" {
		// 顶层字段已由通用校验兜住范围与哨兵，这里只需读出来。
		if req.RequestsSelfSelectedDuration() {
			return durationIntent{selfSelect: true}, nil
		}
		return durationIntent{seconds: req.RequestedOutputSeconds()}, nil
	}
	for _, key := range []string{"duration", "seconds"} {
		raw, ok := relaycommon.MetadataValue(req.Metadata, key)
		if !ok || raw == nil {
			continue
		}
		seconds, ok := relaycommon.StrictIntFromAny(raw)
		switch {
		case ok && seconds == relaycommon.DurationSelfSelect:
			return durationIntent{selfSelect: true}, nil
		case ok && seconds > 0 && seconds <= relaycommon.MaxTaskDurationSeconds:
			return durationIntent{seconds: seconds}, nil
		default:
			return durationIntent{}, service.TaskErrorWrapperLocal(
				fmt.Errorf("%s must be an integer between 1 and %d, or %d for a model-selected duration", key, relaycommon.MaxTaskDurationSeconds, relaycommon.DurationSelfSelect),
				"invalid_seconds", http.StatusBadRequest)
		}
	}
	return durationIntent{}, nil
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/api/v3/contents/generations/tasks", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling 根据请求 metadata 中的输出分辨率与是否包含视频输入，返回相对基准价的计费 OtherRatio。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	hasVideo := hasVideoInMetadata(req.Metadata)
	resolution := req.RequestedResolution()
	ratio, ok := GetVideoInputRatio(info.OriginModelName, resolution, hasVideo)
	if !ok || ratio == 1.0 {
		return nil
	}
	return map[string]float64{"video_input": ratio}
}

// hasVideoInMetadata 直接检查 metadata 的 content 数组是否包含 video_url 条目，
// 避免构建完整的上游 requestPayload。
func hasVideoInMetadata(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	contentRaw, ok := metadata["content"]
	if !ok {
		return false
	}
	contentSlice, ok := contentRaw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req, common.GetContextKeyBool(c, constant.ContextKeyNativeTaskContent))
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if dResp.ID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return dResp.ID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, nativeContent bool) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	// Add images if present
	if req.HasImage() {
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
			})
		}
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	// 时长与计费估算同源：顶层 duration/seconds 优先于 metadata，未显式请求时不下发，
	// 交给上游用它自己的默认值。
	switch {
	case r.Frames != nil:
		// frames 在场时不下发 duration：官方规定 frames 优先，同时给两个字段只会让
		// 「下发的」和「计费的」各自解释一遍。必须显式清空——metadata 会整体反序列化
		// 进请求体，metadata 里的 duration 已经落在 r.Duration 上了。
		r.Duration = nil
	case req.RequestsSelfSelectedDuration():
		// -1 必须显式下发。计费已按该模型时长区间的上界预扣，这里漏发就变成
		// 「按上界收费、按上游默认时长生成」。
		r.Duration = lo.ToPtr(dto.IntValue(relaycommon.DurationSelfSelect))
	default:
		if sec := req.RequestedOutputSeconds(); sec > 0 {
			r.Duration = lo.ToPtr(dto.IntValue(sec))
		}
	}
	// 分辨率与宽高比同理：统一协议入站的调用方只会给顶层 size，方舟只认 resolution/ratio。
	// 不在这里换算，metadata 里没有 resolution 的请求就会按 size 计费、却让方舟用它自己的
	// 默认分辨率生成。取值走与计费估算相同的入口，两边不可能算出不同的档位。
	if resolution := req.RequestedResolution(); resolution != "" {
		r.Resolution = resolution
	}
	if ratio := req.RequestedAspectRatio(); ratio != "" {
		r.Ratio = ratio
	}

	// 方舟官方协议入站时 content 已是上游原生数组，官方契约保证按数组顺序处理并保留
	// 文本、素材及其 role，所以原样下发。统一协议入站时上游只认顶层 prompt，仍按既有
	// 契约剔除 metadata 里的 text 项、把 prompt 追加到素材之后。
	if !nativeContent || len(r.Content) == 0 {
		r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
		r.Content = append(r.Content, ContentItem{
			Type: "text",
			Text: req.Prompt,
		})
	}

	return &r, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
	case "failed", "cancelled", "expired":
		// cancelled/expired 同样是终态：漏判会让任务一直轮询到超时清理才退款。
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Error.Message
		if taskResult.Reason == "" {
			taskResult.Reason = fmt.Sprintf("task %s", resTask.Status)
		}
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", dResp.Content.VideoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if dResp.Status == "failed" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: dResp.Error.Message,
			Code:    dResp.Error.Code,
		}
	}

	return common.Marshal(openAIVideo)
}
