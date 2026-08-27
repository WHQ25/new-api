package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadBillingSetting(t *testing.T, values map[string]string) {
	t.Helper()
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(values))
}

func TestLookupVideoTokenPricePerToken(t *testing.T) {
	loadBillingSetting(t, map[string]string{
		"billing_setting.billing_mode":      `{"seedance-2":"video_token"}`,
		"billing_setting.video_token_price": `{"seedance-2":{"720p":7,"720p_video":4.2,"1080p":7.7}}`,
	})

	assert.True(t, IsVideoTokenBilling("seedance-2"))
	assert.True(t, HasVideoTokenPrice("seedance-2"))
	assert.Equal(t, VideoTokenUnitToken, GetVideoTokenUnit("seedance-2"))

	tariff, err := LookupVideoTokenPrice("seedance-2", "720P", nil)
	require.NoError(t, err)
	assert.Equal(t, "720p", tariff.Key)
	assert.Equal(t, VideoTokenUnitToken, tariff.Unit)
	assert.Equal(t, 7.0, tariff.Price)

	tariff, err = LookupVideoTokenPrice("seedance-2", "720p", []string{VideoTokenVariantVideo})
	require.NoError(t, err)
	assert.Equal(t, "720p_video", tariff.Key)
	assert.Equal(t, 4.2, tariff.Price)

	// 该表没有任何 _audio 格子，说明这个模型不按音频分档：音频信号不能把请求推到
	// 一个没配价的 key 上，否则每个带 generate_audio 的 Seedance 请求都会 400。
	tariff, err = LookupVideoTokenPrice("seedance-2", "720p", []string{VideoTokenVariantAudio})
	require.NoError(t, err)
	assert.Equal(t, "720p", tariff.Key)
	assert.Equal(t, 7.0, tariff.Price)

	tariff, err = LookupVideoTokenPrice("seedance-2", "4k", nil)
	require.Error(t, err)
	assert.Equal(t, "4k", tariff.Key)

	_, err = LookupVideoTokenPrice("seedance-2", "1440p", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported video resolution")

	_, err = LookupVideoTokenPrice("missing", "720p", nil)
	require.Error(t, err)

	_, err = LookupVideoTokenPrice("seedance-2", "", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrVideoTokenResolutionRequired)
}

func TestLookupVideoTokenPricePerSecond(t *testing.T) {
	// 可灵官方价目：分辨率 × 音频档，单价 ¥/秒。
	loadBillingSetting(t, map[string]string{
		"billing_setting.billing_mode": `{"kling-v3":"video_token"}`,
		"billing_setting.video_token_price": `{"kling-v3":{
			"sec:720p":0.6,"sec:720p_audio":0.9,"sec:720p_audio_voice":1.1,
			"sec:1080p":0.8,"sec:1080p_audio":1.2,"sec:1080p_audio_voice":1.4,
			"sec:4k":3.0,"sec:4k_audio":3.0,"sec:4k_audio_voice":3.0}}`,
	})

	assert.Equal(t, VideoTokenUnitSecond, GetVideoTokenUnit("kling-v3"))

	cases := []struct {
		name       string
		resolution string
		variants   []string
		wantKey    string
		wantPrice  float64
	}{
		{"720p silent", "720p", nil, "sec:720p", 0.6},
		{"720p audio", "720P", []string{VideoTokenVariantAudio}, "sec:720p_audio", 0.9},
		{"1080p audio + voice", "1080p", []string{VideoTokenVariantAudio, VideoTokenVariantVoice}, "sec:1080p_audio_voice", 1.4},
		{"variant order is normalized", "1080p", []string{VideoTokenVariantVoice, VideoTokenVariantAudio}, "sec:1080p_audio_voice", 1.4},
		{"4k silent", "2160p", nil, "sec:4k", 3.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tariff, err := LookupVideoTokenPrice("kling-v3", tc.resolution, tc.variants)
			require.NoError(t, err)
			assert.Equal(t, tc.wantKey, tariff.Key)
			assert.Equal(t, VideoTokenUnitSecond, tariff.Unit)
			assert.Equal(t, tc.wantPrice, tariff.Price)
		})
	}

	// 该表按音频分档，但没配 480p 这一行：不能退到别的行收钱，必须拒绝请求。
	tariff, err := LookupVideoTokenPrice("kling-v3", "480p", []string{VideoTokenVariantAudio})
	require.Error(t, err)
	assert.Equal(t, "sec:480p_audio", tariff.Key)
	assert.Zero(t, tariff.Price)
}

// 单价必须是正数：0 和负数要在查表这一步就拒绝，而不是乘进额度里变成免费或负扣费。
func TestLookupVideoTokenPriceRejectsUnusablePrices(t *testing.T) {
	loadBillingSetting(t, map[string]string{
		"billing_setting.video_token_price": `{"kling-v3":{"sec:720p":0,"sec:1080p":-1,"sec:4k":3}}`,
	})

	for _, resolution := range []string{"720p", "1080p"} {
		_, err := LookupVideoTokenPrice("kling-v3", resolution, nil)
		require.Error(t, err, resolution)
	}
	tariff, err := LookupVideoTokenPrice("kling-v3", "4k", nil)
	require.NoError(t, err)
	assert.Equal(t, 3.0, tariff.Price)
}

// 一张表混着两种计量单位时，本节点与其它版本节点读到的可能不是同一行，
// $/秒 与 $/百万 token 差六个数量级，必须整表拒绝而不是挑一行收钱。
func TestLookupVideoTokenPriceRejectsMixedUnits(t *testing.T) {
	loadBillingSetting(t, map[string]string{
		"billing_setting.video_token_price": `{"kling-v3":{"720p":7,"sec:1080p":0.8}}`,
	})

	for _, resolution := range []string{"720p", "1080p"} {
		_, err := LookupVideoTokenPrice("kling-v3", resolution, nil)
		require.Error(t, err, resolution)
		assert.Contains(t, err.Error(), "mixes per-second and per-token")
	}
}
