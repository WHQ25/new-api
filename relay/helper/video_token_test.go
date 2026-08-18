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
	})
	require.NoError(t, err)
	assert.Equal(t, "1080p", est.Tier)
	assert.Equal(t, 7.7, est.USDPerM)
	assert.InDelta(t, 243000, est.Tokens, 0.001)
	assert.False(t, est.HasVideo)

	est, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 10,
		Metadata: map[string]interface{}{
			"resolution":     "1080p",
			"input_duration": 10,
			"content": []interface{}{
				map[string]interface{}{"type": "video_url", "video_url": map[string]interface{}{"url": "https://example.com/a.mp4"}},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "1080p_video", est.Tier)
	assert.Equal(t, 4.6, est.USDPerM)
	assert.InDelta(t, 972000, est.Tokens, 0.001)

	_, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Metadata: map[string]interface{}{"resolution": "4k"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "4k")

	_, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Metadata: map[string]interface{}{"resolution": "1440p"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported video resolution")

	_, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Prompt:   "A small orange cat walks slowly --duration 5 --ratio 16:9 --resolution 480p",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, billing_setting.ErrVideoTokenResolutionRequired)

	_, err = estimateVideoTokenBilling("seedance-test", relaycommon.TaskSubmitReq{
		Duration: 5,
		Metadata: map[string]interface{}{},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, billing_setting.ErrVideoTokenResolutionRequired)
}
