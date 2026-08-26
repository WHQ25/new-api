package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertModelMetaWithPricingRejectsUnsupportedKey(t *testing.T) {
	setupPricingControllerDB(t)
	savedPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(savedPrice)
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"keep":0.02}`))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/models/with-pricing",
		strings.NewReader(`{"model":{"model_name":"kling-v3","status":1},"options":{"GroupRatio":"not-json"}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	UpsertModelMetaWithPricing(c)

	success, message := decodeOptionPayload(t, rec)
	assert.False(t, success)
	assert.Contains(t, message, "unsupported pricing option key")
	var count int64
	require.NoError(t, model.DB.Model(&model.Model{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
	price, ok := ratio_setting.GetModelPrice("keep", false)
	assert.True(t, ok)
	assert.Equal(t, 0.02, price)
	mode, _ := billing_setting.ObserveTaskUnitPair("kling-v3")
	assert.NotEqual(t, billing_setting.BillingModeTaskUnitTier, mode)
	var stored model.Option
	err := model.DB.Where("key = ?", "GroupRatio").First(&stored).Error
	assert.Error(t, err)
}

func TestUpsertModelMetaWithPricingRejectsMalformedNonPricingKey(t *testing.T) {
	setupPricingControllerDB(t)
	savedPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(savedPrice)
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"keep":0.02}`))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/models/with-pricing",
		strings.NewReader(`{"model":{"model_name":"kling-v3","status":1},"options":{"SMTPServer":"{","Notice":"x"}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	UpsertModelMetaWithPricing(c)

	success, message := decodeOptionPayload(t, rec)
	assert.False(t, success)
	assert.Contains(t, message, "unsupported pricing option key")
	var count int64
	require.NoError(t, model.DB.Model(&model.Model{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
	_, ok := ratio_setting.GetModelPrice("keep", false)
	assert.True(t, ok)
	var smtp model.Option
	err := model.DB.Where("key = ?", "SMTPServer").First(&smtp).Error
	assert.Error(t, err)
}
