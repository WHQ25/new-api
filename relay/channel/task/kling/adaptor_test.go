package kling

import (
	"strconv"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 下发给可灵的时长必须与计费估算取自同一个入口。metadata 会被整体反序列化进请求体，
// 所以它能覆盖构造时写入的时长；按秒计费下这直接等于少收钱。
func TestConvertToRequestPayloadPinsBilledDuration(t *testing.T) {
	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want string
	}{
		{
			name: "top-level duration wins over metadata",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "a cat",
				Duration: 5,
				Metadata: map[string]interface{}{"duration": "15"},
			},
			want: "5",
		},
		{
			name: "metadata duration is used when the top level omits it",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "a cat",
				Metadata: map[string]interface{}{"duration": "10"},
			},
			want: "10",
		},
		{
			name: "duration falls back to the 5s default",
			req:  relaycommon.TaskSubmitReq{Prompt: "a cat"},
			want: "5",
		},
		{
			name: "an out-of-range duration is capped, not passed through",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "a cat",
				Metadata: map[string]interface{}{"duration": strconv.Itoa(relaycommon.MaxTaskDurationSeconds + 100)},
			},
			want: "3600",
		},
	}

	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := adaptor.convertToRequestPayload(&tc.req, info)
			require.NoError(t, err)
			assert.Equal(t, tc.want, payload.Duration)
		})
	}
}

// 下发给上游的模型必须与计费依据一致。可灵上游认的是 model_name，而 UnmarshalMetadata
// 只删 metadata 里的 model——删掉的恰好是那个不起作用的兼容字段，放行 model_name 就等于
// 按 kling-v1 收费、按 kling-v2-master 生成。
func TestConvertToRequestPayloadPinsBilledModel(t *testing.T) {
	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v1"},
	}

	// model_name 是真正决定上游用哪个模型的字段，只能拒绝：静默改回去等于丢掉用户
	// 明确写了的参数，放行则是绕过计费。
	_, err := adaptor.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a cat",
		Metadata: map[string]interface{}{"model_name": "kling-v2-master"},
	}, info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "can't change model with metadata")

	// model 在反序列化前就被删掉了，够不到请求体，因此中和而非报错。
	payload, err := adaptor.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a cat",
		Metadata: map[string]interface{}{"model": "kling-v2-master"},
	}, info)
	require.NoError(t, err)
	assert.Equal(t, "kling-v1", payload.ModelName)
	assert.Equal(t, "kling-v1", payload.Model)

	// 与请求一致的模型名不该被误伤：透传上游私有参数是 metadata 的正当用途。
	payload, err = adaptor.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "a cat",
		Metadata: map[string]interface{}{"model_name": "kling-v1", "cfg_scale": 0.8},
	}, info)
	require.NoError(t, err)
	assert.Equal(t, "kling-v1", payload.ModelName)
	assert.Equal(t, 0.8, payload.CfgScale)
}
