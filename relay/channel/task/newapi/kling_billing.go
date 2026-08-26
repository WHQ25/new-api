package newapi

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const (
	klingDefaultOutputSeconds = 5
	klingFamilyV3             = "v3"
	klingFamilyOmni           = "omni"
)

func (a *TaskAdaptor) EstimateTaskUnitTier(c *gin.Context, info *relaycommon.RelayInfo) (channel.TaskUnitTierEstimate, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return channel.TaskUnitTierEstimate{}, err
	}
	raw, err := loadTaskRequestRaw(c)
	if err != nil {
		return channel.TaskUnitTierEstimate{}, err
	}
	familyModel := info.OriginModelName
	if info.ChannelMeta != nil {
		if mapped := strings.TrimSpace(info.UpstreamModelName); mapped != "" {
			familyModel = mapped
		}
	}
	return estimateKlingTaskUnitTier(familyModel, req, raw)
}

func estimateKlingTaskUnitTier(modelName string, req relaycommon.TaskSubmitReq, raw map[string]any) (channel.TaskUnitTierEstimate, error) {
	if err := validateKlingBillingStructures(raw, req.Metadata); err != nil {
		return channel.TaskUnitTierEstimate{}, err
	}
	family, err := klingModelFamily(modelName)
	if err != nil {
		return channel.TaskUnitTierEstimate{}, err
	}
	seconds, err := klingOutputSeconds(req, raw)
	if err != nil {
		return channel.TaskUnitTierEstimate{}, err
	}
	size, err := klingSizeKey(req, raw)
	if err != nil {
		return channel.TaskUnitTierEstimate{}, err
	}
	audio, err := klingBoolField(raw, req.Metadata, "audio_generation")
	if err != nil {
		return channel.TaskUnitTierEstimate{}, err
	}

	tierKey := size
	switch family {
	case klingFamilyOmni:
		hasRefVideo, err := klingHasRefVideo(raw, req.Metadata)
		if err != nil {
			return channel.TaskUnitTierEstimate{}, err
		}
		if hasRefVideo {
			tierKey = size + "_ref_video"
		} else if audio {
			tierKey = size + "_audio"
		}
	default:
		hasVoice, err := klingHasVoice(raw, req.Metadata)
		if err != nil {
			return channel.TaskUnitTierEstimate{}, err
		}
		if hasVoice {
			tierKey = size + "_voice"
		} else if audio {
			tierKey = size + "_audio"
		}
	}

	return channel.TaskUnitTierEstimate{
		Units:   float64(seconds),
		TierKey: tierKey,
	}, nil
}

func klingModelFamily(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "kling-v3-omni", "keling-v3-omni":
		return klingFamilyOmni, nil
	case "kling-v3", "keling-v3":
		return klingFamilyV3, nil
	default:
		return "", fmt.Errorf("unsupported kling model: %s", name)
	}
}

func klingOutputSeconds(req relaycommon.TaskSubmitReq, raw map[string]any) (int, error) {
	if req.Duration != 0 {
		return klingBoundedSeconds(req.Duration, "duration")
	}
	if strings.TrimSpace(req.Seconds) != "" {
		sec, err := strconv.Atoi(strings.TrimSpace(req.Seconds))
		if err != nil {
			return 0, fmt.Errorf("seconds must be an integer")
		}
		return klingBoundedSeconds(sec, "seconds")
	}
	for _, src := range []map[string]any{raw, req.Metadata} {
		for _, key := range []string{"duration", "seconds"} {
			v, ok := klingLookupOK(src, nil, key)
			if !ok {
				continue
			}
			sec, err := klingIntValue(v, key)
			if err != nil {
				return 0, err
			}
			return klingBoundedSeconds(sec, key)
		}
	}
	return klingDefaultOutputSeconds, nil
}

func klingBoundedSeconds(sec int, field string) (int, error) {
	if sec <= 0 {
		return 0, fmt.Errorf("%s must be positive", field)
	}
	if sec > relaycommon.MaxTaskDurationSeconds {
		return 0, fmt.Errorf("%s exceeds %d seconds", field, relaycommon.MaxTaskDurationSeconds)
	}
	return sec, nil
}

func klingSizeKey(req relaycommon.TaskSubmitReq, raw map[string]any) (string, error) {
	size := strings.TrimSpace(req.Size)
	if size == "" {
		s, err := klingOptionalString(raw, req.Metadata, "size")
		if err != nil {
			return "", err
		}
		size = s
	}
	if size == "" {
		s, err := klingOptionalString(raw, req.Metadata, "resolution")
		if err != nil {
			return "", err
		}
		size = s
	}
	if size == "" {
		return "720p", nil
	}
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "720p", "720", "hd":
		return "720p", nil
	case "1080p", "1080", "fhd":
		return "1080p", nil
	case "4k", "2160p", "2160", "uhd":
		return "4k", nil
	default:
		return "", fmt.Errorf("unsupported kling resolution: %s", size)
	}
}

func validateKlingBillingStructures(raw, meta map[string]any) error {
	if v, ok := klingLookupOK(raw, meta, "element_voice_id"); ok {
		if _, isStr := v.(string); !isStr {
			return fmt.Errorf("element_voice_id must be a string")
		}
	}
	if v, ok := klingLookupOK(raw, meta, "voice_list"); ok {
		normalized, err := klingExpandNestedJSON(v)
		if err != nil {
			return fmt.Errorf("voice_list: %w", err)
		}
		if _, isArr := normalized.([]any); !isArr {
			return fmt.Errorf("voice_list must be an array")
		}
	}
	if v, ok := klingLookupOK(raw, meta, "ext_info"); ok {
		if _, err := klingParseExtInfo(v); err != nil {
			return err
		}
	}
	if v, ok := klingLookupOK(raw, meta, "file_infos"); ok {
		if _, err := klingParseFileInfos(v); err != nil {
			return err
		}
	}
	if v, ok := klingLookupOK(raw, meta, "subject_infos"); ok {
		normalized, err := klingExpandNestedJSON(v)
		if err != nil {
			return fmt.Errorf("subject_infos: %w", err)
		}
		if _, isMap := normalized.(map[string]any); !isMap {
			if _, isArr := normalized.([]any); !isArr {
				return fmt.Errorf("subject_infos must be an object or array")
			}
		}
	}
	return nil
}

func klingParseFileInfos(v any) ([]any, error) {
	normalized, err := klingExpandNestedJSON(v)
	if err != nil {
		return nil, fmt.Errorf("file_infos: %w", err)
	}
	items, ok := normalized.([]any)
	if !ok {
		return nil, fmt.Errorf("file_infos must be an array")
	}
	for i, item := range items {
		if _, ok := item.(map[string]any); !ok {
			return nil, fmt.Errorf("file_infos[%d] must be an object", i)
		}
	}
	return items, nil
}

func klingHasRefVideo(raw, meta map[string]any) (bool, error) {
	v, ok := klingLookupOK(raw, meta, "file_infos")
	if !ok {
		return false, nil
	}
	items, err := klingParseFileInfos(v)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		m := item.(map[string]any)
		cat := strings.ToLower(klingMapString(m, "Category", "category", "type", "Type"))
		if cat == "video" {
			return true, nil
		}
	}
	return false, nil
}

func klingHasVoice(raw, meta map[string]any) (bool, error) {
	if v, ok := klingLookupOK(raw, meta, "element_voice_id"); ok {
		s, isStr := v.(string)
		if !isStr {
			return false, fmt.Errorf("element_voice_id must be a string")
		}
		if strings.TrimSpace(s) != "" {
			return true, nil
		}
	}
	if v, ok := klingLookupOK(raw, meta, "voice_list"); ok {
		normalized, err := klingExpandNestedJSON(v)
		if err != nil {
			return false, fmt.Errorf("voice_list: %w", err)
		}
		items, isArr := normalized.([]any)
		if !isArr {
			return false, fmt.Errorf("voice_list must be an array")
		}
		if len(items) > 0 {
			return true, nil
		}
	}
	if v, ok := klingLookupOK(raw, meta, "ext_info"); ok {
		parsed, err := klingParseExtInfo(v)
		if err != nil {
			return false, err
		}
		has, err := klingValueHasVoice(parsed)
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	for _, key := range []string{"file_infos", "subject_infos"} {
		if v, ok := klingLookupOK(raw, meta, key); ok {
			normalized, err := klingExpandNestedJSON(v)
			if err != nil {
				return false, fmt.Errorf("%s: %w", key, err)
			}
			has, err := klingValueHasVoice(normalized)
			if err != nil {
				return false, err
			}
			if has {
				return true, nil
			}
		}
	}
	return false, nil
}

func klingParseExtInfo(v any) (any, error) {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return map[string]any{}, nil
		}
		var parsed any
		if err := common.UnmarshalJsonStr(s, &parsed); err != nil {
			return nil, fmt.Errorf("ext_info must be valid JSON: %w", err)
		}
		return klingExpandNestedJSON(parsed)
	case map[string]any:
		return klingExpandNestedJSON(t)
	default:
		return nil, fmt.Errorf("ext_info must be an object or JSON string")
	}
}

func klingExpandNestedJSON(v any) (any, error) {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" || (s[0] != '{' && s[0] != '[') {
			return t, nil
		}
		var parsed any
		if err := common.UnmarshalJsonStr(s, &parsed); err != nil {
			return nil, fmt.Errorf("malformed nested JSON: %w", err)
		}
		return klingExpandNestedJSON(parsed)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			expanded, err := klingExpandNestedJSON(val)
			if err != nil {
				return nil, err
			}
			out[k] = expanded
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			expanded, err := klingExpandNestedJSON(val)
			if err != nil {
				return nil, err
			}
			out[i] = expanded
		}
		return out, nil
	default:
		return v, nil
	}
}

func klingValueHasVoice(v any) (bool, error) {
	switch t := v.(type) {
	case map[string]any:
		if raw, exists := t["element_voice_id"]; exists && raw != nil {
			s, ok := raw.(string)
			if !ok {
				return false, fmt.Errorf("element_voice_id must be a string")
			}
			if strings.TrimSpace(s) != "" {
				return true, nil
			}
		}
		if raw, exists := t["voice_id"]; exists && raw != nil {
			switch n := raw.(type) {
			case string:
				if strings.TrimSpace(n) != "" {
					return true, nil
				}
			case float64:
				return true, nil
			default:
				return false, fmt.Errorf("voice_id has invalid type")
			}
		}
		if raw, exists := t["voice_list"]; exists && raw != nil {
			items, ok := raw.([]any)
			if !ok {
				return false, fmt.Errorf("voice_list must be an array")
			}
			if len(items) > 0 {
				return true, nil
			}
		}
		for _, x := range t {
			has, err := klingValueHasVoice(x)
			if err != nil {
				return false, err
			}
			if has {
				return true, nil
			}
		}
	case []any:
		for _, x := range t {
			has, err := klingValueHasVoice(x)
			if err != nil {
				return false, err
			}
			if has {
				return true, nil
			}
		}
	}
	return false, nil
}

func klingBoolField(raw, meta map[string]any, key string) (bool, error) {
	v, ok := klingLookupOK(raw, meta, key)
	if !ok {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return b, nil
}

func klingOptionalString(raw, meta map[string]any, key string) (string, error) {
	v, ok := klingLookupOK(raw, meta, key)
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(s), nil
}

func klingIntValue(v any, field string) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("%s must be an integer", field)
		}
		return int(n), nil
	case string:
		sec, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", field)
		}
		return sec, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", field)
	}
}

func klingLookupOK(raw, meta map[string]any, key string) (any, bool) {
	if raw != nil {
		if v, exists := raw[key]; exists && v != nil {
			return v, true
		}
	}
	if meta != nil {
		if v, exists := meta[key]; exists && v != nil {
			return v, true
		}
	}
	return nil, false
}

func klingMapString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func loadTaskRequestRaw(c *gin.Context) (map[string]any, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return map[string]any{}, nil
	}
	body, err := storage.Bytes()
	if err != nil || len(body) == 0 {
		return map[string]any{}, nil
	}
	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid task request body: %w", err)
	}
	return raw, nil
}
