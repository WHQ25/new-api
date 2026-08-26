package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertModelWithOptionsRenameConflictTable(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)

	cases := []struct {
		name    string
		prepare func(t *testing.T)
		source  string
		dest    string
		assert  func(t *testing.T)
	}{
		{
			name: "ModelPrice",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"old-price":0.02,"new-price":0.03}`))
			},
			source: "old-price",
			dest:   "new-price",
			assert: func(t *testing.T) {
				price, ok := ratio_setting.GetModelPrice("old-price", false)
				assert.True(t, ok)
				assert.Equal(t, 0.02, price)
				price, ok = ratio_setting.GetModelPrice("new-price", false)
				assert.True(t, ok)
				assert.Equal(t, 0.03, price)
			},
		},
		{
			name: "ModelRatio",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"old-ratio":1,"new-ratio":2}`))
			},
			source: "old-ratio",
			dest:   "new-ratio",
			assert: func(t *testing.T) {
				assert.Equal(t, map[string]float64{"old-ratio": 1, "new-ratio": 2}, ratio_setting.GetModelRatioCopy())
			},
		},
		{
			name: "CompletionRatio",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"old-comp":1,"new-comp":2}`))
			},
			source: "old-comp",
			dest:   "new-comp",
			assert: func(t *testing.T) {
				assert.Equal(t, map[string]float64{"old-comp": 1, "new-comp": 2}, ratio_setting.GetCompletionRatioCopy())
			},
		},
		{
			name: "CacheRatio",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"old-cache":1,"new-cache":2}`))
			},
			source: "old-cache",
			dest:   "new-cache",
			assert: func(t *testing.T) {
				assert.Equal(t, map[string]float64{"old-cache": 1, "new-cache": 2}, ratio_setting.GetCacheRatioCopy())
			},
		},
		{
			name: "CreateCacheRatio",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(`{"old-cc":1,"new-cc":2}`))
			},
			source: "old-cc",
			dest:   "new-cc",
			assert: func(t *testing.T) {
				assert.Equal(t, map[string]float64{"old-cc": 1, "new-cc": 2}, ratio_setting.GetCreateCacheRatioCopy())
			},
		},
		{
			name: "ImageRatio",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateImageRatioByJSONString(`{"old-img":1,"new-img":2}`))
			},
			source: "old-img",
			dest:   "new-img",
			assert: func(t *testing.T) {
				assert.Equal(t, map[string]float64{"old-img": 1, "new-img": 2}, ratio_setting.GetImageRatioCopy())
			},
		},
		{
			name: "AudioRatio",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(`{"old-audio":1,"new-audio":2}`))
			},
			source: "old-audio",
			dest:   "new-audio",
			assert: func(t *testing.T) {
				assert.Equal(t, map[string]float64{"old-audio": 1, "new-audio": 2}, ratio_setting.GetAudioRatioCopy())
			},
		},
		{
			name: "AudioCompletionRatio",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(`{"old-ac":1,"new-ac":2}`))
			},
			source: "old-ac",
			dest:   "new-ac",
			assert: func(t *testing.T) {
				assert.Equal(t, map[string]float64{"old-ac": 1, "new-ac": 2}, ratio_setting.GetAudioCompletionRatioCopy())
			},
		},
		{
			name: "billing_mode",
			prepare: func(t *testing.T) {
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
					"billing_setting.billing_mode": `{"old-mode":"task_unit_tier","new-mode":"video_token"}`,
					"billing_setting.task_unit_tier_price": `{
						"old-mode":{"720p":0.6}
					}`,
					"billing_setting.video_token_price": `{"new-mode":{"720p":7}}`,
				}))
			},
			source: "old-mode",
			dest:   "new-mode",
			assert: func(t *testing.T) {
				assert.Equal(t, billing_setting.BillingModeTaskUnitTier, billing_setting.GetBillingMode("old-mode"))
				assert.Equal(t, billing_setting.BillingModeVideoToken, billing_setting.GetBillingMode("new-mode"))
			},
		},
		{
			name: "billing_expr",
			prepare: func(t *testing.T) {
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
					"billing_setting.billing_mode": `{"old-expr":"tiered_expr","new-expr":"tiered_expr"}`,
					"billing_setting.billing_expr": `{"old-expr":"input * 0.001","new-expr":"input * 0.002"}`,
				}))
			},
			source: "old-expr",
			dest:   "new-expr",
			assert: func(t *testing.T) {
				expr, ok := billing_setting.GetBillingExpr("old-expr")
				assert.True(t, ok)
				assert.Equal(t, "input * 0.001", expr)
				expr, ok = billing_setting.GetBillingExpr("new-expr")
				assert.True(t, ok)
				assert.Equal(t, "input * 0.002", expr)
			},
		},
		{
			name: "video_token_price",
			prepare: func(t *testing.T) {
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
					"billing_setting.billing_mode":      `{"old-video":"video_token","new-video":"video_token"}`,
					"billing_setting.video_token_price": `{"old-video":{"720p":7},"new-video":{"720p":8}}`,
				}))
			},
			source: "old-video",
			dest:   "new-video",
			assert: func(t *testing.T) {
				assert.Equal(t, 7.0, billing_setting.GetVideoTokenPriceTable("old-video")["720p"])
				assert.Equal(t, 8.0, billing_setting.GetVideoTokenPriceTable("new-video")["720p"])
			},
		},
		{
			name: "task_unit_tier_price",
			prepare: func(t *testing.T) {
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
					"billing_setting.billing_mode":         `{"old-unit":"task_unit_tier","new-unit":"task_unit_tier"}`,
					"billing_setting.task_unit_tier_price": `{"old-unit":{"720p":0.6},"new-unit":{"720p":0.9}}`,
				}))
			},
			source: "old-unit",
			dest:   "new-unit",
			assert: func(t *testing.T) {
				_, oldTable := billing_setting.ObserveTaskUnitPair("old-unit")
				_, newTable := billing_setting.ObserveTaskUnitPair("new-unit")
				assert.Equal(t, 0.6, oldTable["720p"])
				assert.Equal(t, 0.9, newTable["720p"])
			},
		},
		{
			name: "destination orphan without source",
			prepare: func(t *testing.T) {
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"dest-orphan":0.08}`))
			},
			source: "src-orphan",
			dest:   "dest-orphan",
			assert: func(t *testing.T) {
				price, ok := ratio_setting.GetModelPrice("dest-orphan", false)
				assert.True(t, ok)
				assert.Equal(t, 0.08, price)
				_, srcOk := ratio_setting.GetModelPrice("src-orphan", false)
				assert.False(t, srcOk)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, DB.Exec("DELETE FROM models").Error)
			tc.prepare(t)
			existing := &Model{ModelName: tc.source, Status: 1}
			require.NoError(t, existing.Insert())
			existing.ModelName = tc.dest
			err := UpsertModelWithOptions(existing, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "pricing conflict")
			var stored Model
			require.NoError(t, DB.First(&stored, existing.Id).Error)
			assert.Equal(t, tc.source, stored.ModelName)
			tc.assert(t)
		})
	}
}

func TestUpsertModelWithOptionsRenameIdenticalIsIdempotent(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"old-same":"task_unit_tier","new-same":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"old-same":{"720p":0.6},"new-same":{"720p":0.6}}`,
	}))
	existing := &Model{ModelName: "old-same", Status: 1}
	require.NoError(t, existing.Insert())
	existing.ModelName = "new-same"
	require.NoError(t, UpsertModelWithOptions(existing, nil))

	var stored Model
	require.NoError(t, DB.First(&stored, existing.Id).Error)
	assert.Equal(t, "new-same", stored.ModelName)
	oldMode, oldTable := billing_setting.ObserveTaskUnitPair("old-same")
	assert.NotEqual(t, billing_setting.BillingModeTaskUnitTier, oldMode)
	assert.Empty(t, oldTable)
	mode, table := billing_setting.ObserveTaskUnitPair("new-same")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 0.6, table["720p"])
}

func TestUpsertModelWithOptionsRenameOverlayPrecedence(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"old-over":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"old-over":{"720p":0.6}}`,
	}))
	existing := &Model{ModelName: "old-over", Status: 1}
	require.NoError(t, existing.Insert())
	existing.ModelName = "new-over"
	require.NoError(t, UpsertModelWithOptions(existing, map[string]string{
		"billing_setting.billing_mode":         `{"new-over":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"new-over":{"720p":1.2}}`,
	}))

	oldMode, oldTable := billing_setting.ObserveTaskUnitPair("old-over")
	assert.NotEqual(t, billing_setting.BillingModeTaskUnitTier, oldMode)
	assert.Empty(t, oldTable)
	mode, table := billing_setting.ObserveTaskUnitPair("new-over")
	assert.Equal(t, billing_setting.BillingModeTaskUnitTier, mode)
	assert.Equal(t, 1.2, table["720p"])
}

func TestUpsertModelWithOptionsRenameSoftDeletedNameReuseConflicts(t *testing.T) {
	setupBillingSnapshotTestDB(t)
	saveBillingConfig(t)
	saveRatioMaps(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"src-reuse":0.02,"dest-reuse":0.09}`))
	dest := &Model{ModelName: "dest-reuse", Status: 1}
	require.NoError(t, dest.Insert())
	require.NoError(t, dest.Delete())
	src := &Model{ModelName: "src-reuse", Status: 1}
	require.NoError(t, src.Insert())
	src.ModelName = "dest-reuse"
	err := UpsertModelWithOptions(src, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pricing conflict")
	var stored Model
	require.NoError(t, DB.First(&stored, src.Id).Error)
	assert.Equal(t, "src-reuse", stored.ModelName)
	price, ok := ratio_setting.GetModelPrice("src-reuse", false)
	assert.True(t, ok)
	assert.Equal(t, 0.02, price)
	price, ok = ratio_setting.GetModelPrice("dest-reuse", false)
	assert.True(t, ok)
	assert.Equal(t, 0.09, price)
}
