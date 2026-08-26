package billing_setting

import (
	"fmt"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio        = "ratio"
	BillingModeTieredExpr   = "tiered_expr"
	BillingModeVideoToken   = "video_token"
	BillingModeTaskUnitTier = "task_unit_tier"
	BillingModeField        = "billing_mode"
	BillingExprField        = "billing_expr"
	VideoTokenPriceField    = "video_token_price"
	TaskUnitTierPriceField  = "task_unit_tier_price"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr,
// billing_setting.video_token_price, billing_setting.task_unit_tier_price
type BillingSetting struct {
	BillingMode       map[string]string             `json:"billing_mode"`
	BillingExpr       map[string]string             `json:"billing_expr"`
	VideoTokenPrice   map[string]map[string]float64 `json:"video_token_price"`
	TaskUnitTierPrice map[string]map[string]float64 `json:"task_unit_tier_price"`
}

var billingSetting = BillingSetting{
	BillingMode:       make(map[string]string),
	BillingExpr:       make(map[string]string),
	VideoTokenPrice:   make(map[string]map[string]float64),
	TaskUnitTierPrice: make(map[string]map[string]float64),
}

func init() {
	publishSnapshot(emptySnapshot())
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	return CurrentView().Mode(model)
}

func GetBillingExpr(model string) (string, bool) {
	return CurrentView().Expr(model)
}

func GetBillingModeCopy() map[string]string {
	return CurrentView().ModeMap()
}

func GetBillingExprCopy() map[string]string {
	return CurrentView().ExprMap()
}

func GetPricingSyncData(base map[string]any) map[string]any {
	view := CurrentView()
	extra := make(map[string]any, 4)
	if modes := view.ModeMap(); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := view.ExprMap(); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	if prices := view.VideoTokenMap(); len(prices) > 0 {
		extra[VideoTokenPriceField] = prices
	}
	if prices := view.TaskUnitMap(); len(prices) > 0 {
		extra[TaskUnitTierPriceField] = prices
	}
	return lo.Assign(base, extra)
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
		}
	}
	return nil
}
