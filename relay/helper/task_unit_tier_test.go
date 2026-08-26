package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperTaskUnitTier(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3-omni":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3-omni":{"720p":0.6,"1080p_audio":1.0}}`,
	}))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{OriginModelName: "kling-v3-omni"}

	view := billing_setting.CurrentView()
	priceData, err := ModelPriceHelperTaskUnitTier(c, info, view, 5, "720p")
	require.NoError(t, err)
	assert.Equal(t, 1_500_000, priceData.Quota)
	assert.Equal(t, 0.6, priceData.TaskUnitPrice)
	assert.Equal(t, 5.0, priceData.TaskUnits)
	assert.Equal(t, "720p", priceData.TaskUnitTierKey)
	assert.Equal(t, "task_unit_tier", priceData.BillingMode)
	assert.False(t, priceData.UsePrice)

	_, err = ModelPriceHelperTaskUnitTier(c, info, view, 5, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tier missing")

	_, err = ModelPriceHelperTaskUnitTier(c, info, view, 0, "720p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finite positive")

	_, err = ModelPriceHelperTaskUnitTier(c, info, view, 5, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tier key is empty")

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3-omni":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3-omni":{"720p":1.2,"1080p_audio":1.0}}`,
	}))
	assert.Equal(t, 0.6, priceData.TaskUnitPrice)
	assert.Equal(t, 1_500_000, priceData.Quota)

	c.Set("auto_group", "default")
	info = &relaycommon.RelayInfo{OriginModelName: "kling-v3-omni", UsingGroup: "default"}
	priceData, err = ModelPriceHelperTaskUnitTier(c, info, billing_setting.CurrentView(), 10, "1080p_audio")
	require.NoError(t, err)
	assert.Equal(t, common.QuotaFromFloat(10*1.0*common.QuotaPerUnit), priceData.Quota)
}

func TestHasModelBillingConfigTaskUnitTier(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier","empty-tier":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	assert.True(t, HasModelBillingConfig("kling-v3"))
	assert.False(t, HasModelBillingConfig("empty-tier"))
}
