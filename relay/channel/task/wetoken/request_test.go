package wetoken

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func infoFor(model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model},
	}
}

// 计费读统一协议的扁平 metadata，上游认的是它自己的顶层字段。两边必须从同一份值
// 得出同一个档位：判出参考视频却没下发（多收）、下发了却没判出（少收），都是钱。
func TestPayloadDimensionsMatchBillingSignals(t *testing.T) {
	cases := []struct {
		name          string
		model         string
		req           relaycommon.TaskSubmitReq
		wantSize      string
		wantDuration  int
		wantAudio     *bool
		wantVideoFile bool
	}{
		{
			name:         "resolution and duration come from the shared accessors",
			model:        "kling-v3",
			req:          relaycommon.TaskSubmitReq{Prompt: "a cat", Size: "1080p"},
			wantSize:     "1080P",
			wantDuration: defaultDurationSeconds,
		},
		{
			name:  "metadata resolution wins over top-level size",
			model: "kling-v3",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "a cat",
				Size:     "720p",
				Duration: 8,
				Metadata: map[string]interface{}{"resolution": "4k"},
			},
			wantSize:     "4K",
			wantDuration: 8,
		},
		{
			name:  "the unified audio alias reaches the upstream native field",
			model: "kling-v3",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "a cat",
				Size:     "720p",
				Metadata: map[string]interface{}{"generate_audio": true},
			},
			wantSize:     "720P",
			wantDuration: defaultDurationSeconds,
			wantAudio:    boolPtr(true),
		},
		{
			name:  "an explicit false is sent, not dropped",
			model: "kling-v3",
			req: relaycommon.TaskSubmitReq{
				Prompt:   "a cat",
				Size:     "720p",
				Metadata: map[string]interface{}{"audio": false},
			},
			wantSize:     "720P",
			wantDuration: defaultDurationSeconds,
			wantAudio:    boolPtr(false),
		},
		{
			name:  "a reference video becomes a Video file_info",
			model: "kling-v3-omni",
			req: relaycommon.TaskSubmitReq{
				Prompt: "a cat",
				Size:   "1080p",
				Metadata: map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{
							"type":      "video_url",
							"role":      "reference_video",
							"video_url": map[string]interface{}{"url": "https://example.com/a.mp4"},
						},
					},
				},
			},
			wantSize:      "1080P",
			wantDuration:  defaultDurationSeconds,
			wantVideoFile: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			normalizeBillingDimensions(tc.req.Metadata)
			payload, taskErr := convertToRequestPayload(&tc.req, infoFor(tc.model))
			require.Nil(t, taskErr)

			assert.Equal(t, tc.wantSize, payload.Size)
			assert.Equal(t, tc.wantDuration, payload.Duration)
			assert.Equal(t, tc.wantAudio, payload.AudioGeneration)

			// 计费判档读的就是 HasMediaType，下发读的是 file_infos——两者必须一致。
			assert.Equal(t, tc.wantVideoFile, hasVideoInput(payload.FileInfos))
			assert.Equal(t,
				tc.req.HasMediaType(relaycommon.TaskMediaTypeVideo),
				hasVideoInput(payload.FileInfos),
				"billing and the upstream request body disagree on video input")
		})
	}
}

// 按上游原生写法提交的 file_infos 也必须被计费读到，否则一个带参考视频的请求会按
// 纯文生视频的便宜档成交。
func TestNativeFileInfosReachBilling(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "a cat",
		Size:   "1080p",
		Metadata: map[string]interface{}{
			"file_infos": []interface{}{
				map[string]interface{}{
					"type": "Url", "category": "Video",
					"url": "https://example.com/a.mp4", "usage": "Reference",
				},
			},
		},
	}
	require.False(t, req.HasMediaType(relaycommon.TaskMediaTypeVideo))

	normalizeBillingDimensions(req.Metadata)
	assert.True(t, req.HasMediaType(relaycommon.TaskMediaTypeVideo))

	payload, taskErr := convertToRequestPayload(&req, infoFor("kling-v3-omni"))
	require.Nil(t, taskErr)
	assert.True(t, hasVideoInput(payload.FileInfos))
}

// 越界的组合必须拦在预扣费之前：放行只会让上游 400，而钱已经按某个档位扣过了。
func TestContractViolationsAreRejected(t *testing.T) {
	cases := []struct {
		name  string
		model string
		req   relaycommon.TaskSubmitReq
	}{
		{
			name:  "480p is not a Kling tier",
			model: "kling-v3",
			req:   relaycommon.TaskSubmitReq{Prompt: "a cat", Size: "480p"},
		},
		{
			name:  "a missing resolution is refused rather than defaulted",
			model: "kling-v3",
			req:   relaycommon.TaskSubmitReq{Prompt: "a cat"},
		},
		{
			name:  "a duration below the model range",
			model: "kling-v3",
			req:   relaycommon.TaskSubmitReq{Prompt: "a cat", Size: "720p", Duration: 2},
		},
		{
			name:  "a duration above the model range",
			model: "kling-v3",
			req:   relaycommon.TaskSubmitReq{Prompt: "a cat", Size: "720p", Duration: 16},
		},
		{
			name:  "kling-v3 does not accept a reference video",
			model: "kling-v3",
			req: relaycommon.TaskSubmitReq{
				Prompt: "a cat", Size: "720p",
				Metadata: map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "video_url", "video_url": "https://example.com/a.mp4"},
					},
				},
			},
		},
		{
			name:  "audio cannot be combined with a reference video",
			model: "kling-v3-omni",
			req: relaycommon.TaskSubmitReq{
				Prompt: "a cat", Size: "720p",
				Metadata: map[string]interface{}{
					"generate_audio": true,
					"content": []interface{}{
						map[string]interface{}{"type": "video_url", "video_url": "https://example.com/a.mp4"},
					},
				},
			},
		},
		{
			name:  "an unknown model is refused rather than guessed",
			model: "kling-v9",
			req:   relaycommon.TaskSubmitReq{Prompt: "a cat", Size: "720p"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			normalizeBillingDimensions(tc.req.Metadata)
			_, taskErr := convertToRequestPayload(&tc.req, infoFor(tc.model))
			require.NotNil(t, taskErr)
		})
	}
}

// 指定音色必然是有声生成，计费据此进 _audio 档。音色不搬进上游认的 ext_info，
// 就是按有声收费、按无声生成。
func TestVoiceIDMovesIntoExtInfo(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "a singer", Size: "720p",
		Metadata: map[string]interface{}{"voice_id": "869048851066937391"},
	}
	normalizeBillingDimensions(req.Metadata)
	payload, taskErr := convertToRequestPayload(&req, infoFor("kling-v3"))
	require.Nil(t, taskErr)

	require.NotNil(t, payload.AudioGeneration)
	assert.True(t, *payload.AudioGeneration)
	assert.JSONEq(t,
		`{"AdditionalParameters":"{\"voice_list\":[{\"voice_id\":\"869048851066937391\"}]}"}`,
		payload.ExtInfo)
}

// 18 位音色 ID 走 JSON 数字会被解析成 float64 而丢掉末几位，静默映射过去就是拿一个
// 不存在的音色去生成。宁可拒绝，也不能生成错的。
func TestNumericVoiceIDIsRejected(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "a singer", Size: "720p",
		Metadata: map[string]interface{}{"voice_id": float64(869048851066937391)},
	}
	normalizeBillingDimensions(req.Metadata)
	_, taskErr := convertToRequestPayload(&req, infoFor("kling-v3"))
	require.NotNil(t, taskErr)
}

// metadata 会被整体反序列化进请求体，能覆盖构造时写入的任何值。模型名和计费维度
// 必须在那之后定值，否则就是按 A 计费、按 B 生成。
func TestMetadataCannotOverrideBilledFields(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "a cat", Size: "720p", Duration: 5,
		Metadata: map[string]interface{}{
			"model":    "kling-v3-omni",
			"duration": 15,
			"size":     "4K",
		},
	}
	normalizeBillingDimensions(req.Metadata)
	payload, taskErr := convertToRequestPayload(&req, infoFor("kling-v3"))
	require.Nil(t, taskErr)

	assert.Equal(t, "kling-v3", payload.Model)
	// 顶层 duration 优先于 metadata，与 RequestedOutputSeconds 的契约一致。
	assert.Equal(t, 5, payload.Duration)
	assert.Equal(t, "720P", payload.Size)
}

// 未参与计价的上游原生字段要能原样透传，否则功能凭空少一块。
func TestPassthroughFieldsSurvive(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "a cat", Size: "720p",
		Metadata: map[string]interface{}{
			"negative_prompt": "blurry",
			"enhance_prompt":  "Enabled",
			"subject_infos":   []interface{}{map[string]interface{}{"id": "858477278396170315"}},
		},
	}
	normalizeBillingDimensions(req.Metadata)
	payload, taskErr := convertToRequestPayload(&req, infoFor("kling-v3"))
	require.Nil(t, taskErr)

	assert.Equal(t, "blurry", payload.NegativePrompt)
	assert.Equal(t, "Enabled", payload.EnhancePrompt)
	require.Len(t, payload.SubjectInfos, 1)
	assert.Equal(t, "858477278396170315", payload.SubjectInfos[0].Id)
}

func boolPtr(v bool) *bool { return &v }
