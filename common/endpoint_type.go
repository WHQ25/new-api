package common

import "github.com/QuantumNous/new-api/constant"

// GetEndpointTypesByChannelType 获取渠道最优先端点类型（所有的渠道都支持 OpenAI 端点）
func GetEndpointTypesByChannelType(channelType int, modelName string) []constant.EndpointType {
	var endpointTypes []constant.EndpointType
	switch channelType {
	case constant.ChannelTypeJina:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeJinaRerank}
	//case constant.ChannelTypeMidjourney, constant.ChannelTypeMidjourneyPlus:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeMidjourney}
	//case constant.ChannelTypeSunoAPI:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeSuno}
	//case constant.ChannelTypeJimeng:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeJimeng}
	case constant.ChannelTypeAws:
		fallthrough
	case constant.ChannelTypeAnthropic:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeVertexAi:
		fallthrough
	case constant.ChannelTypeGemini:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeOpenRouter: // OpenRouter 只支持 OpenAI 端点
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
	case constant.ChannelTypeXai:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse}
	// 纯视频任务渠道：没有同步聊天适配器，走的是 /v1/video/generations 那条任务链路。
	// 漏掉它们会落到下面的 default 分支报成 openai，模型广场就把视频模型标成 Chat，
	// 调用方照着去打 /v1/chat/completions 只会拿到 404。
	// Jimeng 不在此列：它另有一个同步图像适配器。
	case constant.ChannelTypeSora, constant.ChannelTypeKling, constant.ChannelTypeVidu:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
	// 豆包视频额外支持方舟官方协议入站（middleware.ArkRequestConvert）。只有它支持：
	// 该入站层把方舟请求原样塞进 metadata，唯一能还原成上游请求体的是 doubao 适配器，
	// 其他视频渠道拿到这份 metadata 只会静默忽略掉全部方舟参数。
	case constant.ChannelTypeDoubaoVideo:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIVideo, constant.EndpointTypeArkVideo}
	case constant.ChannelTypeSub2API, constant.ChannelTypeNewAPI:
		endpointTypes = []constant.EndpointType{
			constant.EndpointTypeOpenAI,
			constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact,
			constant.EndpointTypeAnthropic,
			constant.EndpointTypeGemini,
			constant.EndpointTypeOpenAIAlphaSearch,
		}
	case constant.ChannelTypeCodex:
		endpointTypes = []constant.EndpointType{
			constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact,
			constant.EndpointTypeOpenAIAlphaSearch,
		}
	default:
		if IsOpenAIResponseOnlyModel(modelName) {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIResponse}
		} else {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
		}
	}
	if IsImageGenerationModel(modelName) {
		// add to first
		endpointTypes = append([]constant.EndpointType{constant.EndpointTypeImageGeneration}, endpointTypes...)
	}
	return endpointTypes
}
