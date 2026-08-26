package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
)

// 端点类型决定模型广场把一个模型归到哪一类。把纯视频渠道报成 openai，调用方会照着
// 去打 /v1/chat/completions 并拿到 404 —— 这正是 Seedance 接入文档不得不专门写一段
// 澄清的原因。
func TestGetEndpointTypesByChannelType(t *testing.T) {
	cases := []struct {
		name        string
		channelType int
		modelName   string
		want        []constant.EndpointType
	}{
		{
			// 豆包视频同时接受统一视频端点和方舟官方协议入站，两者都要报出来，
			// 否则模型广场不会告诉客户可以直接把火山官方 SDK 指过来。
			name:        "doubao video reports both the unified and the ark surface",
			channelType: constant.ChannelTypeDoubaoVideo,
			modelName:   "doubao-seedance-2-5-260628",
			want: []constant.EndpointType{
				constant.EndpointTypeOpenAIVideo,
				constant.EndpointTypeArkVideo,
			},
		},
		{
			// 方舟入站层只有 doubao 适配器能还原 metadata，别的视频渠道不能报 ark-video。
			name:        "kling does not advertise the ark surface",
			channelType: constant.ChannelTypeKling,
			modelName:   "kling-v1",
			want:        []constant.EndpointType{constant.EndpointTypeOpenAIVideo},
		},
		{
			name:        "vidu is video only",
			channelType: constant.ChannelTypeVidu,
			modelName:   "viduq1",
			want:        []constant.EndpointType{constant.EndpointTypeOpenAIVideo},
		},
		{
			name:        "sora is video only",
			channelType: constant.ChannelTypeSora,
			modelName:   "sora-2",
			want:        []constant.EndpointType{constant.EndpointTypeOpenAIVideo},
		},
		{
			// VolcEngine 是通用渠道，同一个渠道也跑聊天模型，不能按视频归类。
			name:        "volcengine stays a chat channel",
			channelType: constant.ChannelTypeVolcEngine,
			modelName:   "doubao-pro-32k",
			want:        []constant.EndpointType{constant.EndpointTypeOpenAI},
		},
		{
			// Jimeng 另有一个同步图像适配器，同样不是纯视频渠道。
			name:        "jimeng stays a chat channel",
			channelType: constant.ChannelTypeJimeng,
			modelName:   "jimeng_vgfm_t2v_l20",
			want:        []constant.EndpointType{constant.EndpointTypeOpenAI},
		},
		{
			name:        "anthropic keeps both surfaces",
			channelType: constant.ChannelTypeAnthropic,
			modelName:   "claude-sonnet-4",
			want:        []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, GetEndpointTypesByChannelType(tc.channelType, tc.modelName))
		})
	}
}

// 模型广场详情页按 `Boolean(e.path)` 过滤调用示例，端点类型缺了默认路径就一个示例
// 都不显示。任何会被 GetEndpointTypesByChannelType 返回的类型都必须有默认路径。
func TestEveryReportedEndpointTypeHasADefaultPath(t *testing.T) {
	channelTypes := []int{
		constant.ChannelTypeOpenAI,
		constant.ChannelTypeAnthropic,
		constant.ChannelTypeAws,
		constant.ChannelTypeGemini,
		constant.ChannelTypeVertexAi,
		constant.ChannelTypeJina,
		constant.ChannelTypeXai,
		constant.ChannelTypeOpenRouter,
		constant.ChannelTypeSora,
		constant.ChannelTypeKling,
		constant.ChannelTypeVidu,
		constant.ChannelTypeDoubaoVideo,
		constant.ChannelTypeVolcEngine,
		constant.ChannelTypeJimeng,
		constant.ChannelTypeNewAPI,
		constant.ChannelTypeSub2API,
		constant.ChannelTypeCodex,
	}
	for _, channelType := range channelTypes {
		for _, endpointType := range GetEndpointTypesByChannelType(channelType, "some-model") {
			info, ok := GetDefaultEndpointInfo(endpointType)
			assert.True(t, ok, "channel type %d reports %q with no default endpoint info", channelType, endpointType)
			assert.NotEmpty(t, info.Path, "endpoint type %q has an empty default path", endpointType)
		}
	}
}
