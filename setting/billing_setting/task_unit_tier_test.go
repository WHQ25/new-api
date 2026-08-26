package billing_setting

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupTaskUnitTierPrice(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{
			"kling-v3":{"720p":0.6,"720p_audio":0.9,"bad_zero":0,"bad_neg":-1}
		}`,
	}))

	assert.True(t, IsTaskUnitTierBilling("kling-v3"))
	assert.True(t, HasTaskUnitTierPrice("kling-v3"))
	assert.False(t, IsTaskUnitTierBilling("missing"))
	assert.False(t, HasTaskUnitTierPrice("missing"))

	price, err := LookupTaskUnitTierPrice("kling-v3", "720p")
	require.NoError(t, err)
	assert.Equal(t, 0.6, price)

	_, err = LookupTaskUnitTierPrice("kling-v3", "720p_voice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tier 720p_voice")

	_, err = LookupTaskUnitTierPrice("missing", "720p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured for model missing")

	_, err = LookupTaskUnitTierPrice("kling-v3", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tier key is empty")

	_, err = LookupTaskUnitTierPrice("kling-v3", "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tier key is empty")

	_, err = LookupTaskUnitTierPrice("kling-v3", "bad_zero")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")

	_, err = LookupTaskUnitTierPrice("kling-v3", "bad_neg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")

	next := cloneSnapshot(currentSnapshot())
	if next.TaskUnitTierPrice["kling-v3"] == nil {
		next.TaskUnitTierPrice["kling-v3"] = map[string]float64{}
	}
	next.TaskUnitTierPrice["kling-v3"]["bad_nan"] = math.NaN()
	next.TaskUnitTierPrice["kling-v3"]["bad_inf"] = math.Inf(1)
	publishSnapshot(next)
	t.Cleanup(func() {
		restored := cloneSnapshot(currentSnapshot())
		delete(restored.TaskUnitTierPrice["kling-v3"], "bad_nan")
		delete(restored.TaskUnitTierPrice["kling-v3"], "bad_inf")
		publishSnapshot(restored)
	})

	_, err = LookupTaskUnitTierPrice("kling-v3", "bad_nan")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")

	_, err = LookupTaskUnitTierPrice("kling-v3", "bad_inf")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestGetTaskUnitTierPriceCopyIsIndependent(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	copied := GetTaskUnitTierPriceCopy()
	copied["kling-v3"]["720p"] = 99
	copied["injected"] = map[string]float64{"x": 1}

	live, err := LookupTaskUnitTierPrice("kling-v3", "720p")
	require.NoError(t, err)
	assert.Equal(t, 0.6, live)
	assert.False(t, HasTaskUnitTierPrice("injected"))
}

func TestValidateTaskUnitTierPriceJSON(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateTaskUnitTierPriceJSON(`{}`))
	require.NoError(t, ValidateTaskUnitTierPriceJSON(`{"kling-v3":{"720p":0.6,"4k_voice":2.4}}`))

	err := ValidateTaskUnitTierPriceJSON(`not-json`)
	require.Error(t, err)

	err = ValidateTaskUnitTierPriceJSON(`{"":{"720p":0.6}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty model name")

	err = ValidateTaskUnitTierPriceJSON(`{"kling-v3":{}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")

	err = ValidateTaskUnitTierPriceJSON(`{"kling-v3":{"":0.6}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty tier key")

	err = ValidateTaskUnitTierPriceJSON(`{"kling-v3":{"720p":0}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finite positive")

	err = ValidateTaskUnitTierPriceJSON(`{"kling-v3":{"720p":-1}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finite positive")
}

func TestValidateBillingModeJSON(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateBillingModeJSON(`{}`))
	require.NoError(t, ValidateBillingModeJSON(`{"a":"ratio","b":"tiered_expr","c":"video_token","d":"task_unit_tier"}`))

	err := ValidateBillingModeJSON(`{"a":"unknown"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported billing mode")

	err = ValidateBillingModeJSON(`{"":"task_unit_tier"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty model name")
}

func TestValidatePairedBillingSettingsVideoToken(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{}`,
		"billing_setting.billing_expr":         `{}`,
		"billing_setting.video_token_price":    `{}`,
		"billing_setting.task_unit_tier_price": `{}`,
	}))

	require.NoError(t, ValidatePairedBillingSettings(
		`{"kling-v3":"task_unit_tier"}`,
		`{"kling-v3":{"720p":0.6}}`,
		`{}`,
		`{}`,
	))
	require.NoError(t, ValidatePairedBillingSettings(
		`{"doubao-seedance-1-0-pro":"video_token"}`,
		`{}`,
		`{"doubao-seedance-1-0-pro":{"480p":1.8,"720p":2.5,"1080p":4}}`,
		`{}`,
	))

	err := ValidatePairedBillingSettings(
		`{"doubao-seedance-1-0-pro":"video_token"}`,
		`{}`,
		`{}`,
		`{}`,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video token price is not configured")
	assert.Contains(t, err.Error(), "doubao-seedance-1-0-pro")

	err = ValidatePairedBillingSettings(
		`{"doubao-seedance-1-0-pro":"video_token"}`,
		`{}`,
		`{"doubao-seedance-1-0-pro":{"480p":0,"720p":-1}}`,
		`{}`,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video token price is not configured")
}

func TestValidatePairedBillingSettingsTieredExpr(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{}`,
		"billing_setting.billing_expr": `{}`,
	}))

	require.NoError(t, ValidatePairedBillingSettings(
		`{"expr-model":"tiered_expr"}`,
		`{}`,
		`{}`,
		`{"expr-model":"input * 0.001"}`,
	))

	err := ValidatePairedBillingSettings(
		`{"expr-model":"tiered_expr"}`,
		`{}`,
		`{}`,
		`{}`,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "billing expression is not configured")
	assert.Contains(t, err.Error(), "expr-model")
}
