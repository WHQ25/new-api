package billing_setting

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/samber/lo"
)

// ErrVideoTokenResolutionRequired is returned when a video_token request has no
// structured resolution. Prompt flags such as "--resolution 480p" are ignored.
var ErrVideoTokenResolutionRequired = errors.New("metadata.resolution is required")

const (
	VideoTokenTier480p  = "480p"
	VideoTokenTier720p  = "720p"
	VideoTokenTier1080p = "1080p"
	VideoTokenTier4K    = "4k"
)

// Billing units for a video tariff table.
//
// VideoTokenUnitToken bills (pixel-formula tokens / 1M) * price, the original
// Seedance meter. VideoTokenUnitSecond bills requested seconds * price, which is
// how Kling, Wan, Vidu, MiniMax and Sora publish their video prices.
const (
	VideoTokenUnitToken  = "per_token"
	VideoTokenUnitSecond = "per_second"

	// videoTokenSecondPrefix marks every cell of a per-second tariff table. The
	// unit lives inside the key on purpose: the table travels through pricing
	// sync, the model pricing API and the admin UI as a plain
	// map[string]float64, so a separate unit field could desync from the prices
	// it applies to and bill ¥0.6/second as ¥0.6 per 1M tokens. A node that does
	// not understand the prefix misses the lookup and rejects the request
	// instead of undercharging by six orders of magnitude.
	videoTokenSecondPrefix = "sec:"
)

// Variant flags qualify a tariff row beyond its resolution. They are independent
// dimensions appended to the tier in this exact order, so "1080p" with
// {video, audio} is the cell "sec:1080p_video_audio".
const (
	VideoTokenVariantVideo = "video" // request carries video input (Seedance)
	VideoTokenVariantAudio = "audio" // request asks for generated audio (Kling)
)

var videoTokenTiers = []string{
	VideoTokenTier480p,
	VideoTokenTier720p,
	VideoTokenTier1080p,
	VideoTokenTier4K,
}

var videoTokenVariants = []string{
	VideoTokenVariantVideo,
	VideoTokenVariantAudio,
}

// VideoTokenTariff is the tariff cell a request resolved to.
type VideoTokenTariff struct {
	Unit  string  // per_token / per_second
	Key   string  // the matched cell, e.g. "720p_video" or "sec:1080p_audio"
	Price float64 // USD per 1M tokens (per_token) or USD per second (per_second)
}

// VideoTokenPriceKey builds the tariff cell key for a unit, resolution tier and
// variant set. Unknown variants are dropped rather than producing a key nothing
// can match.
func VideoTokenPriceKey(unit, resolution string, variants []string) (string, error) {
	tier, err := ParseVideoTokenTier(resolution)
	if err != nil {
		return "", err
	}
	key := tier
	for _, variant := range videoTokenVariants {
		if lo.Contains(variants, variant) {
			key += "_" + variant
		}
	}
	if unit == VideoTokenUnitSecond {
		key = videoTokenSecondPrefix + key
	}
	return key, nil
}

// ParseVideoTokenTier maps known resolution labels onto the four tariff rows.
// Empty/missing resolution is rejected so billing cannot silently default to 720p.
func ParseVideoTokenTier(resolution string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "":
		return "", ErrVideoTokenResolutionRequired
	case "720p", "720", "hd":
		return VideoTokenTier720p, nil
	case "480p", "480", "sd":
		return VideoTokenTier480p, nil
	case "1080p", "1080", "fhd":
		return VideoTokenTier1080p, nil
	case "4k", "2160p", "2160", "uhd":
		return VideoTokenTier4K, nil
	default:
		return "", fmt.Errorf("unsupported video resolution: %s", resolution)
	}
}

// NormalizeVideoTokenTier is the best-effort form used by display/meter
// helpers that already validated the label.
func NormalizeVideoTokenTier(resolution string) string {
	tier, err := ParseVideoTokenTier(resolution)
	if err != nil {
		return VideoTokenTier720p
	}
	return tier
}

func IsVideoTokenBilling(model string) bool {
	return GetBillingMode(model) == BillingModeVideoToken
}

func GetVideoTokenPriceCopy() map[string]map[string]float64 {
	src := billingSetting.VideoTokenPrice
	if len(src) == 0 {
		return map[string]map[string]float64{}
	}
	out := make(map[string]map[string]float64, len(src))
	for model, table := range src {
		out[model] = lo.Assign(table)
	}
	return out
}

func GetVideoTokenPriceTable(model string) map[string]float64 {
	if table, ok := billingSetting.VideoTokenPrice[model]; ok && len(table) > 0 {
		return lo.Assign(table)
	}
	return nil
}

// GetVideoTokenUnit reports how a model's tariff table meters usage. Tables
// written before per-second billing existed carry no prefix and keep the
// original per-1M-token meaning.
func GetVideoTokenUnit(model string) string {
	unit, _ := videoTokenTableUnit(GetVideoTokenPriceTable(model))
	return unit
}

// LookupVideoTokenPrice resolves the tariff cell for a request. Missing,
// non-positive and non-finite prices are an error so callers reject the request
// instead of silently undercharging.
//
// Variants are intersected with the ones the table actually prices: a model
// whose table has no "_audio" cell (Wan bills resolution only, Seedance only
// distinguishes video input) must not be pushed onto a key it never configured.
// Beyond that intersection the match is exact — a configured dimension with a
// missing cell fails the request rather than falling back to a cheaper row.
func LookupVideoTokenPrice(model, resolution string, variants []string) (VideoTokenTariff, error) {
	table := GetVideoTokenPriceTable(model)
	unit, mixed := videoTokenTableUnit(table)
	key, err := VideoTokenPriceKey(unit, resolution, videoTokenPricedVariants(table, variants))
	if err != nil {
		return VideoTokenTariff{}, err
	}
	if len(table) == 0 {
		return VideoTokenTariff{Unit: unit, Key: key}, fmt.Errorf("video token price is not configured for model %s", model)
	}
	// 一张表只能有一个计量单位。混着 sec: 与无前缀两种 key 时，这个节点读到的档位和另一个
	// 版本的节点读到的可能不是同一行，$/秒 与 $/百万 token 差六个数量级；宁可整表拒绝。
	if mixed {
		return VideoTokenTariff{Unit: unit, Key: key}, fmt.Errorf("video token price for model %s mixes per-second and per-token tiers", model)
	}
	price, ok := table[key]
	if !ok || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return VideoTokenTariff{Unit: unit, Key: key}, fmt.Errorf("video token price is not configured for model %s tier %s", model, key)
	}
	return VideoTokenTariff{Unit: unit, Key: key, Price: price}, nil
}

func HasVideoTokenPrice(model string) bool {
	table := GetVideoTokenPriceTable(model)
	for _, price := range table {
		if price > 0 {
			return true
		}
	}
	return false
}

func VideoTokenTiers() []string {
	return append([]string(nil), videoTokenTiers...)
}

func VideoTokenVariants() []string {
	return append([]string(nil), videoTokenVariants...)
}

// videoTokenTableUnit reports the unit a table meters in, plus whether it mixes
// both units — a configuration a caller must reject rather than resolve.
func videoTokenTableUnit(table map[string]float64) (unit string, mixed bool) {
	perSecond, perToken := false, false
	for key := range table {
		if strings.HasPrefix(key, videoTokenSecondPrefix) {
			perSecond = true
		} else {
			perToken = true
		}
	}
	if perSecond {
		return VideoTokenUnitSecond, perToken
	}
	return VideoTokenUnitToken, false
}

// videoTokenPricedVariants keeps only the variants that appear somewhere in the
// table, so a request signal the operator never priced cannot invent a cell key.
// VideoTokenPricedVariants 返回模型价目表实际配了价的变体维度。
//
// 适配器在认不出上游契约时用它判断某个维度是不是真的参与计费：没配价的维度不会影响
// 档位（查表时会被变体交集去掉），也就不值得为它改写请求或拒绝请求。
func VideoTokenPricedVariants(model string) []string {
	return videoTokenPricedVariants(GetVideoTokenPriceTable(model), videoTokenVariants)
}

func videoTokenPricedVariants(table map[string]float64, variants []string) []string {
	if len(table) == 0 || len(variants) == 0 {
		return nil
	}
	priced := make([]string, 0, len(variants))
	for _, variant := range variants {
		suffix := "_" + variant
		for key := range table {
			if strings.HasSuffix(key, suffix) || strings.Contains(key, suffix+"_") {
				priced = append(priced, variant)
				break
			}
		}
	}
	return priced
}
