package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertOption(t *testing.T, key, value string) {
	t.Helper()
	require.NoError(t, DB.Create(&Option{Key: key, Value: value}).Error)
}

func resetLiveBillingSnapshot(t *testing.T) {
	t.Helper()
	billing_setting.PublishSnapshot(nil)
}

func TestLoadOptionsFromDatabaseModeFirstCompletesPair(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveOptionMap(t)
	resetLiveBillingSnapshot(t)

	insertOption(t, "billing_setting.billing_mode", `{"kling-v3":"task_unit_tier"}`)
	insertOption(t, "billing_setting.task_unit_tier_price", `{"kling-v3":{"720p":0.9}}`)

	loadOptionsFromDatabase()

	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.9, table["720p"])
}

func TestLoadOptionsFromDatabaseTableFirstCompletesPair(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveOptionMap(t)
	resetLiveBillingSnapshot(t)

	insertOption(t, "billing_setting.task_unit_tier_price", `{"kling-v3":{"720p":0.9}}`)
	insertOption(t, "billing_setting.billing_mode", `{"kling-v3":"task_unit_tier"}`)

	loadOptionsFromDatabase()

	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.9, table["720p"])
}

func TestLoadOptionsFromDatabaseReloadKeepsPreviousOnJSONError(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveOptionMap(t)
	resetLiveBillingSnapshot(t)

	insertOption(t, "billing_setting.billing_mode", `{"kling-v3":"task_unit_tier"}`)
	insertOption(t, "billing_setting.task_unit_tier_price", `{"kling-v3":{"720p":0.9}}`)
	loadOptionsFromDatabase()

	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "billing_setting.billing_mode").Update("value", `{not-json`).Error)
	loadOptionsFromDatabase()

	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.9, table["720p"])
}

func TestLoadOptionsFromDatabaseIncompletePairFailClosed(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveOptionMap(t)
	resetLiveBillingSnapshot(t)

	insertOption(t, "billing_setting.billing_mode", `{"kling-v3":"task_unit_tier"}`)
	loadOptionsFromDatabase()

	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Empty(t, table)
	assert.NotEqual(t, billing_setting.BillingModeRatio, mode)
}

func TestInitOptionMapLoadsCompleteBillingPair(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveOptionMap(t)
	resetLiveBillingSnapshot(t)

	insertOption(t, "billing_setting.billing_mode", `{"kling-v3":"task_unit_tier"}`)
	insertOption(t, "billing_setting.task_unit_tier_price", `{"kling-v3":{"720p":0.9}}`)

	InitOptionMap()

	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.9, table["720p"])
}

func TestBillingOptionLoadDialect(t *testing.T) {
	require.NotNil(t, DB)
	name := DB.Dialector.Name()
	switch name {
	case "sqlite":
	case "mysql", "postgres":
		t.Skip("billing option load uses the shared production loader; mysql/postgres fixtures are not wired in this package")
	default:
		t.Skipf("unsupported dialect %s", name)
	}
	assert.Equal(t, "sqlite", name)
}

func saveOptionMap(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	saved := make(map[string]string, len(common.OptionMap))
	for key, value := range common.OptionMap {
		saved[key] = value
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = saved
		common.OptionMapRWMutex.Unlock()
	})
}
