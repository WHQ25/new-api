package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareTaskBillingForSelectedGroupStartsAfterFreeGroup(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)

	const userID = 801
	seedUser(t, userID, 500_000)

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userID,
		IsPlayground:    true,
		OriginModelName: "kling-v3",
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
		PriceData: hosttypes.PriceData{
			Quota:          100_000,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
	}
	ctx, _ := gin.CreateTestContext(nil)

	require.Nil(t, PrepareTaskBillingForSelectedGroup(ctx, relayInfo))
	require.NotNil(t, relayInfo.Billing)
	assert.Equal(t, 100_000, relayInfo.FinalPreConsumedQuota)

	userQuota, err := model.GetUserQuota(userID, false)
	require.NoError(t, err)
	assert.Equal(t, 400_000, userQuota)
}

func TestPrepareTaskBillingForSelectedGroupRaisesReservation(t *testing.T) {
	truncate(t)

	const userID = 802
	seedUser(t, userID, 80_000)

	relayInfo := &relaycommon.RelayInfo{
		UserId:                userID,
		IsPlayground:          true,
		FinalPreConsumedQuota: 50_000,
		PriceData: hosttypes.PriceData{
			Quota:          100_000,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 2},
		},
	}
	session := &BillingSession{
		relayInfo:        relayInfo,
		funding:          &WalletFunding{userId: userID, consumed: 50_000},
		preConsumedQuota: 50_000,
	}
	relayInfo.Billing = session

	require.Nil(t, PrepareTaskBillingForSelectedGroup(nil, relayInfo))
	assert.Equal(t, 100_000, session.GetPreConsumedQuota())
	assert.Equal(t, 100_000, relayInfo.FinalPreConsumedQuota)

	userQuota, err := model.GetUserQuota(userID, false)
	require.NoError(t, err)
	assert.Equal(t, 30_000, userQuota)
}

func TestPrepareTaskBillingForSelectedGroupWalletRaiseInsufficientBlocks(t *testing.T) {
	truncate(t)

	const userID = 804
	seedUser(t, userID, 20_000)

	relayInfo := &relaycommon.RelayInfo{
		UserId:                userID,
		IsPlayground:          true,
		FinalPreConsumedQuota: 50_000,
		PriceData: hosttypes.PriceData{
			Quota: 100_000,
		},
	}
	session := &BillingSession{
		relayInfo:        relayInfo,
		funding:          &WalletFunding{userId: userID, consumed: 50_000},
		preConsumedQuota: 50_000,
	}
	relayInfo.Billing = session

	apiErr := PrepareTaskBillingForSelectedGroup(nil, relayInfo)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Equal(t, 50_000, session.GetPreConsumedQuota())

	userQuota, err := model.GetUserQuota(userID, false)
	require.NoError(t, err)
	assert.Equal(t, 20_000, userQuota)
}

func TestPrepareTaskBillingForSelectedGroupCheaperGroupKeepsReserve(t *testing.T) {
	billing := &recordingBillingSettler{preConsumedQuota: 80_000}
	relayInfo := &relaycommon.RelayInfo{
		Billing:               billing,
		FinalPreConsumedQuota: 80_000,
		PriceData: hosttypes.PriceData{
			Quota: 20_000,
		},
	}

	require.Nil(t, PrepareTaskBillingForSelectedGroup(nil, relayInfo))
	assert.Empty(t, billing.reserveTargets)
	assert.Equal(t, 80_000, billing.preConsumedQuota)
	assert.Equal(t, 80_000, relayInfo.FinalPreConsumedQuota)
}

func TestPrepareTaskBillingForSelectedGroupSubscriptionInsufficientBlocks(t *testing.T) {
	truncate(t)

	const userID, subID = 803, 803
	seedUser(t, userID, 0)
	seedSubscription(t, subID, userID, 5_000, 2_000)

	relayInfo := &relaycommon.RelayInfo{
		UserId:                userID,
		IsPlayground:          true,
		FinalPreConsumedQuota: 2_000,
		PriceData: hosttypes.PriceData{
			Quota: 8_000,
		},
	}
	session := &BillingSession{
		relayInfo: relayInfo,
		funding: &SubscriptionFunding{
			subscriptionId: subID,
			preConsumed:    2_000,
			amount:         2_000,
		},
		preConsumedQuota: 2_000,
	}
	relayInfo.Billing = session

	apiErr := PrepareTaskBillingForSelectedGroup(nil, relayInfo)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	assert.Equal(t, 2_000, session.GetPreConsumedQuota())

	var sub model.UserSubscription
	require.NoError(t, model.DB.First(&sub, subID).Error)
	assert.Equal(t, int64(2_000), sub.AmountUsed)
}

func TestPrepareTaskBillingForSelectedGroupSkipsFreeFirstAttempt(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{
		PriceData: hosttypes.PriceData{FreeModel: true, Quota: 0},
	}
	require.Nil(t, PrepareTaskBillingForSelectedGroup(nil, relayInfo))
	assert.Nil(t, relayInfo.Billing)
}

func TestPrepareTaskBillingForSelectedGroupPaidToFreeKeepsReservation(t *testing.T) {
	billing := &recordingBillingSettler{preConsumedQuota: 80_000}
	relayInfo := &relaycommon.RelayInfo{
		Billing:               billing,
		FinalPreConsumedQuota: 80_000,
		PriceData: hosttypes.PriceData{
			Quota:     0,
			FreeModel: true,
		},
	}
	require.Nil(t, PrepareTaskBillingForSelectedGroup(nil, relayInfo))
	assert.Empty(t, billing.reserveTargets)
	assert.Equal(t, 80_000, billing.preConsumedQuota)
	assert.Equal(t, 80_000, relayInfo.FinalPreConsumedQuota)
	assert.NotNil(t, relayInfo.Billing)
}
