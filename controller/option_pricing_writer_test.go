package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPricingControllerDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	previous, previousLog := model.DB, model.LOG_DB
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Model{}, &model.Log{}, &model.User{}))
	common.OptionMapRWMutex.Lock()
	savedMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		model.SetPricingPersistHook(nil)
		model.SetPricingAfterCommit(nil)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = savedMap
		common.OptionMapRWMutex.Unlock()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		model.DB = previous
		model.LOG_DB = previousLog
		common.RedisEnabled = previousRedis
	})
}

func decodeOptionPayload(t *testing.T, rec *httptest.ResponseRecorder) (bool, string) {
	t.Helper()
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &payload))
	return payload.Success, payload.Message
}

func TestUpdateOptionSingleRatioCommitFailureLeavesRuntime(t *testing.T) {
	setupPricingControllerDB(t)
	cases := []struct {
		key   string
		value string
		copy  func() map[string]float64
		probe string
	}{
		{"ImageRatio", `{"probe-image":3.5}`, ratio_setting.GetImageRatioCopy, "probe-image"},
		{"AudioRatio", `{"probe-audio":4.5}`, ratio_setting.GetAudioRatioCopy, "probe-audio"},
		{"AudioCompletionRatio", `{"probe-audio-c":5.5}`, ratio_setting.GetAudioCompletionRatioCopy, "probe-audio-c"},
		{"CreateCacheRatio", `{"probe-cache":6.5}`, ratio_setting.GetCreateCacheRatioCopy, "probe-cache"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			saved := map[string]string{
				"ImageRatio":           ratio_setting.ImageRatio2JSONString(),
				"AudioRatio":           ratio_setting.AudioRatio2JSONString(),
				"AudioCompletionRatio": ratio_setting.AudioCompletionRatio2JSONString(),
				"CreateCacheRatio":     ratio_setting.CreateCacheRatio2JSONString(),
			}
			t.Cleanup(func() {
				_ = ratio_setting.UpdateImageRatioByJSONString(saved["ImageRatio"])
				_ = ratio_setting.UpdateAudioRatioByJSONString(saved["AudioRatio"])
				_ = ratio_setting.UpdateAudioCompletionRatioByJSONString(saved["AudioCompletionRatio"])
				_ = ratio_setting.UpdateCreateCacheRatioByJSONString(saved["CreateCacheRatio"])
			})
			before := tc.copy()
			model.SetPricingPersistHook(func(*gorm.DB, map[string]string) error {
				return errors.New("injected persist failure")
			})
			t.Cleanup(func() { model.SetPricingPersistHook(nil) })

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(
				http.MethodPut,
				"/api/option/",
				strings.NewReader(fmt.Sprintf(`{"key":%q,"value":%q}`, tc.key, tc.value)),
			)
			UpdateOption(c)

			success, message := decodeOptionPayload(t, rec)
			assert.False(t, success)
			assert.Contains(t, message, "injected persist failure")
			_, ok := tc.copy()[tc.probe]
			assert.False(t, ok)
			assert.Equal(t, before, tc.copy())
			var stored model.Option
			err := model.DB.Where("key = ?", tc.key).First(&stored).Error
			assert.Error(t, err)
		})
	}
}

func TestResetModelRatioAndCustomWriterLastCommitWins(t *testing.T) {
	setupPricingControllerDB(t)
	saved := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(saved)
	})

	t.Run("custom commits later", func(t *testing.T) {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"keep":1}`))
		var once sync.Once
		model.SetPricingAfterCommit(func() {
			once.Do(func() {
				model.SetPricingAfterCommit(nil)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(
					http.MethodPut,
					"/api/option/",
					strings.NewReader(`{"key":"ModelRatio","value":"{\"custom-probe\":9}"}`),
				)
				UpdateOption(c)
				success, _ := decodeOptionPayload(t, rec)
				require.True(t, success)
			})
		})
		t.Cleanup(func() { model.SetPricingAfterCommit(nil) })

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/reset_model_ratio", nil)
		ResetModelRatio(c)
		success, _ := decodeOptionPayload(t, rec)
		require.True(t, success)

		copied := ratio_setting.GetModelRatioCopy()
		assert.Equal(t, 9.0, copied["custom-probe"])
		_, hasKeep := copied["keep"]
		assert.False(t, hasKeep)
		var stored model.Option
		require.NoError(t, model.DB.Where("key = ?", "ModelRatio").First(&stored).Error)
		assert.Contains(t, stored.Value, "custom-probe")
		assert.NotContains(t, stored.Value, `"keep"`)
	})

	t.Run("reset commits later", func(t *testing.T) {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"keep":1}`))
		var once sync.Once
		model.SetPricingAfterCommit(func() {
			once.Do(func() {
				model.SetPricingAfterCommit(nil)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/api/reset_model_ratio", nil)
				ResetModelRatio(c)
				success, _ := decodeOptionPayload(t, rec)
				require.True(t, success)
			})
		})
		t.Cleanup(func() { model.SetPricingAfterCommit(nil) })

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(
			http.MethodPut,
			"/api/option/",
			strings.NewReader(`{"key":"ModelRatio","value":"{\"custom-probe\":9}"}`),
		)
		UpdateOption(c)
		success, _ := decodeOptionPayload(t, rec)
		require.True(t, success)

		copied := ratio_setting.GetModelRatioCopy()
		_, hasCustom := copied["custom-probe"]
		assert.False(t, hasCustom)
		var stored model.Option
		require.NoError(t, model.DB.Where("key = ?", "ModelRatio").First(&stored).Error)
		assert.NotContains(t, stored.Value, "custom-probe")
		assert.Equal(t, ratio_setting.DefaultModelRatio2JSONString(), stored.Value)
	})
}
