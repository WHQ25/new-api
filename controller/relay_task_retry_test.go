package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShouldRetryTaskRelayLocalForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.False(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{
		StatusCode: http.StatusForbidden,
		LocalError: true,
	}, 3))
	require.True(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{
		StatusCode: http.StatusForbidden,
		LocalError: false,
	}, 3))
	require.False(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{
		StatusCode: http.StatusForbidden,
		LocalError: false,
	}, 0))
}

func TestShouldRetryTaskRelaySkipRetryLocal500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	skipErr := types.NewError(
		errors.New("update user quota failed"),
		types.ErrorCodeUpdateDataError,
		types.ErrOptionWithSkipRetry(),
	)
	taskErr := service.TaskErrorFromAPIError(skipErr)
	require.NotNil(t, taskErr)
	require.Equal(t, http.StatusInternalServerError, taskErr.StatusCode)
	require.True(t, taskErr.LocalError)
	require.False(t, shouldRetryTaskRelay(c, 1, taskErr, 3))

	require.False(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{
		StatusCode: http.StatusInternalServerError,
		LocalError: true,
		Code:       "update_data_error",
	}, 3))
	require.True(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{
		StatusCode: http.StatusInternalServerError,
		LocalError: false,
	}, 3))
}

func TestRelayTaskStopsAutoGroupRetryWhenBillingInsufficient(t *testing.T) {
	t.Run("wallet", func(t *testing.T) {
		runRelayTaskBillingSkipRetryCase(t, relayTaskBillingSkipRetryCase{
			preference:     "wallet_only",
			userQuota:      2_000_000,
			useSub:         false,
			wantStatus:     http.StatusForbidden,
			wantCode:       "insufficient_user_quota",
			wantUsedGroups: 1,
		})
	})
	t.Run("subscription", func(t *testing.T) {
		runRelayTaskBillingSkipRetryCase(t, relayTaskBillingSkipRetryCase{
			preference:     "subscription_only",
			userQuota:      0,
			useSub:         true,
			subTotal:       2_000_000,
			subUsed:        0,
			wantStatus:     http.StatusForbidden,
			wantCode:       "insufficient_user_quota",
			wantUsedGroups: 1,
		})
	})
}

type relayTaskBillingSkipRetryCase struct {
	preference     string
	userQuota      int
	useSub         bool
	subTotal       int64
	subUsed        int64
	wantStatus     int
	wantCode       string
	wantUsedGroups int
}

func runRelayTaskBillingSkipRetryCase(t *testing.T, tc relayTaskBillingSkipRetryCase) {
	t.Helper()

	setupRelayTaskRetryTestDB(t)
	restoreRelayTaskRetryGlobals(t)

	const (
		userID         = 9101
		tokenID        = 9101
		vipChannelID   = 9101
		cheapChannelID = 9102
		planID         = 9101
		subID          = 9101
		modelName      = "kling-v3"
		unitPrice      = 0.6
		seconds        = 5.0
		expensiveRatio = 2.0
		cheapRatio     = 1.0
	)

	expensiveQuota, err := common.QuotaFromFloatStrict(seconds * unitPrice * common.QuotaPerUnit * expensiveRatio)
	require.NoError(t, err)
	cheapQuota, err := common.QuotaFromFloatStrict(seconds * unitPrice * common.QuotaPerUnit * cheapRatio)
	require.NoError(t, err)
	require.Greater(t, expensiveQuota, tc.userQuota+int(tc.subTotal-tc.subUsed))
	require.Less(t, cheapQuota, 2_000_000)

	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"up-1","task_id":"up-1","status":"queued"}`))
	}))
	t.Cleanup(upstream.Close)

	priority := int64(0)
	weight := uint(100)
	baseURL := upstream.URL
	require.NoError(t, model.DB.Create(&model.User{
		Id:       userID,
		Username: fmt.Sprintf("tr-%d", userID),
		Password: "password",
		Quota:    tc.userQuota,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  fmt.Sprintf("tra%d", userID),
	}).Error)
	require.NoError(t, model.DB.Create(&model.Token{
		Id:              tokenID,
		UserId:          userID,
		Key:             "task-retry-token-key",
		Name:            "task-retry",
		Status:          common.TokenStatusEnabled,
		UnlimitedQuota:  true,
		Group:           "auto",
		CrossGroupRetry: true,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:       vipChannelID,
		Type:     constant.ChannelTypeNewAPI,
		Key:      "sk-vip",
		Status:   common.ChannelStatusEnabled,
		Name:     "vip-kling",
		Weight:   &weight,
		Models:   modelName,
		Group:    "vip",
		Priority: &priority,
		BaseURL:  &baseURL,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:       cheapChannelID,
		Type:     constant.ChannelTypeNewAPI,
		Key:      "sk-default",
		Status:   common.ChannelStatusEnabled,
		Name:     "default-kling",
		Weight:   &weight,
		Models:   modelName,
		Group:    "default",
		Priority: &priority,
		BaseURL:  &baseURL,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     "vip",
		Model:     modelName,
		ChannelId: vipChannelID,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: cheapChannelID,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)

	if tc.useSub {
		overflow := false
		require.NoError(t, model.DB.Create(&model.SubscriptionPlan{
			Id:                  planID,
			Title:               "task-retry-plan",
			Enabled:             true,
			QuotaResetPeriod:    model.SubscriptionResetNever,
			AllowWalletOverflow: &overflow,
		}).Error)
		require.NoError(t, model.DB.Create(&model.UserSubscription{
			Id:                  subID,
			UserId:              userID,
			PlanId:              planID,
			AmountTotal:         tc.subTotal,
			AmountUsed:          tc.subUsed,
			Status:              "active",
			StartTime:           time.Now().Add(-time.Hour).Unix(),
			EndTime:             time.Now().Add(30 * 24 * time.Hour).Unix(),
			AllowWalletOverflow: false,
		}).Error)
	}

	body := []byte(`{"model":"kling-v3","prompt":"a cat running","size":"720p","seconds":"5"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	common.SetContextKey(c, constant.ContextKeyUserId, userID)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "vip")
	common.SetContextKey(c, constant.ContextKeyUserQuota, tc.userQuota)
	common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: tc.preference})
	common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
	common.SetContextKey(c, constant.ContextKeyTokenId, tokenID)
	common.SetContextKey(c, constant.ContextKeyTokenKey, "task-retry-token-key")
	common.SetContextKey(c, constant.ContextKeyTokenUnlimited, true)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, true)
	common.SetContextKey(c, constant.ContextKeyAutoGroup, "vip")
	common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 1)
	common.SetContextKey(c, constant.ContextKeyChannelId, vipChannelID)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeNewAPI)
	common.SetContextKey(c, constant.ContextKeyChannelName, "vip-kling")
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-vip")
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)

	RelayTask(c)

	assert.Equal(t, tc.wantStatus, recorder.Code)
	assert.Equal(t, int32(0), hits.Load())
	assert.Equal(t, []string{fmt.Sprintf("%d", vipChannelID)}, c.GetStringSlice("use_channel"))

	var taskErr taskdto.TaskError
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &taskErr))
	assert.Equal(t, tc.wantCode, taskErr.Code)

	gotQuota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, tc.userQuota, gotQuota)

	if tc.useSub {
		var sub model.UserSubscription
		require.NoError(t, model.DB.First(&sub, subID).Error)
		assert.Equal(t, tc.subUsed, sub.AmountUsed)
	}
}

func TestRelayTaskStopsWalletRaiseWhenRetryingExpensiveGroup(t *testing.T) {
	setupRelayTaskRetryTestDB(t)
	restoreRelayTaskRetryGlobals(t)

	const (
		userID         = 9201
		tokenID        = 9201
		cheapChannelID = 9201
		vipChannelID   = 9202
		thirdChannelID = 9203
		modelName      = "kling-v3"
		unitPrice      = 0.6
		seconds        = 5.0
		userQuota      = 2_000_000
	)
	cheapQuota, err := common.QuotaFromFloatStrict(seconds * unitPrice * common.QuotaPerUnit * 1)
	require.NoError(t, err)
	expensiveQuota, err := common.QuotaFromFloatStrict(seconds * unitPrice * common.QuotaPerUnit * 2)
	require.NoError(t, err)
	require.Greater(t, userQuota, cheapQuota)
	require.Less(t, userQuota, expensiveQuota)

	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"upstream down"}`))
	}))
	t.Cleanup(upstream.Close)

	priority := int64(0)
	weight := uint(100)
	baseURL := upstream.URL
	require.NoError(t, model.DB.Create(&model.User{
		Id: userID, Username: "tr-raise", Password: "password", Quota: userQuota,
		Status: common.UserStatusEnabled, Group: "default", AffCode: "trb9201",
	}).Error)
	require.NoError(t, model.DB.Create(&model.Token{
		Id: tokenID, UserId: userID, Key: "task-raise-token", Name: "task-raise",
		Status: common.TokenStatusEnabled, UnlimitedQuota: true, Group: "auto", CrossGroupRetry: true,
	}).Error)
	for _, ch := range []struct {
		id    int
		group string
		name  string
		key   string
	}{
		{cheapChannelID, "default", "cheap-kling", "sk-cheap"},
		{vipChannelID, "vip", "vip-kling", "sk-vip"},
		{thirdChannelID, "pro", "pro-kling", "sk-pro"},
	} {
		require.NoError(t, model.DB.Create(&model.Channel{
			Id: ch.id, Type: constant.ChannelTypeNewAPI, Key: ch.key, Status: common.ChannelStatusEnabled,
			Name: ch.name, Weight: &weight, Models: modelName, Group: ch.group, Priority: &priority, BaseURL: &baseURL,
		}).Error)
		require.NoError(t, model.DB.Create(&model.Ability{
			Group: ch.group, Model: modelName, ChannelId: ch.id, Enabled: true, Priority: &priority, Weight: weight,
		}).Error)
	}

	savedUsable := setting.UserUsableGroups2JSONString()
	savedRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		_ = setting.UpdateUserUsableGroupsByJSONString(savedUsable)
		_ = ratio_setting.UpdateGroupRatioByJSONString(savedRatios)
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","pro":"Pro"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"pro":3}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","vip","pro"]`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("3"))

	body := []byte(`{"model":"kling-v3","prompt":"a cat running","size":"720p","seconds":"5"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	common.SetContextKey(c, constant.ContextKeyUserId, userID)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserQuota, userQuota)
	common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
	common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
	common.SetContextKey(c, constant.ContextKeyTokenId, tokenID)
	common.SetContextKey(c, constant.ContextKeyTokenKey, "task-raise-token")
	common.SetContextKey(c, constant.ContextKeyTokenUnlimited, true)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"default", "vip", "pro"})
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, true)
	common.SetContextKey(c, constant.ContextKeyAutoGroup, "default")
	common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 1)
	common.SetContextKey(c, constant.ContextKeyChannelId, cheapChannelID)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeNewAPI)
	common.SetContextKey(c, constant.ContextKeyChannelName, "cheap-kling")
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-cheap")
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)

	RelayTask(c)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, int32(1), hits.Load())
	used := c.GetStringSlice("use_channel")
	assert.NotContains(t, used, fmt.Sprintf("%d", thirdChannelID))
	require.Eventually(t, func() bool {
		gotQuota, err := model.GetUserQuota(userID, true)
		return err == nil && gotQuota == userQuota
	}, 2*time.Second, 20*time.Millisecond)
}

func setupRelayTaskRetryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	initModelListColumnNames(t)

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Channel{},
		&model.Ability{},
		&model.Task{},
		&model.Log{},
		&model.SubscriptionPlan{},
		&model.UserSubscription{},
		&model.SubscriptionPreConsumeRecord{},
	))

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func restoreRelayTaskRetryGlobals(t *testing.T) {
	t.Helper()

	originalRetryTimes := common.RetryTimes
	originalMemoryCache := common.MemoryCacheEnabled
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalMaxTokenAutoGroups := setting.GetMaxTokenAutoGroups()
	savedBilling := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if strings.HasPrefix(key, "billing_setting.") {
			savedBilling[key] = value
		}
		return nil
	}))

	common.RetryTimes = 3
	common.MemoryCacheEnabled = false
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	t.Cleanup(func() {
		common.RetryTimes = originalRetryTimes
		common.MemoryCacheEnabled = originalMemoryCache
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMaxTokenAutoGroups)))
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedBilling))
	})
}
