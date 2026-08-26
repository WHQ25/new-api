package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

func UpsertModelWithOptions(m *Model, options map[string]string) error {
	if m == nil {
		return errors.New("model is nil")
	}
	if m.ModelName == "" {
		return errors.New("模型名称不能为空")
	}
	if options == nil {
		options = map[string]string{}
	}
	if err := EnsureModelPricingOptions(options); err != nil {
		return err
	}

	pricingWriteMu.Lock()
	defer pricingWriteMu.Unlock()

	oldName := ""
	if m.Id != 0 {
		var existing Model
		if err := DB.First(&existing, m.Id).Error; err != nil {
			return err
		}
		oldName = existing.ModelName
	}
	resolved, err := resolvePricingOptions(oldName, m.ModelName, options)
	if err != nil {
		return err
	}
	prepared, err := preparePricingApply(resolved)
	if err != nil {
		return err
	}
	if err := rejectProtectedBillingDowngrade(oldName, m.ModelName, resolved, prepared); err != nil {
		return err
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		dup, err := IsModelNameDuplicatedTx(tx, m.Id, m.ModelName)
		if err != nil {
			return err
		}
		if dup {
			return fmt.Errorf("模型名称已存在")
		}
		if m.Id == 0 {
			if err := m.InsertTx(tx); err != nil {
				return err
			}
		} else {
			if err := m.UpdateTx(tx); err != nil {
				return err
			}
		}
		return persistOptionsTx(tx, prepared.values)
	})
	if err != nil {
		return err
	}
	publishPreparedLocked(prepared)
	RefreshPricing()
	return nil
}
