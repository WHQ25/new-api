package hailuo

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 下发给海螺的时长必须与计费估算取自同一个入口。metadata 会被整体反序列化进请求体，
// 所以它能覆盖构造时写入的时长；按秒计费下这直接等于少收钱。
func TestConvertToRequestPayloadPinsBilledDuration(t *testing.T) {
	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want int
	}{
		{
			name: "top-level duration wins over metadata",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "a cat",
				Duration: 6,
				Metadata: map[string]interface{}{"duration": 10},
			},
			want: 6,
		},
		{
			name: "metadata duration is used when the top level omits it",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "a cat",
				Metadata: map[string]interface{}{"duration": 10},
			},
			want: 10,
		},
		{
			name: "duration falls back to the provider default",
			req:  relaycommon.TaskSubmitReq{Prompt: "a cat"},
			want: DefaultDuration,
		},
	}

	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MiniMax-Hailuo-02"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := adaptor.convertToRequestPayload(&tc.req, info)
			require.NoError(t, err)
			require.NotNil(t, payload.Duration)
			assert.Equal(t, tc.want, *payload.Duration)
		})
	}
}

// hailuo 原先用的是 TaskSubmitReq 自带的 UnmarshalMetadata，那个变体不删任何键，
// 所以 metadata.model 能直接覆盖计费依据的模型：按 MiniMax-Hailuo-02 收费、
// 按 MiniMax-Hailuo-2.3 生成。
func TestConvertToRequestPayloadPinsBilledModel(t *testing.T) {
	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MiniMax-Hailuo-02"},
	}

	payload, err := adaptor.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a cat",
		Metadata: map[string]interface{}{"model": "MiniMax-Hailuo-2.3"},
	}, info)
	require.NoError(t, err)
	assert.Equal(t, "MiniMax-Hailuo-02", payload.Model)

	// 其余 metadata 字段仍要照常透传给上游。
	payload, err = adaptor.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a cat",
		Metadata: map[string]interface{}{"aigc_watermark": true},
	}, info)
	require.NoError(t, err)
	assert.Equal(t, "MiniMax-Hailuo-02", payload.Model)
	require.NotNil(t, payload.AigcWatermark)
	assert.True(t, *payload.AigcWatermark)
}
