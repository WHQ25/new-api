package helper

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	videoTokenFPS            = 24
	videoTokenDefaultSeconds = 5
	videoTokenDefaultRatio   = "16:9"
)

var videoTokenPixels = map[string]map[string][2]int{
	billing_setting.VideoTokenTier480p: {
		"16:9": {854, 480},
		"9:16": {480, 854},
		"1:1":  {480, 480},
	},
	billing_setting.VideoTokenTier720p: {
		"16:9": {1280, 720},
		"9:16": {720, 1280},
		"1:1":  {720, 720},
	},
	billing_setting.VideoTokenTier1080p: {
		"16:9": {1920, 1080},
		"9:16": {1080, 1920},
		"1:1":  {1080, 1080},
	},
	billing_setting.VideoTokenTier4K: {
		"16:9": {3840, 2160},
		"9:16": {2160, 3840},
		"1:1":  {2160, 2160},
	},
}

type videoTokenEstimate struct {
	Tokens      float64
	USDPerM     float64
	Tier        string
	HasVideo    bool
	OutSeconds  int
	InSeconds   int
	Resolution  string
	AspectRatio string
}

func ModelPriceHelperVideoToken(c *gin.Context, info *relaycommon.RelayInfo) (hosttypes.PriceData, error) {
	groupRatioInfo := HandleGroupRatio(c, info)
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return hosttypes.PriceData{}, err
	}
	estimate, err := estimateVideoTokenBilling(info.OriginModelName, req)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	quotaFloat := estimate.Tokens / 1_000_000 * estimate.USDPerM * common.QuotaPerUnit * groupRatioInfo.GroupRatio
	quota, err := common.QuotaFromFloatStrict(quotaFloat)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	return hosttypes.PriceData{
		ModelPrice:        estimate.USDPerM,
		UsePrice:          false,
		Quota:             quota,
		GroupRatioInfo:    groupRatioInfo,
		BillingMode:       billing_setting.BillingModeVideoToken,
		VideoTokenPrice:   estimate.USDPerM,
		VideoTokenTier:    estimate.Tier,
		EstimatedTokens:   estimate.Tokens,
		QuotaToPreConsume: quota,
	}, nil
}

func estimateVideoTokenBilling(modelName string, req relaycommon.TaskSubmitReq) (videoTokenEstimate, error) {
	resolution := metadataString(req.Metadata, "resolution")
	aspect := metadataString(req.Metadata, "ratio")
	if aspect == "" {
		aspect = metadataString(req.Metadata, "aspect_ratio")
	}
	if _, err := billing_setting.ParseVideoTokenTier(resolution); err != nil {
		return videoTokenEstimate{}, err
	}
	hasVideo := metadataHasVideo(req.Metadata)
	outSeconds := requestOutputSeconds(req)
	inSeconds := 0
	if hasVideo {
		inSeconds = metadataInt(req.Metadata, "input_duration")
		if inSeconds <= 0 {
			inSeconds = metadataInt(req.Metadata, "input_seconds")
		}
		if inSeconds <= 0 {
			inSeconds = outSeconds
		}
	}

	tokens := EstimateSeedanceTokens(resolution, aspect, outSeconds, inSeconds, hasVideo)
	price, tier, err := billing_setting.LookupVideoTokenPrice(modelName, resolution, hasVideo)
	if err != nil {
		return videoTokenEstimate{}, err
	}
	return videoTokenEstimate{
		Tokens:      tokens,
		USDPerM:     price,
		Tier:        tier,
		HasVideo:    hasVideo,
		OutSeconds:  outSeconds,
		InSeconds:   inSeconds,
		Resolution:  billing_setting.NormalizeVideoTokenTier(resolution),
		AspectRatio: normalizeAspectRatio(aspect),
	}, nil
}

// EstimateSeedanceTokens implements the official Seedance meter:
// (width * height * 24 * billedSeconds) / 1024
// billedSeconds is output seconds, plus input seconds when the request includes video.
func EstimateSeedanceTokens(resolution, aspectRatio string, outSeconds, inSeconds int, hasVideo bool) float64 {
	tier := billing_setting.NormalizeVideoTokenTier(resolution)
	aspect := normalizeAspectRatio(aspectRatio)
	size := videoTokenPixels[tier][aspect]
	if size[0] == 0 || size[1] == 0 {
		size = videoTokenPixels[tier][videoTokenDefaultRatio]
	}
	seconds := clampVideoTokenSeconds(outSeconds)
	if hasVideo {
		seconds += clampVideoTokenSeconds(inSeconds)
	}
	return float64(size[0]*size[1]*videoTokenFPS*seconds) / 1024
}

func requestOutputSeconds(req relaycommon.TaskSubmitReq) int {
	return clampVideoTokenSeconds(req.RequestedOutputSeconds())
}

func clampVideoTokenSeconds(seconds int) int {
	if seconds <= 0 {
		return videoTokenDefaultSeconds
	}
	if seconds > relaycommon.MaxTaskDurationSeconds {
		return relaycommon.MaxTaskDurationSeconds
	}
	return seconds
}

func normalizeAspectRatio(ratio string) string {
	switch strings.ToLower(strings.TrimSpace(ratio)) {
	case "9:16", "9x16":
		return "9:16"
	case "1:1", "1x1":
		return "1:1"
	default:
		return videoTokenDefaultRatio
	}
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

func metadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func metadataInt(metadata map[string]interface{}, key string) int {
	if metadata == nil {
		return 0
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return 0
	}
	n, ok := relaycommon.BoundedIntFromAny(raw, relaycommon.MaxTaskDurationSeconds)
	if !ok {
		return 0
	}
	return n
}
