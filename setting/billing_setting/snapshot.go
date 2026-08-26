package billing_setting

import (
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/samber/lo"
)

type billingSnapshot struct {
	BillingMode       map[string]string
	BillingExpr       map[string]string
	VideoTokenPrice   map[string]map[string]float64
	TaskUnitTierPrice map[string]map[string]float64
	revision          uint64
}

type View struct {
	snap *billingSnapshot
}

type Prepared struct {
	snap *billingSnapshot
}

var (
	liveSnapshot     atomic.Pointer[billingSnapshot]
	snapshotRevision atomic.Uint64
)

func emptySnapshot() *billingSnapshot {
	return &billingSnapshot{
		BillingMode:       map[string]string{},
		BillingExpr:       map[string]string{},
		VideoTokenPrice:   map[string]map[string]float64{},
		TaskUnitTierPrice: map[string]map[string]float64{},
	}
}

func currentSnapshot() *billingSnapshot {
	if snap := liveSnapshot.Load(); snap != nil {
		return snap
	}
	return emptySnapshot()
}

func CurrentView() View {
	return View{snap: currentSnapshot()}
}

func (v View) Mode(model string) string {
	if v.snap == nil {
		return BillingModeRatio
	}
	if mode, ok := v.snap.BillingMode[model]; ok && mode != "" {
		return mode
	}
	return BillingModeRatio
}

func (v View) Expr(model string) (string, bool) {
	if v.snap == nil {
		return "", false
	}
	expr, ok := v.snap.BillingExpr[model]
	return expr, ok
}

func (v View) TaskUnitTable(model string) map[string]float64 {
	if v.snap == nil {
		return nil
	}
	table := v.snap.TaskUnitTierPrice[model]
	if len(table) == 0 {
		return nil
	}
	return lo.Assign(table)
}

func (v View) VideoTokenTable(model string) map[string]float64 {
	if v.snap == nil {
		return nil
	}
	table := v.snap.VideoTokenPrice[model]
	if len(table) == 0 {
		return nil
	}
	return lo.Assign(table)
}

func (v View) ModeMap() map[string]string {
	if v.snap == nil {
		return map[string]string{}
	}
	return lo.Assign(v.snap.BillingMode)
}

func (v View) ExprMap() map[string]string {
	if v.snap == nil {
		return map[string]string{}
	}
	return lo.Assign(v.snap.BillingExpr)
}

func (v View) VideoTokenMap() map[string]map[string]float64 {
	if v.snap == nil {
		return map[string]map[string]float64{}
	}
	return cloneNestedFloatMap(v.snap.VideoTokenPrice)
}

func (v View) TaskUnitMap() map[string]map[string]float64 {
	if v.snap == nil {
		return map[string]map[string]float64{}
	}
	return cloneNestedFloatMap(v.snap.TaskUnitTierPrice)
}

func ObserveTaskUnitPair(model string) (mode string, table map[string]float64) {
	view := CurrentView()
	return view.Mode(model), view.TaskUnitTable(model)
}

func cloneSnapshot(src *billingSnapshot) *billingSnapshot {
	if src == nil {
		return emptySnapshot()
	}
	return &billingSnapshot{
		BillingMode:       lo.Assign(src.BillingMode),
		BillingExpr:       lo.Assign(src.BillingExpr),
		VideoTokenPrice:   cloneNestedFloatMap(src.VideoTokenPrice),
		TaskUnitTierPrice: cloneNestedFloatMap(src.TaskUnitTierPrice),
	}
}

func cloneNestedFloatMap(src map[string]map[string]float64) map[string]map[string]float64 {
	out := make(map[string]map[string]float64, len(src))
	for model, table := range src {
		out[model] = lo.Assign(table)
	}
	return out
}

func attachSnapshot(next *billingSnapshot) {
	if next == nil {
		next = emptySnapshot()
	}
	if next.revision == 0 {
		next.revision = snapshotRevision.Add(1)
	}
	liveSnapshot.Store(next)
	billingSetting.BillingMode = next.BillingMode
	billingSetting.BillingExpr = next.BillingExpr
	billingSetting.VideoTokenPrice = next.VideoTokenPrice
	billingSetting.TaskUnitTierPrice = next.TaskUnitTierPrice
}

func publishSnapshot(next *billingSnapshot) {
	attachSnapshot(next)
}

func BuildSnapshotFromUpdates(updates map[string]string) (*billingSnapshot, error) {
	return buildSnapshot(currentSnapshot(), updates, true)
}

func PrepareUpdates(updates map[string]string) (*Prepared, error) {
	next, err := buildSnapshot(currentSnapshot(), updates, true)
	if err != nil {
		return nil, err
	}
	return &Prepared{snap: next}, nil
}

func (p *Prepared) SetRevision(rev uint64) {
	if p == nil || p.snap == nil {
		return
	}
	p.snap.revision = rev
}

func (p *Prepared) Publish() {
	if p == nil {
		publishSnapshot(emptySnapshot())
		return
	}
	publishSnapshot(p.snap)
}

func (p *Prepared) View() View {
	if p == nil || p.snap == nil {
		return CurrentView()
	}
	return View{snap: p.snap}
}

func PublishSnapshot(next *billingSnapshot) {
	publishSnapshot(next)
}

func ApplyBillingUpdates(updates map[string]string) error {
	next, err := buildSnapshot(currentSnapshot(), updates, true)
	if err != nil {
		return err
	}
	publishSnapshot(next)
	return nil
}

func ReplaceFromDB(updates map[string]string) error {
	next, err := buildSnapshot(emptySnapshot(), updates, false)
	if err != nil {
		return err
	}
	warnInconsistentBillingPairs(next)
	publishSnapshot(next)
	return nil
}

func (s *BillingSetting) LoadConfigMap(updates map[string]string) error {
	next, err := buildSnapshot(currentSnapshot(), updates, false)
	if err != nil {
		return err
	}
	publishSnapshot(next)
	return nil
}

func buildSnapshot(base *billingSnapshot, updates map[string]string, strictPair bool) (*billingSnapshot, error) {
	next := cloneSnapshot(base)
	if raw, ok := updates[BillingModeField]; ok {
		modes := map[string]string{}
		if err := common.UnmarshalJsonStr(raw, &modes); err != nil {
			return nil, err
		}
		if err := ValidateBillingModeJSON(raw); err != nil {
			return nil, err
		}
		if modes == nil {
			modes = map[string]string{}
		}
		next.BillingMode = modes
	}
	if raw, ok := updates[BillingExprField]; ok {
		exprs := map[string]string{}
		if err := common.UnmarshalJsonStr(raw, &exprs); err != nil {
			return nil, err
		}
		if exprs == nil {
			exprs = map[string]string{}
		}
		next.BillingExpr = exprs
	}
	if raw, ok := updates[VideoTokenPriceField]; ok {
		tables := map[string]map[string]float64{}
		if err := common.UnmarshalJsonStr(raw, &tables); err != nil {
			return nil, err
		}
		if tables == nil {
			tables = map[string]map[string]float64{}
		}
		next.VideoTokenPrice = tables
	}
	if raw, ok := updates[TaskUnitTierPriceField]; ok {
		tables := map[string]map[string]float64{}
		if err := common.UnmarshalJsonStr(raw, &tables); err != nil {
			return nil, err
		}
		if tables == nil {
			tables = map[string]map[string]float64{}
		}
		next.TaskUnitTierPrice = tables
	}
	if strictPair {
		changed := changedBillingModels(base, next)
		if err := validatePairedBillingMaps(next.BillingMode, next.BillingExpr, next.TaskUnitTierPrice, next.VideoTokenPrice, changed); err != nil {
			return nil, err
		}
	}
	return next, nil
}

func changedBillingModels(base, next *billingSnapshot) map[string]struct{} {
	if base == nil {
		base = emptySnapshot()
	}
	if next == nil {
		next = emptySnapshot()
	}
	names := map[string]struct{}{}
	add := func(m map[string]string) {
		for name := range m {
			names[name] = struct{}{}
		}
	}
	addNested := func(m map[string]map[string]float64) {
		for name := range m {
			names[name] = struct{}{}
		}
	}
	add(base.BillingMode)
	add(next.BillingMode)
	add(base.BillingExpr)
	add(next.BillingExpr)
	addNested(base.VideoTokenPrice)
	addNested(next.VideoTokenPrice)
	addNested(base.TaskUnitTierPrice)
	addNested(next.TaskUnitTierPrice)
	changed := map[string]struct{}{}
	for name := range names {
		if billingEntryChanged(base, next, name) {
			changed[name] = struct{}{}
		}
	}
	return changed
}

func billingEntryChanged(base, next *billingSnapshot, model string) bool {
	if base.BillingMode[model] != next.BillingMode[model] {
		return true
	}
	if base.BillingExpr[model] != next.BillingExpr[model] {
		return true
	}
	if !floatTableEqual(base.VideoTokenPrice[model], next.VideoTokenPrice[model]) {
		return true
	}
	if !floatTableEqual(base.TaskUnitTierPrice[model], next.TaskUnitTierPrice[model]) {
		return true
	}
	return false
}

func floatTableEqual(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		other, ok := b[key]
		if !ok || value != other {
			return false
		}
	}
	return true
}

func warnInconsistentBillingPairs(snap *billingSnapshot) {
	if snap == nil {
		return
	}
	for model := range snap.BillingMode {
		err := validatePairedBillingMaps(
			snap.BillingMode,
			snap.BillingExpr,
			snap.TaskUnitTierPrice,
			snap.VideoTokenPrice,
			map[string]struct{}{model: {}},
		)
		if err != nil {
			common.SysLog("billing_setting: pre-existing " + err.Error())
		}
	}
}
