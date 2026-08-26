package model

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingSnapshotTestDB(t *testing.T) {
	t.Helper()
	require.NotNil(t, DB)
	require.NoError(t, DB.AutoMigrate(&Option{}, &Model{}))
	require.NoError(t, DB.Exec("DELETE FROM options").Error)
	require.NoError(t, DB.Exec("DELETE FROM models").Error)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		_ = DB.Exec("DELETE FROM options")
		_ = DB.Exec("DELETE FROM models")
	})
}

func saveBillingConfig(t *testing.T) {
	t.Helper()
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if strings.HasPrefix(key, "billing_setting.") {
			saved[key] = value
		}
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
}

func TestUpdateOptionsBulkSwapsCompleteBillingSnapshot(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"ratio"}`,
		"billing_setting.task_unit_tier_price": `{}`,
	}))

	var mixed atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
					price := table["720p"]
					oldPair := mode != billing_setting.BillingModeTaskUnitTier && price != 0.9
					newPair := mode == billing_setting.BillingModeTaskUnitTier && price == 0.9
					if !oldPair && !newPair {
						mixed.Add(1)
					}
				}
			}
		}()
	}

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.9}}`,
	}))
	close(stop)
	wg.Wait()
	assert.Equal(t, int64(0), mixed.Load())

	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.9, table["720p"])
}

func TestUpdateOptionsBulkApplyFailureLeavesSnapshot(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"ratio"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	err := UpdateOptionsBulk(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{not-json`,
	})
	require.Error(t, err)

	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, "ratio", mode)
	assert.Equal(t, 0.6, table["720p"])

	var stored Option
	err = DB.Where("key = ?", "billing_setting.billing_mode").First(&stored).Error
	assert.Error(t, err)
}

func TestUpdateOptionsBulkPriceChangeKeepsPairedSnapshot(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	var mixed atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
					price := table["720p"]
					ok := mode == billing_setting.BillingModeTaskUnitTier && (price == 0.6 || price == 1.2)
					if !ok {
						mixed.Add(1)
					}
				}
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":1.2}}`,
	}))
	close(stop)
	wg.Wait()
	assert.Equal(t, int64(0), mixed.Load())
	_, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, 1.2, table["720p"])
}

func TestUpsertModelWithOptionsRollsBackOnBillingError(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{}`,
		"billing_setting.task_unit_tier_price": `{}`,
	}))

	m := &Model{ModelName: "kling-v3", Status: 1}
	err := UpsertModelWithOptions(m, map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{}`,
	})
	require.Error(t, err)

	var count int64
	require.NoError(t, DB.Model(&Model{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
	mode, _ := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.NotEqual(t, billing_setting.BillingModeTaskUnitTier, mode)
}

func TestUpsertModelWithOptionsRenameRollsBack(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"old-kling":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"old-kling":{"720p":0.6}}`,
	}))
	existing := &Model{ModelName: "old-kling", Status: 1}
	require.NoError(t, existing.Insert())

	existing.ModelName = "new-kling"
	err := UpsertModelWithOptions(existing, map[string]string{
		"billing_setting.billing_mode":         `{"new-kling":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{`,
	})
	require.Error(t, err)

	var stored Model
	require.NoError(t, DB.First(&stored, existing.Id).Error)
	assert.Equal(t, "old-kling", stored.ModelName)
	mode, table := billing_setting.ObserveTaskUnitPair("old-kling")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.6, table["720p"])
}

func TestUpsertModelWithOptionsSuccessWritesModelAndPricing(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{}`,
		"billing_setting.task_unit_tier_price": `{}`,
	}))

	m := &Model{ModelName: "kling-v3", Status: 1}
	require.NoError(t, UpsertModelWithOptions(m, map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
		"ModelPrice":                           `{}`,
	}))
	require.NotZero(t, m.Id)

	var stored Model
	require.NoError(t, DB.First(&stored, m.Id).Error)
	assert.Equal(t, "kling-v3", stored.ModelName)
	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.6, table["720p"])
}

func TestUpdateOptionsBulkTaskToRatioSwapsTogether(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	var mixed atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
					price := table["720p"]
					oldPair := mode == billing_setting.BillingModeTaskUnitTier && price == 0.6
					newPair := mode != billing_setting.BillingModeTaskUnitTier && price != 0.6
					if !oldPair && !newPair {
						mixed.Add(1)
					}
				}
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"ratio"}`,
		"billing_setting.task_unit_tier_price": `{}`,
	}))
	close(stop)
	wg.Wait()
	assert.Equal(t, int64(0), mixed.Load())
}

func TestPricingWriterLastCommitWinsReversePublish(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{}`,
		"billing_setting.task_unit_tier_price": `{}`,
	}))

	a, err := preparePricingApply(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return persistOptionsTx(tx, a.values)
	}))
	assignPreparedRevision(a)

	b, err := preparePricingApply(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":1.2}}`,
	})
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return persistOptionsTx(tx, b.values)
	}))
	assignPreparedRevision(b)

	applyPrepared(b)
	applyPrepared(a)

	mode, table := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 1.2, table["720p"])

	var stored Option
	require.NoError(t, DB.Where("key = ?", "billing_setting.task_unit_tier_price").First(&stored).Error)
	assert.Contains(t, stored.Value, "1.2")
}

func TestUpsertModelWithOptionsMalformedModelPriceLeavesUnchanged(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{}`,
		"billing_setting.task_unit_tier_price": `{}`,
	}))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"keep":0.02}`))

	m := &Model{ModelName: "kling-v3", Status: 1}
	err := UpsertModelWithOptions(m, map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
		"ModelPrice":                           `{not-json`,
	})
	require.Error(t, err)

	var count int64
	require.NoError(t, DB.Model(&Model{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
	mode, _ := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.NotEqual(t, billing_setting.BillingModeTaskUnitTier, mode)
	price, ok := ratio_setting.GetModelPrice("keep", false)
	assert.True(t, ok)
	assert.Equal(t, 0.02, price)
	var stored Option
	err = DB.Where("key = ?", "ModelPrice").First(&stored).Error
	assert.Error(t, err)
}

func TestUpsertModelWithOptionsRenameMigratesTaskUnit(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"old-kling":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"old-kling":{"720p":0.6}}`,
	}))
	existing := &Model{ModelName: "old-kling", Status: 1}
	require.NoError(t, existing.Insert())

	existing.ModelName = "new-kling"
	require.NoError(t, UpsertModelWithOptions(existing, nil))

	var stored Model
	require.NoError(t, DB.First(&stored, existing.Id).Error)
	assert.Equal(t, "new-kling", stored.ModelName)
	oldMode, oldTable := billing_setting.ObserveTaskUnitPair("old-kling")
	assert.NotEqual(t, billing_setting.BillingModeTaskUnitTier, oldMode)
	assert.Empty(t, oldTable)
	mode, table := billing_setting.ObserveTaskUnitPair("new-kling")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.6, table["720p"])
	assert.NotContains(t, storedOptionValue(t, "billing_setting.billing_mode"), "old-kling")
	assert.Contains(t, storedOptionValue(t, "billing_setting.billing_mode"), "new-kling")
}

func TestUpsertModelWithOptionsRenameMigratesLegacyPrice(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"old-legacy":0.02}`))
	existing := &Model{ModelName: "old-legacy", Status: 1}
	require.NoError(t, existing.Insert())

	existing.ModelName = "new-legacy"
	require.NoError(t, UpsertModelWithOptions(existing, nil))

	_, oldOk := ratio_setting.GetModelPrice("old-legacy", false)
	assert.False(t, oldOk)
	price, ok := ratio_setting.GetModelPrice("new-legacy", false)
	assert.True(t, ok)
	assert.Equal(t, 0.02, price)
	assert.NotContains(t, storedOptionValue(t, "ModelPrice"), "old-legacy")
	assert.Contains(t, storedOptionValue(t, "ModelPrice"), "new-legacy")
}

func TestUpsertModelWithOptionsRenameMigratesExpr(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"old-expr":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"old-expr":"input * 0.001"}`,
	}))
	existing := &Model{ModelName: "old-expr", Status: 1}
	require.NoError(t, existing.Insert())

	existing.ModelName = "new-expr"
	require.NoError(t, UpsertModelWithOptions(existing, nil))

	assert.NotEqual(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("old-expr"))
	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("new-expr"))
	expr, ok := billing_setting.GetBillingExpr("new-expr")
	assert.True(t, ok)
	assert.Equal(t, "input * 0.001", expr)
	_, oldOk := billing_setting.GetBillingExpr("old-expr")
	assert.False(t, oldOk)
}

func TestUpsertModelWithOptionsRenameMigratesVideo(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":      `{"old-video":"video_token"}`,
		"billing_setting.video_token_price": `{"old-video":{"720p":7.0}}`,
	}))
	existing := &Model{ModelName: "old-video", Status: 1}
	require.NoError(t, existing.Insert())

	existing.ModelName = "new-video"
	require.NoError(t, UpsertModelWithOptions(existing, nil))

	assert.NotEqual(t, billing_setting.BillingModeVideoToken, billing_setting.GetBillingMode("old-video"))
	assert.Equal(t, billing_setting.BillingModeVideoToken, billing_setting.GetBillingMode("new-video"))
	assert.Equal(t, 7.0, billing_setting.GetVideoTokenPriceTable("new-video")["720p"])
	assert.Empty(t, billing_setting.GetVideoTokenPriceTable("old-video"))
}

func TestUpsertModelWithOptionsStrictPairRejectsVideoTokenWithoutPriceTable(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":      `{}`,
		"billing_setting.video_token_price": `{}`,
	}))

	m := &Model{ModelName: "doubao-seedance-1-0-pro", Status: 1}
	err := UpsertModelWithOptions(m, map[string]string{
		"billing_setting.billing_mode":      `{"doubao-seedance-1-0-pro":"video_token"}`,
		"billing_setting.video_token_price": `{}`,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video token price is not configured")

	var count int64
	require.NoError(t, DB.Model(&Model{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
	assert.NotEqual(t, billing_setting.BillingModeVideoToken, billing_setting.GetBillingMode("doubao-seedance-1-0-pro"))
}

func TestUpsertModelWithOptionsRejectsVideoTokenDowngradeToUnpriced(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":      `{"doubao-seedance-1-0-pro":"video_token"}`,
		"billing_setting.video_token_price": `{"doubao-seedance-1-0-pro":{"480p":1.8,"720p":2.5,"1080p":4}}`,
	}))
	existing := &Model{ModelName: "doubao-seedance-1-0-pro", Status: 1}
	require.NoError(t, existing.Insert())

	err := UpsertModelWithOptions(existing, map[string]string{
		"billing_setting.billing_mode":      `{}`,
		"billing_setting.video_token_price": `{}`,
		"ModelPrice":                        `{}`,
		"ModelRatio":                        `{"gpt-4":1}`,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unpriced")
	assert.Contains(t, err.Error(), "video_token")

	assert.Equal(t, billing_setting.BillingModeVideoToken, billing_setting.GetBillingMode("doubao-seedance-1-0-pro"))
	assert.Equal(t, 1.8, billing_setting.GetVideoTokenPriceTable("doubao-seedance-1-0-pro")["480p"])
	assert.Equal(t, 2.5, billing_setting.GetVideoTokenPriceTable("doubao-seedance-1-0-pro")["720p"])
	assert.Equal(t, 4.0, billing_setting.GetVideoTokenPriceTable("doubao-seedance-1-0-pro")["1080p"])
}

func TestUpsertModelWithOptionsAllowsVideoTokenToPricedRatio(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":      `{"doubao-seedance-1-0-pro":"video_token"}`,
		"billing_setting.video_token_price": `{"doubao-seedance-1-0-pro":{"480p":1.8,"720p":2.5,"1080p":4}}`,
	}))
	existing := &Model{ModelName: "doubao-seedance-1-0-pro", Status: 1}
	require.NoError(t, existing.Insert())

	require.NoError(t, UpsertModelWithOptions(existing, map[string]string{
		"billing_setting.billing_mode":      `{}`,
		"billing_setting.video_token_price": `{}`,
		"ModelPrice":                        `{}`,
		"ModelRatio":                        `{"doubao-seedance-1-0-pro":15}`,
	}))

	assert.NotEqual(t, billing_setting.BillingModeVideoToken, billing_setting.GetBillingMode("doubao-seedance-1-0-pro"))
	ratio, ok := ratio_setting.GetModelRatioCopy()["doubao-seedance-1-0-pro"]
	assert.True(t, ok)
	assert.Equal(t, 15.0, ratio)
}

func TestUpsertModelWithOptionsRejectsTieredExprDowngradeToUnpriced(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"expr-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"expr-model":"input * 0.001"}`,
	}))
	existing := &Model{ModelName: "expr-model", Status: 1}
	require.NoError(t, existing.Insert())

	err := UpsertModelWithOptions(existing, map[string]string{
		"billing_setting.billing_mode": `{}`,
		"billing_setting.billing_expr": `{}`,
		"ModelPrice":                   `{}`,
		"ModelRatio":                   `{"gpt-4":1}`,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unpriced")
	assert.Contains(t, err.Error(), "tiered_expr")
	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("expr-model"))
	expr, ok := billing_setting.GetBillingExpr("expr-model")
	assert.True(t, ok)
	assert.Equal(t, "input * 0.001", expr)
}

func TestUpsertModelWithOptionsRenameFailureRollsBackModelAndRuntime(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"old-kling":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"old-kling":{"720p":0.6}}`,
	}))
	existing := &Model{ModelName: "old-kling", Status: 1}
	require.NoError(t, existing.Insert())

	existing.ModelName = "new-kling"
	err := UpsertModelWithOptions(existing, map[string]string{
		"ModelRatio": `{`,
	})
	require.Error(t, err)

	var stored Model
	require.NoError(t, DB.First(&stored, existing.Id).Error)
	assert.Equal(t, "old-kling", stored.ModelName)
	mode, table := billing_setting.ObserveTaskUnitPair("old-kling")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.6, table["720p"])
}

func storedOptionValue(t *testing.T, key string) string {
	t.Helper()
	var stored Option
	require.NoError(t, DB.Where("key = ?", key).First(&stored).Error)
	return stored.Value
}

func saveRatioMaps(t *testing.T) {
	t.Helper()
	price := ratio_setting.ModelPrice2JSONString()
	ratio := ratio_setting.ModelRatio2JSONString()
	completion := ratio_setting.CompletionRatio2JSONString()
	cache := ratio_setting.CacheRatio2JSONString()
	createCache := ratio_setting.CreateCacheRatio2JSONString()
	image := ratio_setting.ImageRatio2JSONString()
	audio := ratio_setting.AudioRatio2JSONString()
	audioCompletion := ratio_setting.AudioCompletionRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(price)
		_ = ratio_setting.UpdateModelRatioByJSONString(ratio)
		_ = ratio_setting.UpdateCompletionRatioByJSONString(completion)
		_ = ratio_setting.UpdateCacheRatioByJSONString(cache)
		_ = ratio_setting.UpdateCreateCacheRatioByJSONString(createCache)
		_ = ratio_setting.UpdateImageRatioByJSONString(image)
		_ = ratio_setting.UpdateAudioRatioByJSONString(audio)
		_ = ratio_setting.UpdateAudioCompletionRatioByJSONString(audioCompletion)
	})
}
