package billing_setting

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

func IsTaskUnitTierBilling(model string) bool {
	return GetBillingMode(model) == BillingModeTaskUnitTier
}

func GetTaskUnitTierPriceCopy() map[string]map[string]float64 {
	return CurrentView().TaskUnitMap()
}

func GetTaskUnitTierPriceTable(model string) map[string]float64 {
	return CurrentView().TaskUnitTable(model)
}

func LookupTaskUnitTierPrice(model, tierKey string) (float64, error) {
	return CurrentView().LookupTaskUnitTierPrice(model, tierKey)
}

func (v View) LookupTaskUnitTierPrice(model, tierKey string) (float64, error) {
	if strings.TrimSpace(tierKey) == "" {
		return 0, fmt.Errorf("task unit tier key is empty")
	}
	table := v.TaskUnitTable(model)
	if len(table) == 0 {
		return 0, fmt.Errorf("task unit tier price is not configured for model %s", model)
	}
	price, ok := table[tierKey]
	if !ok {
		return 0, fmt.Errorf("task unit tier price is not configured for model %s tier %s", model, tierKey)
	}
	if !isFinitePositivePrice(price) {
		return 0, fmt.Errorf("task unit tier price is invalid for model %s tier %s", model, tierKey)
	}
	return price, nil
}

func HasTaskUnitTierPrice(model string) bool {
	return HasTaskUnitTierPriceFromTable(GetTaskUnitTierPriceTable(model))
}

func ValidateTaskUnitTierPriceJSON(raw string) error {
	var tables map[string]map[string]float64
	if err := common.UnmarshalJsonStr(raw, &tables); err != nil {
		return fmt.Errorf("invalid task_unit_tier_price JSON: %w", err)
	}
	for model, table := range tables {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("task unit tier price has empty model name")
		}
		if len(table) == 0 {
			return fmt.Errorf("task unit tier price for model %s is empty", model)
		}
		for key, price := range table {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("task unit tier price for model %s has empty tier key", model)
			}
			if !isFinitePositivePrice(price) {
				return fmt.Errorf("task unit tier price for model %s tier %s must be a finite positive number", model, key)
			}
		}
	}
	return nil
}

func ValidatePairedBillingSettings(modeJSON, unitJSON, videoJSON, exprJSON string) error {
	if strings.TrimSpace(modeJSON) == "" {
		modeJSON = "{}"
	}
	if strings.TrimSpace(unitJSON) == "" {
		unitJSON = "{}"
	}
	if strings.TrimSpace(videoJSON) == "" {
		videoJSON = "{}"
	}
	if strings.TrimSpace(exprJSON) == "" {
		exprJSON = "{}"
	}
	if err := ValidateBillingModeJSON(modeJSON); err != nil {
		return err
	}
	if err := ValidateTaskUnitTierPriceJSON(unitJSON); err != nil {
		return err
	}
	var modes map[string]string
	if err := common.UnmarshalJsonStr(modeJSON, &modes); err != nil {
		return fmt.Errorf("invalid billing_mode JSON: %w", err)
	}
	if modes == nil {
		modes = map[string]string{}
	}
	var unitTables map[string]map[string]float64
	if err := common.UnmarshalJsonStr(unitJSON, &unitTables); err != nil {
		return fmt.Errorf("invalid task_unit_tier_price JSON: %w", err)
	}
	if unitTables == nil {
		unitTables = map[string]map[string]float64{}
	}
	var videoTables map[string]map[string]float64
	if err := common.UnmarshalJsonStr(videoJSON, &videoTables); err != nil {
		return fmt.Errorf("invalid video_token_price JSON: %w", err)
	}
	if videoTables == nil {
		videoTables = map[string]map[string]float64{}
	}
	var exprs map[string]string
	if err := common.UnmarshalJsonStr(exprJSON, &exprs); err != nil {
		return fmt.Errorf("invalid billing_expr JSON: %w", err)
	}
	if exprs == nil {
		exprs = map[string]string{}
	}
	next := &billingSnapshot{
		BillingMode:       modes,
		BillingExpr:       exprs,
		VideoTokenPrice:   videoTables,
		TaskUnitTierPrice: unitTables,
	}
	changed := changedBillingModels(currentSnapshot(), next)
	return validatePairedBillingMaps(modes, exprs, unitTables, videoTables, changed)
}

func validatePairedBillingMaps(
	modes map[string]string,
	exprs map[string]string,
	unitTables, videoTables map[string]map[string]float64,
	changed map[string]struct{},
) error {
	for model := range changed {
		switch modes[model] {
		case BillingModeTaskUnitTier:
			if !HasTaskUnitTierPriceFromTable(unitTables[model]) {
				return fmt.Errorf("task unit tier price is not configured for model %s", model)
			}
		case BillingModeVideoToken:
			if !HasVideoTokenPriceFromTable(videoTables[model]) {
				return fmt.Errorf("video token price is not configured for model %s", model)
			}
		case BillingModeTieredExpr:
			if strings.TrimSpace(exprs[model]) == "" {
				return fmt.Errorf("billing expression is not configured for model %s", model)
			}
		}
	}
	return nil
}

func HasTaskUnitTierPriceFromTable(table map[string]float64) bool {
	for _, price := range table {
		if isFinitePositivePrice(price) {
			return true
		}
	}
	return false
}

func ValidateBillingModeJSON(raw string) error {
	var modes map[string]string
	if err := common.UnmarshalJsonStr(raw, &modes); err != nil {
		return fmt.Errorf("invalid billing_mode JSON: %w", err)
	}
	for model, mode := range modes {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("billing mode has empty model name")
		}
		switch mode {
		case "", BillingModeRatio, BillingModeTieredExpr, BillingModeVideoToken, BillingModeTaskUnitTier:
		default:
			return fmt.Errorf("unsupported billing mode %q for model %s", mode, model)
		}
	}
	return nil
}

func isFinitePositivePrice(price float64) bool {
	return price > 0 && !math.IsInf(price, 0) && !math.IsNaN(price)
}
