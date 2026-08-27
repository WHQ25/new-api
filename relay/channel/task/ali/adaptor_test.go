package ali

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func testRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
}

func TestConvertToAliRequestWan27I2VBuildsMediaFromImage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:    "wan2.7-i2v",
		Prompt:   "animate the first frame",
		Image:    "https://example.com/first.png",
		Size:     "720p",
		Duration: 10,
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "wan2.7-i2v", aliReq.Model)
	require.Equal(t, "720P", aliReq.Parameters.Resolution)
	require.Equal(t, 10, aliReq.Parameters.Duration)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
	}, aliReq.Input.Media)
	require.Empty(t, aliReq.Input.ImgURL)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestConvertToAliRequestWan27I2VBuildsFirstAndLastFrameFromImages(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "interpolate between frames",
		Images: []string{
			"https://example.com/first.png",
			"https://example.com/last.png",
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VPrefersImageBeforeImagesAndInputReference(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "wan2.7-i2v",
		Prompt:         "use the direct image",
		Image:          " https://example.com/direct.png ",
		Images:         []string{"https://example.com/images-first.png", " https://example.com/images-last.png "},
		InputReference: "https://example.com/input-reference.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/direct.png"},
		{Type: "last_frame", URL: "https://example.com/images-last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VFallsBackToFirstNonEmptyImage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "skip blank images",
		Image:  " ",
		Images: []string{
			" ",
			" https://example.com/first.png ",
			" https://example.com/last.png ",
		},
		InputReference: "https://example.com/input-reference.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VKeepsExplicitMetadataMedia(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "wan2.7-i2v",
		Prompt:         "continue the clip",
		Image:          "https://example.com/direct.png",
		Images:         []string{"https://example.com/images-first.png", "https://example.com/images-last.png"},
		InputReference: "https://example.com/input-reference.png",
		Metadata: map[string]interface{}{
			"input": map[string]interface{}{
				"media": []interface{}{
					map[string]interface{}{
						"type": "first_clip",
						"url":  "https://example.com/input.mp4",
					},
				},
			},
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_clip", URL: "https://example.com/input.mp4"},
	}, aliReq.Input.Media)
	require.Empty(t, aliReq.Input.ImgURL)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestConvertToAliRequestWan27I2VRequiresMedia(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "animate without a frame",
	}

	_, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "requires image"))
}

func TestConvertToAliRequestWan25I2VKeepsLegacyImgURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.5-i2v-preview",
		Prompt: "animate the first frame",
		Image:  "https://example.com/first.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "https://example.com/first.png", aliReq.Input.ImgURL)
	require.Empty(t, aliReq.Input.Media)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"img_url"`)
	require.NotContains(t, string(body), `"media"`)
}

// 万相的原生请求体把生成参数放在 parameters 这层，metadata 会整体反序列化进请求体并
// 覆盖上面写入的时长。计费只认顶层时，parameters.duration=10 会按 5 秒收费、按 10 秒生成。
func TestConvertToAliRequestPinsBilledDuration(t *testing.T) {
	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want int
	}{
		{
			name: "nested parameters duration reaches both sides",
			req: relaycommon.TaskSubmitReq{
				Model:    "wan2.5-i2v",
				Prompt:   "a cat",
				Image:    "https://example.com/a.png",
				Size:     "720p",
				Metadata: map[string]interface{}{"parameters": map[string]interface{}{"duration": 10}},
			},
			want: 10,
		},
		{
			// 顶层 duration 与 parameters.duration 矛盾时，取值口以顶层为准；
			// 关键是下发的和计费的必须是同一个数，而不是各取一个。
			name: "an explicit top-level duration wins on both sides",
			req: relaycommon.TaskSubmitReq{
				Model:    "wan2.5-i2v",
				Prompt:   "a cat",
				Image:    "https://example.com/a.png",
				Size:     "720p",
				Duration: 5,
				Metadata: map[string]interface{}{"parameters": map[string]interface{}{"duration": 10}},
			},
			want: 5,
		},
		{
			// 扁平 metadata.duration 以前只被计费读到、从不下发，方向是多收。
			name: "a flat metadata duration is sent upstream too",
			req: relaycommon.TaskSubmitReq{
				Model:    "wan2.5-i2v",
				Prompt:   "a cat",
				Image:    "https://example.com/a.png",
				Size:     "720p",
				Metadata: map[string]interface{}{"duration": 10},
			},
			want: 10,
		},
		{
			// 未指定时双方都落到 5 秒：适配器的默认值与计费估算的默认值必须一致。
			name: "duration falls back to the 5s default",
			req: relaycommon.TaskSubmitReq{
				Model:  "wan2.5-i2v",
				Prompt: "a cat",
				Image:  "https://example.com/a.png",
				Size:   "720p",
			},
			want: 5,
		},
	}

	adaptor := &TaskAdaptor{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), tc.req)
			require.NoError(t, err)
			require.Equal(t, tc.want, aliReq.Parameters.Duration)
			if requested := tc.req.RequestedOutputSeconds(); requested > 0 {
				require.Equal(t, tc.want, requested)
			}
		})
	}
}

// 万相的原生请求体把生成参数放在 parameters 这层，统一协议的调用方却写在扁平 metadata 里。
// 两种写法都必须既被计费读到、又被下发给上游，否则一边少收、一边多收。
func TestProjectAliBillingDimensions(t *testing.T) {
	cases := []struct {
		name           string
		metadata       map[string]interface{}
		wantSeconds    int
		wantResolution string
		wantNested     map[string]interface{}
	}{
		{
			// 以前 parameters.duration 只下发、不计费：少收。
			name:           "native nested parameters become billable",
			metadata:       map[string]interface{}{"parameters": map[string]interface{}{"duration": 10, "resolution": "1080P"}},
			wantSeconds:    10,
			wantResolution: "1080p",
			wantNested:     map[string]interface{}{"duration": 10, "resolution": "1080P"},
		},
		{
			// 以前扁平 metadata.resolution 只计费、不下发：多收。
			name:           "flat metadata reaches the upstream parameters",
			metadata:       map[string]interface{}{"duration": 10, "resolution": "1080p", "audio": true},
			wantSeconds:    10,
			wantResolution: "1080p",
			wantNested:     map[string]interface{}{"duration": 10, "resolution": "1080p", "audio": true},
		},
		{
			// 反序列化让嵌套值最终生效，所以冲突时按嵌套值计费。
			name: "the nested value wins a conflict, because the upstream body does",
			metadata: map[string]interface{}{
				"duration":   5,
				"resolution": "480p",
				"parameters": map[string]interface{}{"duration": 10, "resolution": "1080P"},
			},
			wantSeconds:    10,
			wantResolution: "1080p",
			wantNested:     map[string]interface{}{"duration": 10, "resolution": "1080P"},
		},
		{
			// size 是 T2V 的像素串、resolution 是 I2V 的档位，不能互相抄：
			// 只投影到扁平一侧供计费归档，nested 保持原样。
			name:           "a pixel size is billed as a tier without touching the upstream fields",
			metadata:       map[string]interface{}{"parameters": map[string]interface{}{"size": "1920*1080"}},
			wantSeconds:    0,
			wantResolution: "1080p",
			wantNested:     map[string]interface{}{"size": "1920*1080"},
		},
		{
			// 计费把 generate_audio 与 audio 视作同一个有声档信号。
			name:           "the generate_audio alias reaches the upstream audio parameter",
			metadata:       map[string]interface{}{"generate_audio": true},
			wantSeconds:    0,
			wantResolution: "",
			wantNested:     map[string]interface{}{"audio": true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectAliBillingDimensions(tc.metadata)
			req := relaycommon.TaskSubmitReq{Metadata: tc.metadata}
			require.Equal(t, tc.wantSeconds, req.RequestedOutputSeconds())
			require.Equal(t, tc.wantResolution, req.RequestedResolution())
			require.Equal(t, tc.wantNested, tc.metadata["parameters"])
		})
	}
}

// 投影之后，下发给万相的参数必须与计费读到的完全一致。
func TestConvertToAliRequestAgreesWithBillingAfterProjection(t *testing.T) {
	metadata := map[string]interface{}{
		"generate_audio": true,
		"parameters":     map[string]interface{}{"duration": 10, "resolution": "1080P"},
	}
	projectAliBillingDimensions(metadata)

	req := relaycommon.TaskSubmitReq{
		Model:    "wan2.5-i2v",
		Prompt:   "a cat",
		Image:    "https://example.com/a.png",
		Metadata: metadata,
	}
	aliReq, err := (&TaskAdaptor{}).convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, req.RequestedOutputSeconds(), aliReq.Parameters.Duration)
	require.Equal(t, "1080P", aliReq.Parameters.Resolution)
	require.Equal(t, "1080p", req.RequestedResolution())
	require.NotNil(t, aliReq.Parameters.Audio)
	require.True(t, *aliReq.Parameters.Audio)
}

// T2V 的像素串只该以 size 下发。为了让计费归档而把它抄成 resolution，会让上游同时
// 收到两个竞争的分辨率字段。
func TestConvertToAliRequestKeepsPixelSizeOutOfResolution(t *testing.T) {
	metadata := map[string]interface{}{"parameters": map[string]interface{}{"size": "1920*1080"}}
	projectAliBillingDimensions(metadata)

	req := relaycommon.TaskSubmitReq{
		Model:    "wan2.5-t2v-preview",
		Prompt:   "a cat",
		Metadata: metadata,
	}
	aliReq, err := (&TaskAdaptor{}).convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "1920*1080", aliReq.Parameters.Size)
	require.Empty(t, aliReq.Parameters.Resolution)
	require.Equal(t, "1080p", req.RequestedResolution())
}

// 计费对有声档取的是 generate_audio 与 audio 的或。两个拼法取值矛盾时，
// 留一个不动就会按有声档收费、上游却不生成音频。
func TestProjectAliBillingDimensionsAlignsAudioAliases(t *testing.T) {
	metadata := map[string]interface{}{"generate_audio": true, "audio": false}
	projectAliBillingDimensions(metadata)

	require.Equal(t, metadata["audio"], metadata["generate_audio"])
	require.Equal(t, metadata["audio"], metadata["parameters"].(map[string]interface{})["audio"])
}
