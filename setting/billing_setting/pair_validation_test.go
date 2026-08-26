package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadBillingMaps(t *testing.T, values map[string]string) {
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

func TestPrepareUpdatesLeavesUntouchedBrokenVideoTokenPair(t *testing.T) {
	loadBillingMaps(t, map[string]string{
		"billing_setting.billing_mode":         `{"seed":"video_token"}`,
		"billing_setting.billing_expr":         `{}`,
		"billing_setting.video_token_price":    `{}`,
		"billing_setting.task_unit_tier_price": `{}`,
	})
	assert.Equal(t, BillingModeVideoToken, GetBillingMode("seed"))
	assert.False(t, HasVideoTokenPrice("seed"))

	prepared, err := PrepareUpdates(map[string]string{
		BillingModeField:       `{"seed":"video_token","kling-v3":"task_unit_tier"}`,
		BillingExprField:       `{}`,
		VideoTokenPriceField:   `{}`,
		TaskUnitTierPriceField: `{"kling-v3":{"720p":0.6}}`,
	})
	require.NoError(t, err)
	require.NotNil(t, prepared)
	prepared.Publish()

	assert.Equal(t, BillingModeVideoToken, GetBillingMode("seed"))
	assert.False(t, HasVideoTokenPrice("seed"))
	assert.Equal(t, BillingModeTaskUnitTier, GetBillingMode("kling-v3"))
	assert.Equal(t, 0.6, GetTaskUnitTierPriceTable("kling-v3")["720p"])
}

func TestPrepareUpdatesRejectsNewBrokenVideoTokenPair(t *testing.T) {
	loadBillingMaps(t, map[string]string{
		"billing_setting.billing_mode":      `{}`,
		"billing_setting.video_token_price": `{}`,
	})

	_, err := PrepareUpdates(map[string]string{
		BillingModeField:     `{"seed":"video_token"}`,
		VideoTokenPriceField: `{}`,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video token price is not configured")
	assert.Contains(t, err.Error(), "seed")
}

func TestPrepareUpdatesFixesBrokenVideoTokenPair(t *testing.T) {
	loadBillingMaps(t, map[string]string{
		"billing_setting.billing_mode":      `{"seed":"video_token"}`,
		"billing_setting.video_token_price": `{}`,
	})

	prepared, err := PrepareUpdates(map[string]string{
		BillingModeField:     `{"seed":"video_token"}`,
		VideoTokenPriceField: `{"seed":{"480p":1.8}}`,
	})
	require.NoError(t, err)
	prepared.Publish()
	assert.Equal(t, 1.8, GetVideoTokenPriceTable("seed")["480p"])
}

func TestValidatePairedBillingSettingsLeavesUntouchedBrokenPair(t *testing.T) {
	loadBillingMaps(t, map[string]string{
		"billing_setting.billing_mode":         `{"seed":"video_token"}`,
		"billing_setting.billing_expr":         `{}`,
		"billing_setting.video_token_price":    `{}`,
		"billing_setting.task_unit_tier_price": `{}`,
	})

	require.NoError(t, ValidatePairedBillingSettings(
		`{"seed":"video_token","kling-v3":"task_unit_tier"}`,
		`{"kling-v3":{"720p":0.6}}`,
		`{}`,
		`{}`,
	))
	assert.Equal(t, BillingModeVideoToken, GetBillingMode("seed"))
	assert.False(t, HasVideoTokenPrice("seed"))
}

func TestPrepareUpdatesRejectsWorsenedVideoTokenPair(t *testing.T) {
	loadBillingMaps(t, map[string]string{
		"billing_setting.billing_mode":      `{"seed":"video_token"}`,
		"billing_setting.video_token_price": `{}`,
	})

	_, err := PrepareUpdates(map[string]string{
		BillingModeField:     `{"seed":"video_token"}`,
		VideoTokenPriceField: `{"seed":{"480p":0}}`,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video token price is not configured")
	assert.False(t, HasVideoTokenPrice("seed"))
}
