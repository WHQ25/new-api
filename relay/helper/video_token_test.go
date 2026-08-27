package helper

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateSeedanceTokensOfficialExamples(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, 48037.5, EstimateSeedanceTokens("480p", "16:9", 5, 0, false), 0.001)
	assert.InDelta(t, 108000, EstimateSeedanceTokens("720p", "16:9", 5, 0, false), 0.001)
	assert.InDelta(t, 243000, EstimateSeedanceTokens("1080p", "16:9", 5, 0, false), 0.001)
	assert.InDelta(t, 192150, EstimateSeedanceTokens("480p", "16:9", 10, 10, true), 0.001)
	assert.InDelta(t, 432000, EstimateSeedanceTokens("720p", "16:9", 10, 10, true), 0.001)
	assert.InDelta(t, 972000, EstimateSeedanceTokens("1080p", "16:9", 10, 10, true), 0.001)
}

func TestEstimateSeedanceTokensDefaultsAndBounds(t *testing.T) {
	t.Parallel()

	assert.Equal(t, EstimateSeedanceTokens("720p", "16:9", 5, 0, false), EstimateSeedanceTokens("", "", 0, 0, false))
	assert.Equal(t, EstimateSeedanceTokens("720p", "9:16", 5, 0, false), EstimateSeedanceTokens("720p", "16:9", 5, 0, false))
	assert.Greater(t, EstimateSeedanceTokens("4k", "16:9", 5, 0, false), EstimateSeedanceTokens("1080p", "16:9", 5, 0, false))
	assert.Equal(t,
		EstimateSeedanceTokens("720p", "16:9", relaycommon.MaxTaskDurationSeconds, 0, false),
		EstimateSeedanceTokens("720p", "16:9", relaycommon.MaxTaskDurationSeconds+10, 0, false),
	)
}

func TestEstimateVideoTokenBillingReadsRequest(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.video_token_price": `{"seedance-test":{"720p":7,"1080p":7.7,"1080p_video":4.6}}`,
	}))

	est, err := estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Metadata: map[string]interface{}{"resolution": "1080p"},
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, "1080p", est.Tariff.Key)
	assert.Equal(t, 7.7, est.Tariff.Price)
	assert.InDelta(t, 243000, est.Tokens, 0.001)
	assert.Empty(t, est.Variants)

	est, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 10,
		Metadata: map[string]interface{}{
			"resolution":     "1080p",
			"input_duration": 10,
			"content": []interface{}{
				map[string]interface{}{"type": "video_url", "video_url": map[string]interface{}{"url": "https://example.com/a.mp4"}},
			},
		},
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, "1080p_video", est.Tariff.Key)
	assert.Equal(t, 4.6, est.Tariff.Price)
	assert.InDelta(t, 972000, est.Tokens, 0.001)

	_, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Metadata: map[string]interface{}{"resolution": "4k"},
	}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "4k")

	_, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Metadata: map[string]interface{}{"resolution": "1440p"},
	}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported video resolution")

	_, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Prompt:   "A small orange cat walks slowly --duration 5 --ratio 16:9 --resolution 480p",
	}, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, billing_setting.ErrVideoTokenResolutionRequired)

	_, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Metadata: map[string]interface{}{},
	}, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, billing_setting.ErrVideoTokenResolutionRequired)
}

// 统一协议入站（/v1/video/generations）的调用方按 OpenAI Video 约定只给顶层 size。
// 在它被映射到方舟档位之前，这类请求会直接被 missing_resolution 拒掉。
func TestEstimateVideoTokenBillingAcceptsUnifiedSize(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.video_token_price": `{"seedance-test":{"480p":6.7,"720p":7,"1080p":7.7}}`,
	}))

	cases := []struct {
		name       string
		req        relaycommon.TaskSubmitReq
		wantTier   string
		wantTokens float64
	}{
		{
			name:       "landscape 720p",
			req:        relaycommon.TaskSubmitReq{Duration: 5, Size: "1280x720"},
			wantTier:   "720p",
			wantTokens: 108000,
		},
		{
			name:       "portrait keeps tier and flips pixels",
			req:        relaycommon.TaskSubmitReq{Duration: 5, Size: "720x1280"},
			wantTier:   "720p",
			wantTokens: 108000,
		},
		{
			// 方舟 480p 的 1:1 输出是 640×640，不是 480×480：它按恒定像素预算分配，
			// 不是把短边钉在档位上。按 480×480 估算每个方形视频都只收 56%。
			name:       "square 480p uses the official 640x640, not 480x480",
			req:        relaycommon.TaskSubmitReq{Duration: 5, Size: "480x480"},
			wantTier:   "480p",
			wantTokens: 48000,
		},
		{
			name:       "metadata resolution still wins over size",
			req:        relaycommon.TaskSubmitReq{Duration: 5, Size: "1920x1080", Metadata: map[string]interface{}{"resolution": "480p"}},
			wantTier:   "480p",
			wantTokens: 48037.5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			est, err := estimateVideoTokenBilling("seedance-test", tc.req, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTier, est.Tariff.Key)
			assert.InDelta(t, tc.wantTokens, est.Tokens, 0.001)
		})
	}
}

// 按秒档位：计量数量是请求时长，不是像素公式算出来的 token 数。
func TestEstimateVideoTokenBillingPerSecond(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.video_token_price": `{"kling-v3":{
			"sec:720p":0.6,"sec:720p_audio":0.9,
			"sec:1080p":0.8,"sec:1080p_audio":1.2}}`,
	}))

	cases := []struct {
		name      string
		req       relaycommon.TaskSubmitReq
		wantKey   string
		wantPrice float64
		wantUnits float64
	}{
		{
			name:      "silent 1080p 10s",
			req:       relaycommon.TaskSubmitReq{Duration: 10, Metadata: map[string]interface{}{"resolution": "1080p"}},
			wantKey:   "sec:1080p",
			wantPrice: 0.8,
			wantUnits: 10,
		},
		{
			name:      "audio surcharge",
			req:       relaycommon.TaskSubmitReq{Duration: 5, Metadata: map[string]interface{}{"resolution": "720p", "generate_audio": true}},
			wantKey:   "sec:720p_audio",
			wantPrice: 0.9,
			wantUnits: 5,
		},
		{
			// 指定音色隐含有声：请求体可能只带 voice_id 而没有显式的 generate_audio，
			// 漏掉这个信号会把有声生成按无声档收钱。
			name:      "voice implies audio",
			req:       relaycommon.TaskSubmitReq{Duration: 5, Metadata: map[string]interface{}{"resolution": "720p", "voice_id": "zh_female_01"}},
			wantKey:   "sec:720p_audio",
			wantPrice: 0.9,
			wantUnits: 5,
		},
		{
			name:      "duration falls back to the 5s default",
			req:       relaycommon.TaskSubmitReq{Metadata: map[string]interface{}{"resolution": "1080p"}},
			wantKey:   "sec:1080p",
			wantPrice: 0.8,
			wantUnits: 5,
		},
		{
			name:      "duration is capped before it becomes a multiplier",
			req:       relaycommon.TaskSubmitReq{Duration: relaycommon.MaxTaskDurationSeconds + 100, Metadata: map[string]interface{}{"resolution": "1080p"}},
			wantKey:   "sec:1080p",
			wantPrice: 0.8,
			wantUnits: relaycommon.MaxTaskDurationSeconds,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			est, err := estimateVideoTokenBilling("kling-v3", tc.req, 0)
			require.NoError(t, err)
			assert.Equal(t, billing_setting.VideoTokenUnitSecond, est.Tariff.Unit)
			assert.Equal(t, tc.wantKey, est.Tariff.Key)
			assert.Equal(t, tc.wantPrice, est.Tariff.Price)
			assert.InDelta(t, tc.wantUnits, est.BillableUnits(), 0.001)
		})
	}
}

// 视频输入档只能从 metadata.content 这层嵌套里读出来，而适配器是大小写不敏感地
// 反序列化它的。归一化只做顶层时，{"content":[{"TYPE":"video_url"}]} 会按纯文生视频
// 收费、按视频生视频生成：Seedance 表里带视频输入还要加算输入秒数，方向是少收。
func TestEstimateVideoTokenBillingReadsNestedVideoInput(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.video_token_price": `{"seedance-nested":{"1080p":7.7,"1080p_video":4.6}}`,
	}))

	cases := []struct {
		name    string
		content []interface{}
		wantKey string
	}{
		{
			name:    "canonical spelling",
			content: []interface{}{map[string]interface{}{"type": "video_url", "video_url": map[string]interface{}{"url": "https://example.com/a.mp4"}}},
			wantKey: "1080p_video",
		},
		{
			name:    "upper case spelling reaches the same tier",
			content: []interface{}{map[string]interface{}{"TYPE": "video_url", "VIDEO_URL": map[string]interface{}{"URL": "https://example.com/a.mp4"}}},
			wantKey: "1080p_video",
		},
		{
			name:    "text only stays on the base tier",
			content: []interface{}{map[string]interface{}{"type": "text", "text": "a cat"}},
			wantKey: "1080p",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := relaycommon.TaskSubmitReq{
				Duration: 5,
				Metadata: relaycommon.CanonicalizeMetadataKeys(map[string]interface{}{
					"resolution": "1080p",
					"content":    tc.content,
				}),
			}
			est, err := estimateVideoTokenBilling("seedance-nested", req, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.wantKey, est.Tariff.Key)
		})
	}
}

// 上游实际生成的秒数不总等于请求里的 duration：海螺不指定时长时生成 6 秒、Sora 生成
// 4 秒，方舟的 frames 优先级高于 duration、duration=-1 时由模型自选。适配器报出实际
// 秒数时必须以它为准，否则按通用的 5 秒估算会少收或多收。
func TestEstimateVideoTokenBillingUsesUpstreamBillableSeconds(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.video_token_price": `{"video-billable-seconds":{"sec:1080p":0.8}}`,
	}))

	cases := []struct {
		name            string
		req             relaycommon.TaskSubmitReq
		billableSeconds float64
		wantSeconds     float64
	}{
		{
			name:        "no upstream override falls back to 5s",
			req:         relaycommon.TaskSubmitReq{Size: "1080p"},
			wantSeconds: 5,
		},
		{
			name:        "no upstream override keeps the requested duration",
			req:         relaycommon.TaskSubmitReq{Size: "1080p", Duration: 10},
			wantSeconds: 10,
		},
		{
			name:            "hailuo generates 6s when the client omits duration",
			req:             relaycommon.TaskSubmitReq{Size: "1080p"},
			billableSeconds: 6,
			wantSeconds:     6,
		},
		{
			name:            "sora generates 4s when the client omits duration",
			req:             relaycommon.TaskSubmitReq{Size: "1080p"},
			billableSeconds: 4,
			wantSeconds:     4,
		},
		{
			// frames 优先于 duration：只认 duration 会按 5 秒收费、按 12 秒生成。
			// 帧数不一定整除帧率，秒数因此必须是小数——289 帧取整成 13 秒会多收 8%。
			name:            "an upstream override wins over the requested duration",
			req:             relaycommon.TaskSubmitReq{Size: "1080p", Duration: 5},
			billableSeconds: 289.0 / 24,
			wantSeconds:     289.0 / 24,
		},
		{
			name:            "an out-of-range override is capped",
			req:             relaycommon.TaskSubmitReq{Size: "1080p"},
			billableSeconds: relaycommon.MaxTaskDurationSeconds + 100,
			wantSeconds:     relaycommon.MaxTaskDurationSeconds,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			est, err := estimateVideoTokenBilling("video-billable-seconds", tc.req, tc.billableSeconds)
			require.NoError(t, err)
			assert.InDelta(t, tc.wantSeconds, est.OutSeconds, 1e-9)
			assert.InDelta(t, tc.wantSeconds, est.BillableUnits(), 0.001)
		})
	}
}

// 方舟按恒定像素预算分配分辨率，不是把短边钉在档位上：720p 的 1:1 是 960×960 而不是
// 720×720。这张表必须逐格对齐官方对照表，自己按比例推算会让方形视频少算 44%。
// https://docs.volcengine.com/docs/82379/1520757
func TestVideoTokenPixelsMatchArkOfficialTable(t *testing.T) {
	t.Parallel()

	official := map[string]map[string][2]int{
		"480p": {
			"16:9": {854, 480}, "4:3": {752, 560}, "1:1": {640, 640},
			"3:4": {560, 752}, "9:16": {480, 854}, "21:9": {992, 432},
		},
		"720p": {
			"16:9": {1280, 720}, "4:3": {1112, 834}, "1:1": {960, 960},
			"3:4": {834, 1112}, "9:16": {720, 1280}, "21:9": {1470, 630},
		},
		"1080p": {
			"16:9": {1920, 1080}, "4:3": {1664, 1248}, "1:1": {1440, 1440},
			"3:4": {1248, 1664}, "9:16": {1080, 1920}, "21:9": {2206, 946},
		},
		"4k": {
			"16:9": {3840, 2160}, "4:3": {3326, 2494}, "1:1": {2880, 2880},
			"3:4": {2494, 3326}, "9:16": {2160, 3840}, "21:9": {4398, 1886},
		},
	}

	for tier, ratios := range official {
		for ratio, want := range ratios {
			t.Run(tier+" "+ratio, func(t *testing.T) {
				assert.Equal(t, want, videoTokenPixels[tier][ratio])
				wantTokens := float64(want[0]*want[1]*videoTokenFPS*5) / 1024
				assert.InDelta(t, wantTokens, EstimateSeedanceTokens(tier, ratio, 5, 0, false), 0.001)
			})
		}
	}
}

// 表外的比例——adaptive、上游后加的比例——按 16:9 估算，而不是 0 像素。
func TestEstimateSeedanceTokensFallsBackForUnlistedRatios(t *testing.T) {
	t.Parallel()

	want := EstimateSeedanceTokens("720p", "16:9", 5, 0, false)
	for _, ratio := range []string{"", "adaptive", "32:9", "garbage"} {
		assert.InDelta(t, want, EstimateSeedanceTokens("720p", ratio, 5, 0, false), 0.001, ratio)
	}
}
