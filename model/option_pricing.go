package model

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

// pricingWriteMu serializes in-process pricing writers (prepare → DB commit → publish).
// Multi-process deployments rely on SyncOptions reloading from DB.
var pricingWriteMu sync.Mutex
var appliedPricingRevision atomic.Uint64
var pricingRevisionCounter atomic.Uint64
var pricingPersistHook func(*gorm.DB, map[string]string) error
var pricingAfterCommit func()

var modelKeyedPricingOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
	"billing_setting.billing_mode",
	"billing_setting.billing_expr",
	"billing_setting.video_token_price",
	"billing_setting.task_unit_tier_price",
}

type preparedPricingApply struct {
	values   map[string]string
	rest     map[string]string
	prepared *billing_setting.Prepared
	revision uint64
}

func nextPricingRevision() uint64 {
	return pricingRevisionCounter.Add(1)
}

func splitBillingOptions(values map[string]string) (map[string]string, map[string]string) {
	billing := make(map[string]string)
	rest := make(map[string]string)
	for key, value := range values {
		if strings.HasPrefix(key, "billing_setting.") {
			billing[strings.TrimPrefix(key, "billing_setting.")] = value
			continue
		}
		rest[key] = value
	}
	return billing, rest
}

func persistOptionsTx(tx *gorm.DB, values map[string]string) error {
	if pricingPersistHook != nil {
		if err := pricingPersistHook(tx, values); err != nil {
			return err
		}
	}
	for key, value := range values {
		option := Option{Key: key}
		if err := tx.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
			return err
		}
		option.Value = value
		if err := tx.Save(&option).Error; err != nil {
			return err
		}
	}
	return nil
}

func IsModelPricingOptionKey(key string) bool {
	for _, allowed := range modelKeyedPricingOptionKeys {
		if key == allowed {
			return true
		}
	}
	return false
}

func EnsureModelPricingOptions(options map[string]string) error {
	for key := range options {
		if !IsModelPricingOptionKey(key) {
			return fmt.Errorf("unsupported pricing option key: %s", key)
		}
	}
	return nil
}

func SetPricingPersistHook(hook func(*gorm.DB, map[string]string) error) {
	pricingPersistHook = hook
}

func SetPricingAfterCommit(hook func()) {
	pricingAfterCommit = hook
}

func validatePricingJSON(key, value string) error {
	switch key {
	case "ModelPrice", "ModelRatio", "CacheRatio", "CreateCacheRatio", "CompletionRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio", "GroupRatio":
		parsed := map[string]float64{}
		if err := common.Unmarshal([]byte(value), &parsed); err != nil {
			return err
		}
	case "billing_setting.billing_mode":
		return billing_setting.ValidateBillingModeJSON(value)
	case "billing_setting.task_unit_tier_price":
		return billing_setting.ValidateTaskUnitTierPriceJSON(value)
	case "billing_setting.billing_expr":
		parsed := map[string]string{}
		if err := common.Unmarshal([]byte(value), &parsed); err != nil {
			return err
		}
	case "billing_setting.video_token_price":
		parsed := map[string]map[string]float64{}
		if err := common.Unmarshal([]byte(value), &parsed); err != nil {
			return err
		}
	}
	return nil
}

func preparePricingApply(values map[string]string) (*preparedPricingApply, error) {
	if values == nil {
		values = map[string]string{}
	}
	for key, value := range values {
		if err := validateOptionValue(key, value); err != nil {
			return nil, err
		}
		if err := validatePricingJSON(key, value); err != nil {
			return nil, err
		}
	}
	billingUpdates, rest := splitBillingOptions(values)
	out := &preparedPricingApply{
		values: values,
		rest:   rest,
	}
	if len(billingUpdates) > 0 {
		prepared, err := billing_setting.PrepareUpdates(billingUpdates)
		if err != nil {
			return nil, err
		}
		out.prepared = prepared
	}
	return out, nil
}

func assignPreparedRevision(prepared *preparedPricingApply) {
	prepared.revision = nextPricingRevision()
	if prepared.prepared != nil {
		prepared.prepared.SetRevision(prepared.revision)
	}
}

func applyPrepared(prepared *preparedPricingApply) {
	for {
		current := appliedPricingRevision.Load()
		if prepared.revision < current {
			return
		}
		if appliedPricingRevision.CompareAndSwap(current, prepared.revision) {
			break
		}
	}
	if prepared.prepared != nil {
		prepared.prepared.Publish()
		InvalidatePricingCache()
		ratio_setting.InvalidateExposedDataCache()
		common.OptionMapRWMutex.Lock()
		if common.OptionMap == nil {
			common.OptionMap = make(map[string]string)
		}
		for key, value := range prepared.values {
			if strings.HasPrefix(key, "billing_setting.") {
				common.OptionMap[key] = value
			}
		}
		common.OptionMapRWMutex.Unlock()
	}
	for key, value := range prepared.rest {
		if err := updateOptionMap(key, value); err != nil {
			common.SysError("failed to apply option " + key + ": " + err.Error())
		}
	}
}

func publishPreparedLocked(prepared *preparedPricingApply) {
	assignPreparedRevision(prepared)
	if hook := pricingAfterCommit; hook != nil {
		pricingWriteMu.Unlock()
		func() {
			defer pricingWriteMu.Lock()
			hook()
		}()
	}
	applyPrepared(prepared)
}

func UpdateOptionsBulk(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	pricingWriteMu.Lock()
	defer pricingWriteMu.Unlock()
	prepared, err := preparePricingApply(values)
	if err != nil {
		return err
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		return persistOptionsTx(tx, prepared.values)
	})
	if err != nil {
		return err
	}
	publishPreparedLocked(prepared)
	return nil
}

func applyOptionsFromDatabase() {
	pricingWriteMu.Lock()
	defer pricingWriteMu.Unlock()
	options, err := AllOption()
	if err != nil {
		common.SysLog("failed to load options from database: " + err.Error())
		return
	}
	values := make(map[string]string, len(options))
	for _, option := range options {
		if option == nil {
			continue
		}
		values[option.Key] = option.Value
	}
	billingUpdates, rest := splitBillingOptions(values)
	if err := billing_setting.ReplaceFromDB(billingUpdates); err != nil {
		common.SysError("failed to load billing snapshot: " + err.Error())
	} else {
		common.OptionMapRWMutex.Lock()
		if common.OptionMap == nil {
			common.OptionMap = make(map[string]string)
		}
		for key, value := range values {
			if strings.HasPrefix(key, "billing_setting.") {
				common.OptionMap[key] = value
			}
		}
		common.OptionMapRWMutex.Unlock()
		InvalidatePricingCache()
		ratio_setting.InvalidateExposedDataCache()
		appliedPricingRevision.Store(nextPricingRevision())
	}
	for key, value := range rest {
		if err := updateOptionMap(key, value); err != nil {
			common.SysLog("failed to update option map: " + err.Error())
		}
	}
}

func currentModelPricingOptions() map[string]string {
	return map[string]string{
		"ModelPrice":                           ratio_setting.ModelPrice2JSONString(),
		"ModelRatio":                           ratio_setting.ModelRatio2JSONString(),
		"CompletionRatio":                      ratio_setting.CompletionRatio2JSONString(),
		"CacheRatio":                           ratio_setting.CacheRatio2JSONString(),
		"CreateCacheRatio":                     ratio_setting.CreateCacheRatio2JSONString(),
		"ImageRatio":                           ratio_setting.ImageRatio2JSONString(),
		"AudioRatio":                           ratio_setting.AudioRatio2JSONString(),
		"AudioCompletionRatio":                 ratio_setting.AudioCompletionRatio2JSONString(),
		"billing_setting.billing_mode":         marshalOptionJSON(billing_setting.GetBillingModeCopy()),
		"billing_setting.billing_expr":         marshalOptionJSON(billing_setting.GetBillingExprCopy()),
		"billing_setting.video_token_price":    marshalOptionJSON(billing_setting.GetVideoTokenPriceCopy()),
		"billing_setting.task_unit_tier_price": marshalOptionJSON(billing_setting.GetTaskUnitTierPriceCopy()),
	}
}

func marshalOptionJSON(value any) string {
	raw, err := common.Marshal(value)
	if err != nil || len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func jsonValueEqual(a, b any) bool {
	left, err := common.Marshal(a)
	if err != nil {
		return false
	}
	right, err := common.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

func renameJSONMapKey(raw, oldName, newName string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	obj := map[string]any{}
	if err := common.UnmarshalJsonStr(raw, &obj); err != nil {
		return "", err
	}
	if obj == nil {
		obj = map[string]any{}
	}
	if oldName == newName {
		encoded, err := common.Marshal(obj)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
	dest, destExists := obj[newName]
	src, srcExists := obj[oldName]
	if destExists {
		if !srcExists || !jsonValueEqual(src, dest) {
			return "", fmt.Errorf("pricing conflict renaming %q to %q", oldName, newName)
		}
		delete(obj, oldName)
	} else if srcExists {
		obj[newName] = src
		delete(obj, oldName)
	}
	encoded, err := common.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func jsonObjectHasKey(raw, name string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	obj := map[string]any{}
	if err := common.UnmarshalJsonStr(raw, &obj); err != nil || obj == nil {
		return false
	}
	_, ok := obj[name]
	return ok
}

func overlayOrLiveHasNamedFloat(resolved map[string]string, optionKey, name string, liveHas func() bool) bool {
	raw, inOverlay := resolved[optionKey]
	if inOverlay {
		return jsonObjectHasKey(raw, name)
	}
	return liveHas()
}

func overlayModelIsPriced(name string, resolved map[string]string, view billing_setting.View) bool {
	if overlayOrLiveHasNamedFloat(resolved, "ModelPrice", name, func() bool {
		_, ok := ratio_setting.GetModelPrice(name, false)
		return ok
	}) {
		return true
	}
	if overlayOrLiveHasNamedFloat(resolved, "ModelRatio", name, func() bool {
		_, ok := ratio_setting.GetModelRatioCopy()[name]
		return ok
	}) {
		return true
	}
	switch view.Mode(name) {
	case billing_setting.BillingModeVideoToken:
		return billing_setting.HasVideoTokenPriceFromTable(view.VideoTokenTable(name))
	case billing_setting.BillingModeTaskUnitTier:
		return billing_setting.HasTaskUnitTierPriceFromTable(view.TaskUnitTable(name))
	case billing_setting.BillingModeTieredExpr:
		expr, ok := view.Expr(name)
		return ok && strings.TrimSpace(expr) != ""
	default:
		return false
	}
}

func isPricedProtectedBilling(view billing_setting.View, name string) bool {
	switch view.Mode(name) {
	case billing_setting.BillingModeVideoToken:
		return billing_setting.HasVideoTokenPriceFromTable(view.VideoTokenTable(name))
	case billing_setting.BillingModeTieredExpr:
		expr, ok := view.Expr(name)
		return ok && strings.TrimSpace(expr) != ""
	default:
		return false
	}
}

func rejectProtectedBillingDowngrade(oldName, newName string, resolved map[string]string, prepared *preparedPricingApply) error {
	if strings.TrimSpace(oldName) == "" {
		return nil
	}
	dest := newName
	if strings.TrimSpace(dest) == "" {
		dest = oldName
	}
	current := billing_setting.CurrentView()
	if !isPricedProtectedBilling(current, oldName) {
		return nil
	}
	view := current
	if prepared != nil && prepared.prepared != nil {
		view = prepared.prepared.View()
	}
	if overlayModelIsPriced(dest, resolved, view) {
		return nil
	}
	return fmt.Errorf("cannot downgrade model %s from %s to an unpriced state", dest, current.Mode(oldName))
}

func resolvePricingOptions(oldName, newName string, overlay map[string]string) (map[string]string, error) {
	if overlay == nil {
		overlay = map[string]string{}
	}
	if oldName == "" || newName == "" || oldName == newName {
		return overlay, nil
	}
	merged := currentModelPricingOptions()
	for _, key := range modelKeyedPricingOptionKeys {
		renamed, err := renameJSONMapKey(merged[key], oldName, newName)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		merged[key] = renamed
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged, nil
}
