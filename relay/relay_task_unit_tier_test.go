package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/newapi"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTaskAdaptor struct {
	ratios   map[string]float64
	estimate channel.TaskUnitTierEstimate
	estErr   error
}

func (stubTaskAdaptor) Init(*relaycommon.RelayInfo) {}
func (stubTaskAdaptor) ValidateRequestAndSetAction(*gin.Context, *relaycommon.RelayInfo) *taskdto.TaskError {
	return nil
}
func (a stubTaskAdaptor) EstimateBilling(*gin.Context, *relaycommon.RelayInfo) map[string]float64 {
	return a.ratios
}
func (stubTaskAdaptor) AdjustBillingOnSubmit(*relaycommon.RelayInfo, []byte) map[string]float64 {
	return nil
}
func (stubTaskAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int { return 0 }
func (stubTaskAdaptor) BuildRequestURL(*relaycommon.RelayInfo) (string, error)         { return "", nil }
func (stubTaskAdaptor) BuildRequestHeader(*gin.Context, *http.Request, *relaycommon.RelayInfo) error {
	return nil
}
func (stubTaskAdaptor) BuildRequestBody(*gin.Context, *relaycommon.RelayInfo) (io.Reader, error) {
	return nil, nil
}
func (stubTaskAdaptor) DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (*http.Response, error) {
	return nil, nil
}
func (stubTaskAdaptor) DoResponse(*gin.Context, *http.Response, *relaycommon.RelayInfo) (string, []byte, *taskdto.TaskError) {
	return "", nil, nil
}
func (stubTaskAdaptor) GetModelList() []string { return nil }
func (stubTaskAdaptor) GetChannelName() string { return "stub" }
func (stubTaskAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return nil, nil
}
func (stubTaskAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) { return nil, nil }

type stubUnitTierAdaptor struct {
	stubTaskAdaptor
}

func (a stubUnitTierAdaptor) EstimateTaskUnitTier(*gin.Context, *relaycommon.RelayInfo) (channel.TaskUnitTierEstimate, error) {
	return a.estimate, a.estErr
}

type countingUnitTierAdaptor struct {
	stubUnitTierAdaptor
	doRequestCount int
}

func (a *countingUnitTierAdaptor) DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (*http.Response, error) {
	a.doRequestCount++
	return nil, nil
}

type failingReserveSettler struct {
	preConsumedQuota int
	reserveCalls     int
}

func (*failingReserveSettler) Settle(int) error    { return nil }
func (*failingReserveSettler) Refund(*gin.Context) {}
func (*failingReserveSettler) NeedsRefund() bool   { return false }
func (s *failingReserveSettler) GetPreConsumedQuota() int {
	return s.preConsumedQuota
}
func (s *failingReserveSettler) Reserve(int) error {
	s.reserveCalls++
	return kittypes.NewErrorWithStatusCode(
		fmt.Errorf("订阅额度不足或未配置订阅: subscription used exceeds total"),
		kittypes.ErrorCodeInsufficientUserQuota,
		http.StatusForbidden,
		kittypes.ErrOptionWithSkipRetry(),
		kittypes.ErrOptionWithNoRecordErrorLog(),
	)
}

func TestApplyTaskSubmitPriceTaskUnitTier(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p_audio":0.9}}`,
	}))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{OriginModelName: "kling-v3"}

	missing := applyTaskSubmitPrice(c, info, stubTaskAdaptor{ratios: map[string]float64{"seconds": 99}}, "kling-v3")
	require.NotNil(t, missing)
	assert.Equal(t, http.StatusBadRequest, missing.StatusCode)
	assert.Equal(t, "task_unit_tier_estimator_missing", missing.Code)

	estFail := applyTaskSubmitPrice(c, info, stubUnitTierAdaptor{stubTaskAdaptor: stubTaskAdaptor{estErr: fmt.Errorf("bad field")}}, "kling-v3")
	require.NotNil(t, estFail)
	assert.Equal(t, "task_unit_tier_estimate_error", estFail.Code)

	noPrice := applyTaskSubmitPrice(c, info, stubUnitTierAdaptor{stubTaskAdaptor: stubTaskAdaptor{estimate: channel.TaskUnitTierEstimate{Units: 5, TierKey: "missing"}}}, "kling-v3")
	require.NotNil(t, noPrice)
	assert.Equal(t, "task_unit_tier_price_error", noPrice.Code)

	ok := applyTaskSubmitPrice(c, info, stubUnitTierAdaptor{
		stubTaskAdaptor: stubTaskAdaptor{
			ratios:   map[string]float64{"seconds": 99},
			estimate: channel.TaskUnitTierEstimate{Units: 5, TierKey: "720p_audio"},
		},
	}, "kling-v3")
	require.Nil(t, ok)
	assert.Equal(t, 2_250_000, info.PriceData.Quota)
	assert.Equal(t, "720p_audio", info.PriceData.TaskUnitTierKey)
	assert.Empty(t, info.PriceData.OtherRatios())
}

func TestApplyTaskSubmitPriceFreeGroupRatioSkipsBilling(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	original := operation_setting.GetQuotaSetting().EnableFreeModelPreConsume
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	t.Cleanup(func() {
		operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = original
	})

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "free")
	info := &relaycommon.RelayInfo{OriginModelName: "kling-v3", UsingGroup: "free"}

	savedRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedRatios))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"free":0,"default":1}`))

	err := applyTaskSubmitPrice(c, info, stubUnitTierAdaptor{
		stubTaskAdaptor: stubTaskAdaptor{
			estimate: channel.TaskUnitTierEstimate{Units: 5, TierKey: "720p"},
		},
	}, "kling-v3")
	require.Nil(t, err)
	assert.True(t, info.PriceData.FreeModel)
	assert.Equal(t, 0, info.PriceData.Quota)
	assert.Equal(t, 0, info.PriceData.QuotaToPreConsume)
}

func TestApplyTaskSubmitPriceKeepsLegacyEstimateBilling(t *testing.T) {
	savedPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"legacy-task":0.02}`))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{OriginModelName: "legacy-task"}
	err := applyTaskSubmitPrice(c, info, stubTaskAdaptor{ratios: map[string]float64{"seconds": 5}}, "legacy-task")
	require.Nil(t, err)
	assert.Equal(t, common.QuotaFromFloat(0.02*common.QuotaPerUnit*5), info.PriceData.Quota)
	assert.Equal(t, 5.0, info.PriceData.OtherRatios()["seconds"])
}

type blockingUnitTierAdaptor struct {
	stubUnitTierAdaptor
	started chan struct{}
	release chan struct{}
}

func (a blockingUnitTierAdaptor) EstimateTaskUnitTier(*gin.Context, *relaycommon.RelayInfo) (channel.TaskUnitTierEstimate, error) {
	select {
	case <-a.started:
	default:
		close(a.started)
	}
	<-a.release
	return a.estimate, a.estErr
}

func TestApplyTaskSubmitPriceCapturedViewIgnoresConcurrentSwap(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{OriginModelName: "kling-v3"}
	adaptor := blockingUnitTierAdaptor{
		stubUnitTierAdaptor: stubUnitTierAdaptor{
			stubTaskAdaptor: stubTaskAdaptor{
				estimate: channel.TaskUnitTierEstimate{Units: 5, TierKey: "720p"},
			},
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}

	done := make(chan *taskdto.TaskError, 1)
	go func() {
		done <- applyTaskSubmitPrice(c, info, adaptor, "kling-v3")
	}()
	<-adaptor.started
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"ratio"}`,
		"billing_setting.task_unit_tier_price": `{}`,
	}))
	close(adaptor.release)
	taskErr := <-done
	require.Nil(t, taskErr)
	assert.Equal(t, "task_unit_tier", info.PriceData.BillingMode)
	assert.Equal(t, 1_500_000, info.PriceData.Quota)
	assert.Equal(t, "720p", info.PriceData.TaskUnitTierKey)
}

func TestAttachVideoTokenUsageSkipsTaskUnitTier(t *testing.T) {
	body, err := common.Marshal(dto.NewOpenAIVideo())
	require.NoError(t, err)
	task := &model.Task{
		Data: []byte(`{"usage":{"total_tokens":40594}}`),
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{BillingMode: "task_unit_tier"},
		},
	}
	got := attachVideoTokenUsage(body, task)
	var video dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(got, &video))
	assert.True(t, video.Usage == nil || video.Usage.TotalTokens == 0)

	task.PrivateData.BillingContext.BillingMode = "video_token"
	got = attachVideoTokenUsage(body, task)
	require.NoError(t, common.Unmarshal(got, &video))
	require.NotNil(t, video.Usage)
	assert.Equal(t, 40594, video.Usage.TotalTokens)
}

func TestRelayTaskSubmitAttemptBlocksUpstreamWhenReservationFails(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6}}`,
	}))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	billing := &failingReserveSettler{preConsumedQuota: 1_000}
	info := &relaycommon.RelayInfo{
		OriginModelName: "kling-v3",
		Billing:         billing,
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	adaptor := &countingUnitTierAdaptor{
		stubUnitTierAdaptor: stubUnitTierAdaptor{
			stubTaskAdaptor: stubTaskAdaptor{
				estimate: channel.TaskUnitTierEstimate{Units: 5, TierKey: "720p"},
			},
		},
	}

	result, taskErr := relayTaskSubmitAttempt(c, info, adaptor, "kling-v3", "")
	require.NotNil(t, taskErr)
	assert.Nil(t, result)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	assert.True(t, taskErr.LocalError)
	assert.Equal(t, 1, billing.reserveCalls)
	assert.Equal(t, 0, adaptor.doRequestCount)
	assert.Equal(t, 1_000, billing.preConsumedQuota)
}

func TestApplyTaskSubmitPriceRejectsMalformedNestedExtInfo(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p":0.6,"720p_audio":0.9,"720p_voice":1.1}}`,
	}))

	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"kling-v3","prompt":"p","size":"720P","audio_generation":true,"ext_info":{"AdditionalParameters":"{\"voice_list\":[{\"voice_id\":1}"}}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "p", Size: "720P"})

	info := &relaycommon.RelayInfo{
		OriginModelName: "kling-v3",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	taskErr := applyTaskSubmitPrice(c, info, &newapi.TaskAdaptor{}, "kling-v3")
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Contains(t, taskErr.Message, "malformed nested JSON")
	assert.Zero(t, info.PriceData.Quota)
	assert.Empty(t, info.PriceData.TaskUnitTierKey)
	assert.Equal(t, 0.0, info.PriceData.TaskUnits)
}

func TestApplyTaskSubmitPriceRejectsVoiceHitWithMalformedExtInfo(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":         `{"kling-v3":"task_unit_tier"}`,
		"billing_setting.task_unit_tier_price": `{"kling-v3":{"720p_voice":1.1}}`,
	}))

	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"kling-v3","prompt":"p","size":"720P","element_voice_id":"voice-1","ext_info":{"AdditionalParameters":"{\"voice_list\":[{\"voice_id\":1}"}}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "p", Size: "720P"})

	info := &relaycommon.RelayInfo{OriginModelName: "kling-v3", ChannelMeta: &relaycommon.ChannelMeta{}}
	taskErr := applyTaskSubmitPrice(c, info, &newapi.TaskAdaptor{}, "kling-v3")
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Contains(t, taskErr.Message, "malformed nested JSON")
	assert.Zero(t, info.PriceData.Quota)
	assert.Empty(t, info.PriceData.TaskUnitTierKey)
	assert.Equal(t, 0.0, info.PriceData.TaskUnits)
}

type succeedingSettler struct {
	preConsumedQuota int
}

func (*succeedingSettler) Settle(int) error    { return nil }
func (*succeedingSettler) Refund(*gin.Context) {}
func (*succeedingSettler) NeedsRefund() bool   { return false }
func (s *succeedingSettler) GetPreConsumedQuota() int {
	return s.preConsumedQuota
}
func (*succeedingSettler) Reserve(int) error { return nil }

func TestRelayTaskSubmitUsesMappedModelForKlingFamily(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	cases := []struct {
		name       string
		origin     string
		mapped     string
		body       string
		priceTable string
		wantTier   string
		wantModel  string
		wantUnits  float64
	}{
		{
			name:       "custom alias maps to v3",
			origin:     "custom-kling",
			mapped:     "kling-v3",
			body:       `{"model":"custom-kling","prompt":"a cat","size":"720p","seconds":"5","audio_generation":true}`,
			priceTable: `{"custom-kling":{"720p_audio":0.9}}`,
			wantTier:   "720p_audio",
			wantModel:  "kling-v3",
			wantUnits:  5,
		},
		{
			name:       "v3 maps to omni ref video",
			origin:     "kling-v3",
			mapped:     "kling-v3-omni",
			body:       `{"model":"kling-v3","prompt":"a cat","size":"720p","seconds":"5","audio_generation":true,"file_infos":[{"Category":"Video","Url":"https://x/v.mp4"}]}`,
			priceTable: `{"kling-v3":{"720p_ref_video":1.2,"720p_audio":0.9}}`,
			wantTier:   "720p_ref_video",
			wantModel:  "kling-v3-omni",
			wantUnits:  5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
				"billing_setting.billing_mode":         fmt.Sprintf(`{"%s":"task_unit_tier"}`, tc.origin),
				"billing_setting.task_unit_tier_price": tc.priceTable,
			}))

			var gotBody map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				_ = common.Unmarshal(data, &gotBody)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"up-1","task_id":"up-1","status":"queued"}`))
			}))
			t.Cleanup(upstream.Close)

			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader([]byte(tc.body)))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeNewAPI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")
			common.SetContextKey(c, constant.ContextKeyOriginalModel, tc.origin)
			c.Set("model_mapping", fmt.Sprintf(`{"%s":"%s"}`, tc.origin, tc.mapped))

			info := &relaycommon.RelayInfo{
				OriginModelName: tc.origin,
				Billing:         &succeedingSettler{preConsumedQuota: 10_000_000},
				IsPlayground:    true,
				TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
			}
			result, taskErr := RelayTaskSubmit(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, result)
			assert.Equal(t, tc.origin, info.OriginModelName)
			assert.Equal(t, tc.mapped, info.UpstreamModelName)
			assert.Equal(t, tc.wantTier, info.PriceData.TaskUnitTierKey)
			assert.Equal(t, tc.wantUnits, info.PriceData.TaskUnits)
			assert.Equal(t, tc.wantModel, gotBody["model"])

			result2, taskErr2 := RelayTaskSubmit(c, info)
			require.Nil(t, taskErr2)
			require.NotNil(t, result2)
			assert.Equal(t, tc.origin, info.OriginModelName)
			assert.Equal(t, tc.mapped, info.UpstreamModelName)
		})
	}
}
