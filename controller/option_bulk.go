package controller

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/gin-gonic/gin"
)

type OptionBulkUpdateRequest struct {
	Options map[string]any `json:"options"`
}

var bulkPricingOptionKeys = map[string]struct{}{
	"ModelPrice":                           {},
	"ModelRatio":                           {},
	"CacheRatio":                           {},
	"CreateCacheRatio":                     {},
	"CompletionRatio":                      {},
	"ImageRatio":                           {},
	"AudioRatio":                           {},
	"AudioCompletionRatio":                 {},
	"ExposeRatioEnabled":                   {},
	"billing_setting.billing_mode":         {},
	"billing_setting.billing_expr":         {},
	"billing_setting.video_token_price":    {},
	"billing_setting.task_unit_tier_price": {},
}

func UpdateOptions(c *gin.Context) {
	var req OptionBulkUpdateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || len(req.Options) == 0 {
		c.JSON(200, gin.H{"success": false, "message": "无效的参数"})
		return
	}

	prepared := make(map[string]string, len(req.Options))
	for key, raw := range req.Options {
		if _, ok := bulkPricingOptionKeys[key]; !ok {
			c.JSON(200, gin.H{"success": false, "message": fmt.Sprintf("unsupported bulk option key: %s", key)})
			return
		}
		value := stringifyOptionValue(raw)
		if reason := pricingOptionRejectReason(key, value); reason != "" {
			c.JSON(200, gin.H{"success": false, "message": reason})
			return
		}
		prepared[key] = value
	}

	_, hasMode := prepared["billing_setting.billing_mode"]
	_, hasUnit := prepared["billing_setting.task_unit_tier_price"]
	_, hasVideo := prepared["billing_setting.video_token_price"]
	_, hasExpr := prepared["billing_setting.billing_expr"]
	if hasMode || hasUnit || hasVideo || hasExpr {
		modeJSON := optionOrCurrent(prepared, "billing_setting.billing_mode", mustJSON(billing_setting.GetBillingModeCopy()))
		unitJSON := optionOrCurrent(prepared, "billing_setting.task_unit_tier_price", mustJSON(billing_setting.GetTaskUnitTierPriceCopy()))
		videoJSON := optionOrCurrent(prepared, "billing_setting.video_token_price", mustJSON(billing_setting.GetVideoTokenPriceCopy()))
		exprJSON := optionOrCurrent(prepared, "billing_setting.billing_expr", mustJSON(billing_setting.GetBillingExprCopy()))
		if err := billing_setting.ValidatePairedBillingSettings(modeJSON, unitJSON, videoJSON, exprJSON); err != nil {
			c.JSON(200, gin.H{"success": false, "message": err.Error()})
			return
		}
	}

	if err := model.UpdateOptionsBulk(prepared); err != nil {
		common.ApiError(c, err)
		return
	}
	keys := make([]string, 0, len(prepared))
	for key := range prepared {
		keys = append(keys, key)
	}
	recordManageAudit(c, "option.bulk_update", map[string]interface{}{"keys": keys})
	c.JSON(200, gin.H{"success": true, "message": ""})
}

func stringifyOptionValue(value any) string {
	switch v := value.(type) {
	case bool:
		return common.Interface2String(v)
	case float64:
		return common.Interface2String(v)
	case int:
		return common.Interface2String(v)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", value)
	}
}

func optionOrCurrent(updates map[string]string, key, current string) string {
	if value, ok := updates[key]; ok {
		return value
	}
	if strings.TrimSpace(current) == "" {
		return "{}"
	}
	return current
}

func mustJSON(value any) string {
	if value == nil {
		return "{}"
	}
	raw, err := common.Marshal(value)
	if err != nil || len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func pricingOptionRejectReason(key, value string) string {
	switch key {
	case "billing_setting.billing_mode":
		if err := billing_setting.ValidateBillingModeJSON(value); err != nil {
			return err.Error()
		}
	case "billing_setting.task_unit_tier_price":
		if err := billing_setting.ValidateTaskUnitTierPriceJSON(value); err != nil {
			return err.Error()
		}
	case "ModelPrice", "ModelRatio", "CacheRatio", "CreateCacheRatio", "CompletionRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio":
		var parsed map[string]float64
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return err.Error()
		}
	case "billing_setting.billing_expr", "billing_setting.video_token_price":
		var parsed map[string]any
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return err.Error()
		}
	case "ExposeRatioEnabled":
		if value != "true" && value != "false" {
			return "ExposeRatioEnabled must be true or false"
		}
	}
	return ""
}
