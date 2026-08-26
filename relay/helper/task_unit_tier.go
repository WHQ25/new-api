package helper

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func ModelPriceHelperTaskUnitTier(c *gin.Context, info *relaycommon.RelayInfo, view billing_setting.View, units float64, tierKey string) (hosttypes.PriceData, error) {
	if units <= 0 || math.IsNaN(units) || math.IsInf(units, 0) {
		return hosttypes.PriceData{}, fmt.Errorf("task unit tier units must be a finite positive number")
	}
	if tierKey == "" {
		return hosttypes.PriceData{}, fmt.Errorf("task unit tier key is empty")
	}
	groupRatioInfo := HandleGroupRatio(c, info)
	price, err := view.LookupTaskUnitTierPrice(info.OriginModelName, tierKey)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	quotaFloat := units * price * common.QuotaPerUnit * groupRatioInfo.GroupRatio
	quota, err := common.QuotaFromFloatStrict(quotaFloat)
	if err != nil {
		return hosttypes.PriceData{}, err
	}
	quota, freeModel := applyFreeGroupQuota(groupRatioInfo.GroupRatio, quota)

	return hosttypes.PriceData{
		FreeModel:         freeModel,
		ModelPrice:        price,
		UsePrice:          false,
		Quota:             quota,
		GroupRatioInfo:    groupRatioInfo,
		BillingMode:       billing_setting.BillingModeTaskUnitTier,
		TaskUnitTierKey:   tierKey,
		TaskUnitPrice:     price,
		TaskUnits:         units,
		QuotaToPreConsume: quota,
	}, nil
}
