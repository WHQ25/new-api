package wetoken

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/pkg/errors"
)

// audioSignalKeys 是有声档的信号键，顺序即优先级。原生的 audio_generation 排在最前：
// 只有它会下发给上游，冲突时听上游的那个说了算。generate_audio / audio 是统一协议的
// 拼法，计费读的正是它们。
var audioSignalKeys = []string{"audio_generation", "generate_audio", "audio"}

// voiceSignalKeys 与计费的音色信号保持一致（见 requestVideoTokenVariants）。
var voiceSignalKeys = []string{"voice_id", "voice", "timbre"}

func badRequest(err error, code string) *taskdto.TaskError {
	return service.TaskErrorWrapperLocal(err, code, http.StatusBadRequest)
}

// normalizeBillingDimensions 把统一协议与上游原生的两种写法合并成同一份值。
// 必须在计价之前跑：计费读扁平 metadata 的 content / generate_audio，上游认的是
// file_infos / audio_generation，各读各的就会出现按一个档位收费、按另一个档位生成。
// Metadata 是 map，就地改写对 context 里的同一份请求生效。
func normalizeBillingDimensions(metadata map[string]interface{}) {
	if metadata == nil {
		return
	}
	projectFileInfosToContent(metadata)
	if preference, found := requestedAudio(metadata); found {
		for _, key := range audioSignalKeys {
			metadata[key] = preference
		}
	}
}

// projectFileInfosToContent 让直接按上游原生写法提交的 file_infos 也能被计费读到。
// 计费只认统一协议的 content，不投影的话，一个带参考视频的请求会按纯文生视频的
// 便宜档成交。content 在场时不动它——那是统一协议的正本，请求体也只从它构造。
func projectFileInfosToContent(metadata map[string]interface{}) {
	if _, has := metadata["content"]; has {
		return
	}
	rawList, ok := metadata["file_infos"].([]interface{})
	if !ok {
		return
	}
	content := make([]interface{}, 0, len(rawList))
	for _, raw := range rawList {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		url, _ := item["url"].(string)
		category, _ := item["category"].(string)
		usage, _ := item["usage"].(string)

		mediaType, role := relaycommon.TaskMediaTypeImage, relaycommon.TaskMediaRoleFirstFrame
		switch {
		case strings.EqualFold(category, fileInfoCategoryVideo):
			mediaType, role = relaycommon.TaskMediaTypeVideo, relaycommon.TaskMediaRoleReferenceVideo
		case strings.EqualFold(usage, fileInfoUsageReference):
			role = relaycommon.TaskMediaRoleReferenceImage
		}
		content = append(content, map[string]interface{}{
			"type": mediaType,
			"role": role,
			mediaType: map[string]interface{}{
				"url": url,
			},
		})
	}
	if len(content) > 0 {
		metadata["content"] = content
	}
}

// requestedAudio 读出客户端有没有明确要不要声音，第二个返回值区分「显式 false」
// 与「没提」。取值范围要与计费的 metadataBool 一致，否则同一个请求两边判出不同的档。
func requestedAudio(metadata map[string]interface{}) (bool, bool) {
	for _, key := range audioSignalKeys {
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
		case float64:
			return value != 0, true
		}
	}
	return false, false
}

// contractFor 返回最终会请求的那个模型的契约。渠道的模型映射可能把请求改投到另一个
// 模型，能力边界跟着变，只看客户端请求的模型名会按错的一套校验。
func contractFor(info *relaycommon.RelayInfo) (modelContract, bool) {
	if contract, known := modelContracts[info.UpstreamModelName]; known {
		return contract, true
	}
	contract, known := modelContracts[info.OriginModelName]
	return contract, known
}

// convertToRequestPayload 构造上游请求体，同时校验该模型的契约边界。
//
// 它是纯函数：计价阶段先跑一次拿计费维度并把越界请求拦在预扣费之前，下发阶段再跑
// 一次构造真正的请求体。同一个入口算两次，两边不可能得出不同的时长或档位。
func convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, *taskdto.TaskError) {
	contract, known := contractFor(info)
	if !known {
		return nil, badRequest(
			fmt.Errorf("model %s is not supported by this channel", info.UpstreamModelName),
			"invalid_model")
	}

	r := requestPayload{}
	// 未参与计价的上游原生字段（ext_info / subject_infos / negative_prompt …）
	// 由客户端通过 metadata 原样透传。
	if err := taskcommon.UnmarshalMetadata(req.Metadata, &r); err != nil {
		return nil, badRequest(err, "invalid_metadata")
	}
	// 以下字段只能由请求本身决定，必须在反序列化之后定值：metadata 会覆盖上面写入的
	// 任何值，漏了这一步就是按 A 计费、按 B 生成。
	r.Model = info.UpstreamModelName
	r.Prompt = req.Prompt

	tier, err := billing_setting.ParseVideoTokenTier(req.RequestedResolution())
	if err != nil {
		return nil, badRequest(err, "missing_resolution")
	}
	size, supported := supportedResolutions[tier]
	if !supported {
		return nil, badRequest(
			fmt.Errorf("resolution %s is not supported by this model, use one of 720p / 1080p / 4k", tier),
			"invalid_resolution")
	}
	r.Size = size

	seconds := req.RequestedOutputSeconds()
	if seconds == 0 {
		seconds = defaultDurationSeconds
	}
	if seconds < minDurationSeconds || seconds > maxDurationSeconds {
		return nil, badRequest(
			fmt.Errorf("duration must be between %d and %d seconds for this model", minDurationSeconds, maxDurationSeconds),
			"invalid_seconds")
	}
	r.Duration = seconds

	if ratio := req.RequestedAspectRatio(); ratio != "" {
		r.AspectRatio = ratio
	}

	files, lastFrame, taskErr := buildFileInfos(req, contract)
	if taskErr != nil {
		return nil, taskErr
	}
	r.FileInfos = files
	if lastFrame != "" {
		r.LastFrameURL = lastFrame
	}

	if taskErr := applyAudio(&r, req.Metadata); taskErr != nil {
		return nil, taskErr
	}
	return &r, nil
}

// buildFileInfos 把统一协议的素材投影成上游的 file_infos，并返回尾帧地址。
func buildFileInfos(req *relaycommon.TaskSubmitReq, contract modelContract) ([]fileInfo, string, *taskdto.TaskError) {
	items := req.MediaItems()
	files := make([]fileInfo, 0, len(items))
	lastFrame := ""
	sawImage := false

	for _, item := range items {
		if item.URL == "" {
			return nil, "", badRequest(
				fmt.Errorf("metadata.content has a %s item without a url", item.Type),
				"invalid_content")
		}
		switch item.Type {
		case relaycommon.TaskMediaTypeVideo:
			if !contract.supportsVideoInput {
				return nil, "", badRequest(
					errors.New("this model does not accept a reference video, use kling-v3-omni"),
					"invalid_content")
			}
			files = append(files, fileInfo{
				Type: fileInfoTypeURL, Category: fileInfoCategoryVideo,
				Url: item.URL, Usage: fileInfoUsageReference,
			})
		case relaycommon.TaskMediaTypeImage:
			sawImage = true
			switch item.Role {
			case relaycommon.TaskMediaRoleLastFrame:
				lastFrame = item.URL
			case relaycommon.TaskMediaRoleReferenceImage:
				if !contract.supportsReferenceImage {
					return nil, "", badRequest(
						errors.New("this model only accepts a first-frame image, use kling-v3-omni for reference images"),
						"invalid_content")
				}
				files = append(files, fileInfo{
					Type: fileInfoTypeURL, Category: fileInfoCategoryImage,
					Url: item.URL, Usage: fileInfoUsageReference,
				})
			default:
				files = append(files, fileInfo{
					Type: fileInfoTypeURL, Category: fileInfoCategoryImage,
					Url: item.URL, Usage: fileInfoUsageFirstFame,
				})
			}
		case relaycommon.TaskMediaTypeAudio:
			return nil, "", badRequest(
				errors.New("this model does not accept a reference audio"),
				"invalid_content")
		}
	}

	// 顶层 images 是统一协议的简写，只在 content 没给图片时生效：两处都给会让同一张图
	// 既当首帧又当参考，上游按数量上限直接拒。第一张是首帧，其余是参考图。
	if !sawImage {
		for index, url := range req.Images {
			if strings.TrimSpace(url) == "" {
				continue
			}
			usage := fileInfoUsageFirstFame
			if index > 0 {
				if !contract.supportsReferenceImage {
					return nil, "", badRequest(
						errors.New("this model accepts only one image, as the first frame"),
						"invalid_content")
				}
				usage = fileInfoUsageReference
			}
			files = append(files, fileInfo{
				Type: fileInfoTypeURL, Category: fileInfoCategoryImage,
				Url: url, Usage: usage,
			})
		}
	}

	if len(files) == 0 {
		return nil, lastFrame, nil
	}
	return files, lastFrame, nil
}

// applyAudio 定下 audio_generation，并把统一协议的音色信号搬进上游认的 ext_info。
func applyAudio(r *requestPayload, metadata map[string]interface{}) *taskdto.TaskError {
	preference, found := requestedAudio(metadata)

	voice, taskErr := requestedVoiceID(metadata)
	if taskErr != nil {
		return taskErr
	}
	if voice != "" {
		if r.ExtInfo != "" {
			return badRequest(
				errors.New("metadata.voice_id conflicts with metadata.ext_info, put voice_list inside ext_info instead"),
				"invalid_request")
		}
		if found && !preference {
			return badRequest(
				errors.New("metadata.voice_id requires audio generation, remove it or set audio_generation to true"),
				"invalid_request")
		}
		extInfo, err := buildVoiceExtInfo(voice)
		if err != nil {
			return badRequest(err, "invalid_request")
		}
		// 指定音色必然是有声生成，计费也据此进 _audio 档。不写死这个字段，上游会按
		// 默认的 false 生成无声视频，而钱已经按有声档收了。
		r.ExtInfo = extInfo
		preference, found = true, true
	}

	if !found {
		return nil
	}
	if preference && hasVideoInput(r.FileInfos) {
		// 官方硬约束：带参考视频时不能生成音频。放行只会让上游 400，而这一步之前
		// 已经按 video + audio 的组合档预扣过了。
		return badRequest(
			errors.New("audio generation is not supported together with a reference video"),
			"invalid_request")
	}
	r.AudioGeneration = &preference
	return nil
}

func hasVideoInput(files []fileInfo) bool {
	for _, file := range files {
		if file.Category == fileInfoCategoryVideo {
			return true
		}
	}
	return false
}

// requestedVoiceID 读出音色 ID。只接受字符串：可灵的音色是 18 位数字，JSON 数字会被
// 解析成 float64 而丢掉末几位精度，静默映射过去就是拿一个不存在的音色去生成。
func requestedVoiceID(metadata map[string]interface{}) (string, *taskdto.TaskError) {
	for _, key := range voiceSignalKeys {
		raw, ok := relaycommon.MetadataValue(metadata, key)
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return "", badRequest(
				fmt.Errorf("metadata.%s must be a string: a bare JSON number loses precision on an 18-digit voice id", key),
				"invalid_request")
		}
		if value = strings.TrimSpace(value); value != "" {
			return value, nil
		}
	}
	return "", nil
}

// buildVoiceExtInfo 拼出上游的 ext_info。AdditionalParameters 的值本身是一个 JSON
// 字符串，不是对象——上游就是这么定义的，少一层转义会被当成非法参数丢弃。
func buildVoiceExtInfo(voiceID string) (string, error) {
	inner, err := common.Marshal(map[string]any{
		"voice_list": []any{map[string]any{"voice_id": voiceID}},
	})
	if err != nil {
		return "", errors.Wrap(err, "marshal voice_list failed")
	}
	outer, err := common.Marshal(map[string]any{"AdditionalParameters": string(inner)})
	if err != nil {
		return "", errors.Wrap(err, "marshal ext_info failed")
	}
	return string(outer), nil
}
