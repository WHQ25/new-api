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
