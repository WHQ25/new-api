package helper

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

const (
	videoTokenFPS            = 24
	videoTokenDefaultSeconds = 5.0
	videoTokenDefaultRatio   = "16:9"
)

// videoTokenPixels 是方舟官方公布的「分辨率 × 宽高比 → 宽高像素值」对照表，取
// Seedance 2.5 一列；4k 一档官方只有 Seedance 2.0 系列有值。
// https://docs.volcengine.com/docs/82379/1520757
//
// 不要按「短边定档、长边按比例推算」自己算这张表。方舟是按恒定像素预算分配的：
// 1:1 在 720p 下是 960×960 而不是 720×720，自己推会少算 44%。
var videoTokenPixels = map[string]map[string][2]int{
	billing_setting.VideoTokenTier480p: {
		"16:9": {854, 480},
		"4:3":  {752, 560},
		"1:1":  {640, 640},
		"3:4":  {560, 752},
		"9:16": {480, 854},
		"21:9": {992, 432},
	},
	billing_setting.VideoTokenTier720p: {
		"16:9": {1280, 720},
		"4:3":  {1112, 834},
		"1:1":  {960, 960},
		"3:4":  {834, 1112},
		"9:16": {720, 1280},
		"21:9": {1470, 630},
	},
	billing_setting.VideoTokenTier1080p: {
		"16:9": {1920, 1080},
		"4:3":  {1664, 1248},
		"1:1":  {1440, 1440},
		"3:4":  {1248, 1664},
		"9:16": {1080, 1920},
		"21:9": {2206, 946},
	},
	billing_setting.VideoTokenTier4K: {
		"16:9": {3840, 2160},
		"4:3":  {3326, 2494},
		"1:1":  {2880, 2880},
		"3:4":  {2494, 3326},
		"9:16": {2160, 3840},
		"21:9": {4398, 1886},
	},
}

type videoTokenEstimate struct {
	Tariff      billing_setting.VideoTokenTariff
	Tokens      float64
	Variants    []string
	OutSeconds  float64
	InSeconds   float64
	Resolution  string
	AspectRatio string
}

// BillableUnits is the quantity the tariff price multiplies: millions of
// pixel-formula tokens for per_token tables, requested seconds for per_second
// tables.
func (e videoTokenEstimate) BillableUnits() float64 {
	if e.Tariff.Unit == billing_setting.VideoTokenUnitSecond {
		return e.OutSeconds
	}
	return e.Tokens / 1_000_000
}

// ModelPriceHelperVideoToken 计算视频阶梯计费的预扣费金额。billableSeconds 是上游给出的
// 实际生成秒数（见 channel.VideoBillingResolver），传 0 表示按通用规则
// （客户端请求时长，未指定时用通用默认值）计费。
func ModelPriceHelperVideoToken(c *gin.Context, info *relaycommon.RelayInfo, billableSeconds float64) (hosttypes.PriceData, error) {
	groupRatioInfo := HandleGroupRatio(c, info)
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return hosttypes.PriceData{}, err
	}
	estimate, err := estimateVideoTokenBilling(info.OriginModelName, req, billableSeconds)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	quotaFloat := estimate.BillableUnits() * estimate.Tariff.Price * common.QuotaPerUnit * groupRatioInfo.GroupRatio
	quota, err := common.QuotaFromFloatStrict(quotaFloat)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	priceData := hosttypes.PriceData{
		ModelPrice:        estimate.Tariff.Price,
		UsePrice:          false,
		Quota:             quota,
		GroupRatioInfo:    groupRatioInfo,
		BillingMode:       billing_setting.BillingModeVideoToken,
		VideoTokenPrice:   estimate.Tariff.Price,
		VideoTokenTier:    estimate.Tariff.Key,
		VideoTokenUnit:    estimate.Tariff.Unit,
		QuotaToPreConsume: quota,
	}
	if estimate.Tariff.Unit == billing_setting.VideoTokenUnitSecond {
		priceData.VideoTokenSeconds = estimate.OutSeconds
	} else {
		priceData.EstimatedTokens = estimate.Tokens
	}
	return priceData, nil
}

func estimateVideoTokenBilling(modelName string, req relaycommon.TaskSubmitReq, billableSeconds float64) (videoTokenEstimate, error) {
	// 分辨率与宽高比必须与下发给上游的请求体同源，否则会按一个档位计费、按另一个档位生成。
	resolution := req.RequestedResolution()
	aspect := req.RequestedAspectRatio()
	if _, err := billing_setting.ParseVideoTokenTier(resolution); err != nil {
		return videoTokenEstimate{}, err
	}
	variants := requestVideoTokenVariants(req)
	hasVideo := lo.Contains(variants, billing_setting.VideoTokenVariantVideo)
	// 上游给出的实际生成秒数优先：方舟的 frames 优先级高于 duration，只看 duration
	// 会按 5 秒收费、按 12 秒生成。
	outSeconds := clampVideoTokenSeconds(billableSeconds)
	if billableSeconds <= 0 {
		outSeconds = clampVideoTokenSeconds(float64(req.RequestedOutputSeconds()))
	}
	inSeconds := float64(0)
	if hasVideo {
		inSeconds = float64(metadataInt(req.Metadata, "input_duration"))
		if inSeconds <= 0 {
			inSeconds = float64(metadataInt(req.Metadata, "input_seconds"))
		}
		if inSeconds <= 0 {
			inSeconds = outSeconds
		}
	}

	tariff, err := billing_setting.LookupVideoTokenPrice(modelName, resolution, variants)
	if err != nil {
		return videoTokenEstimate{}, err
	}
	return videoTokenEstimate{
		Tariff:      tariff,
		Tokens:      EstimateSeedanceTokens(resolution, aspect, outSeconds, inSeconds, hasVideo),
		Variants:    variants,
		OutSeconds:  outSeconds,
		InSeconds:   inSeconds,
		Resolution:  billing_setting.NormalizeVideoTokenTier(resolution),
		AspectRatio: normalizeAspectRatio(billing_setting.NormalizeVideoTokenTier(resolution), aspect),
	}, nil
}

// requestVideoTokenVariants reads the tariff dimensions a request selects beyond
// its resolution. Which of them actually bill is decided by the configured
// table (see LookupVideoTokenPrice), so reading a signal here is safe even for
// providers that do not price it.
func requestVideoTokenVariants(req relaycommon.TaskSubmitReq) []string {
	variants := make([]string, 0, len(billing_setting.VideoTokenVariants()))
	if metadataHasVideo(req.Metadata) {
		variants = append(variants, billing_setting.VideoTokenVariantVideo)
	}
	// 指定音色必然是有声生成：可灵的价目表里「有声+音色」是「有声」的加价档，
	// 只报 voice 会落到一个没人配置的 _voice 格子上，把请求打成 400。
	hasVoice := metadataNonEmptyString(req.Metadata, "voice_id", "voice", "timbre")
	if hasVoice || metadataBool(req.Metadata, "generate_audio", "audio") {
		variants = append(variants, billing_setting.VideoTokenVariantAudio)
	}
	if hasVoice {
		variants = append(variants, billing_setting.VideoTokenVariantVoice)
	}
	return variants
}

// EstimateSeedanceTokens implements the official Seedance meter:
// (width * height * 24 * billedSeconds) / 1024
// billedSeconds is output seconds, plus input seconds when the request includes video.
func EstimateSeedanceTokens(resolution, aspectRatio string, outSeconds, inSeconds float64, hasVideo bool) float64 {
	tier := billing_setting.NormalizeVideoTokenTier(resolution)
	size := videoTokenPixels[tier][normalizeAspectRatio(tier, aspectRatio)]
	seconds := clampVideoTokenSeconds(outSeconds)
	if hasVideo {
		seconds += clampVideoTokenSeconds(inSeconds)
	}
	return float64(size[0]*size[1]*videoTokenFPS) * seconds / 1024
}

// clampVideoTokenSeconds 把时长收敛到可计费区间，未知时退到通用默认值。
func clampVideoTokenSeconds(seconds float64) float64 {
	if seconds <= 0 {
		return videoTokenDefaultSeconds
	}
	if seconds > relaycommon.MaxTaskDurationSeconds {
		return relaycommon.MaxTaskDurationSeconds
	}
	return seconds
}

// normalizeAspectRatio 返回实际参与计费的宽高比。表里没有的取值——adaptive、上游后加的
// 比例——按 16:9 估算：方舟按恒定像素预算分配，各比例的像素数在 720p 及以上相差不到
// 0.7%、480p 不到 4.5%，且 Seedance 会用上游回报的实际用量做差额结算。
func normalizeAspectRatio(tier, ratio string) string {
	normalized := strings.ToLower(strings.TrimSpace(ratio))
	if _, ok := videoTokenPixels[tier][normalized]; ok {
		return normalized
	}
	return videoTokenDefaultRatio
}

func metadataHasVideo(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	contentRaw, ok := metadata["content"]
	if !ok {
		return false
	}
	contentSlice, ok := contentRaw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}

// metadataBool reads a boolean tariff signal that clients may send as a JSON
// bool, a string or a number, depending on the upstream protocol being proxied.
func metadataBool(metadata map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		raw, ok := relaycommon.MetadataValue(metadata, key)
		if !ok {
			continue
		}
		switch value := raw.(type) {
		case bool:
			if value {
				return true
			}
		case string:
			if strings.EqualFold(strings.TrimSpace(value), "true") {
				return true
			}
		case float64:
			if value != 0 {
				return true
			}
		}
	}
	return false
}

func metadataNonEmptyString(metadata map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		raw, ok := relaycommon.MetadataValue(metadata, key)
		if !ok {
			continue
		}
		if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func metadataInt(metadata map[string]interface{}, key string) int {
	raw, ok := relaycommon.MetadataValue(metadata, key)
	if !ok {
		return 0
	}
	n, ok := relaycommon.BoundedIntFromAny(raw, relaycommon.MaxTaskDurationSeconds)
	if !ok {
		return 0
	}
	return n
}
