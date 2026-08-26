package billing_setting

import (
	"errors"
	"fmt"
	"strings"
)

// ErrVideoTokenResolutionRequired is returned when a video_token request has no
// structured resolution. Prompt flags such as "--resolution 480p" are ignored.
var ErrVideoTokenResolutionRequired = errors.New("metadata.resolution is required")

const (
	VideoTokenTier480p  = "480p"
	VideoTokenTier720p  = "720p"
	VideoTokenTier1080p = "1080p"
	VideoTokenTier4K    = "4k"

	VideoTokenTierSuffixVideo = "_video"
)

var videoTokenTiers = []string{
	VideoTokenTier480p,
	VideoTokenTier720p,
	VideoTokenTier1080p,
	VideoTokenTier4K,
}

// VideoTokenPriceKey returns the tariff cell key for a resolution tier and
// whether the request includes video input.
func VideoTokenPriceKey(resolution string, hasVideo bool) (string, error) {
	tier, err := ParseVideoTokenTier(resolution)
	if err != nil {
		return "", err
	}
	if hasVideo {
		return tier + VideoTokenTierSuffixVideo, nil
	}
	return tier, nil
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
	return CurrentView().VideoTokenMap()
}

func GetVideoTokenPriceTable(model string) map[string]float64 {
	return CurrentView().VideoTokenTable(model)
}

// LookupVideoTokenPrice returns the configured USD-per-1M-token price for a
// tariff cell. Missing or non-positive prices are an error so callers reject
// the request instead of silently undercharging.
func LookupVideoTokenPrice(model, resolution string, hasVideo bool) (float64, string, error) {
	return CurrentView().LookupVideoTokenPrice(model, resolution, hasVideo)
}

func (v View) LookupVideoTokenPrice(model, resolution string, hasVideo bool) (float64, string, error) {
	key, err := VideoTokenPriceKey(resolution, hasVideo)
	if err != nil {
		return 0, "", err
	}
	table := v.VideoTokenTable(model)
	if len(table) == 0 {
		return 0, key, fmt.Errorf("video token price is not configured for model %s", model)
	}
	price, ok := table[key]
	if !ok || price <= 0 || price != price {
		return 0, key, fmt.Errorf("video token price is not configured for model %s tier %s", model, key)
	}
	return price, key, nil
}

func HasVideoTokenPrice(model string) bool {
	return HasVideoTokenPriceFromTable(GetVideoTokenPriceTable(model))
}

func HasVideoTokenPriceFromTable(table map[string]float64) bool {
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
