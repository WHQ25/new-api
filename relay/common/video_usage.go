package common

import (
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// MaxVideoTotalTokens is 4K 16:9 in+out at MaxTaskDurationSeconds:
// (3840 * 2160 * 24 * 3600 * 2) / 1024
const MaxVideoTotalTokens = 1_399_680_000

// ParseVideoTotalTokens extracts a bounded token count from an upstream
// video-task body. Invalid, negative, NaN, and missing values yield 0.
func ParseVideoTotalTokens(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return 0
	}
	if n, ok := BoundedIntFromAny(raw["total_tokens"], MaxVideoTotalTokens); ok && n > 0 {
		return n
	}
	if usage, ok := raw["usage"].(map[string]any); ok {
		if n, ok := BoundedIntFromAny(usage["total_tokens"], MaxVideoTotalTokens); ok && n > 0 {
			return n
		}
	}
	if meta, ok := raw["metadata"].(map[string]any); ok {
		if n, ok := BoundedIntFromAny(meta["total_tokens"], MaxVideoTotalTokens); ok && n > 0 {
			return n
		}
		if usage, ok := meta["usage"].(map[string]any); ok {
			if n, ok := BoundedIntFromAny(usage["total_tokens"], MaxVideoTotalTokens); ok && n > 0 {
				return n
			}
		}
	}
	if data, ok := raw["data"].(map[string]any); ok {
		if n, ok := BoundedIntFromAny(data["total_tokens"], MaxVideoTotalTokens); ok && n > 0 {
			return n
		}
		if usage, ok := data["usage"].(map[string]any); ok {
			if n, ok := BoundedIntFromAny(usage["total_tokens"], MaxVideoTotalTokens); ok && n > 0 {
				return n
			}
		}
	}
	return 0
}

func BoundedIntFromAny(raw any, max int) (int, bool) {
	if raw == nil || max <= 0 {
		return 0, false
	}
	var n float64
	switch v := raw.(type) {
	case int:
		n = float64(v)
	case int32:
		n = float64(v)
	case int64:
		n = float64(v)
	case uint:
		n = float64(v)
	case uint32:
		n = float64(v)
	case uint64:
		if v > uint64(max) {
			return max, true
		}
		n = float64(v)
	case float32:
		n = float64(v)
	case float64:
		n = v
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, false
	}
	if n > float64(max) {
		return max, true
	}
	return int(math.Trunc(n)), true
}
