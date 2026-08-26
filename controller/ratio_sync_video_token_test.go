package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type syncChannel = struct {
	name string
	data map[string]any
}

func videoTokenChannels(name string, data map[string]any) []syncChannel {
	return []syncChannel{{name: name, data: data}}
}

// The video tier table is a nested map, so it can only take part in sync diffing
// if valueMap enumerates it and normalizeSyncValue renders it as a stable scalar.
func TestBuildDifferences_VideoTokenPriceTable(t *testing.T) {
	const model = "doubao-seedance-1-0-pro"

	cases := []struct {
		name         string
		local        map[string]map[string]float64
		upstream     map[string]any
		wantIncluded bool
		wantCurrent  any
		wantUpstream any
	}{
		{
			name:         "identical tables are not a difference",
			local:        map[string]map[string]float64{model: {"720p": 7, "1080p": 7.7}},
			upstream:     map[string]any{model: map[string]float64{"1080p": 7.7, "720p": 7}},
			wantIncluded: false,
		},
		{
			name:         "changed tier price is a difference",
			local:        map[string]map[string]float64{model: {"720p": 7, "1080p": 7.7}},
			upstream:     map[string]any{model: map[string]float64{"720p": 7, "1080p": 9.9}},
			wantIncluded: true,
			wantCurrent:  `{"1080p":7.7,"720p":7}`,
			wantUpstream: `{"1080p":9.9,"720p":7}`,
		},
		{
			name:         "missing local table is a difference",
			local:        nil,
			upstream:     map[string]any{model: map[string]float64{"720p": 7}},
			wantIncluded: true,
			wantCurrent:  nil,
			wantUpstream: `{"720p":7}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			localData := map[string]any{}
			if tc.local != nil {
				localData[billing_setting.VideoTokenPriceField] = tc.local
			}
			channels := videoTokenChannels("upstream-a", map[string]any{
				billing_setting.VideoTokenPriceField: tc.upstream,
			})

			differences := buildDifferences(localData, channels)
			item, ok := differences[model][billing_setting.VideoTokenPriceField]
			require.Equal(t, tc.wantIncluded, ok, "unexpected difference presence")
			if !tc.wantIncluded {
				return
			}
			assert.Equal(t, tc.wantCurrent, item.Current)
			assert.Equal(t, tc.wantUpstream, item.Upstreams["upstream-a"])
		})
	}
}

// A video_token mode without a price table makes every request to that model
// fail price lookup, so it must never be offered for sync on its own.
func TestDropUnpricedVideoTokenMode(t *testing.T) {
	cases := []struct {
		name      string
		data      map[string]any
		wantModes map[string]any
		wantPrice bool
	}{
		{
			name: "mode without a table is dropped",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"video-a": billing_setting.BillingModeVideoToken,
				},
			},
			wantModes: nil,
		},
		{
			name: "mode with an all-zero table is dropped",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"video-a": billing_setting.BillingModeVideoToken,
				},
				billing_setting.VideoTokenPriceField: map[string]any{
					"video-a": map[string]float64{"720p": 0},
				},
			},
			wantModes: nil,
		},
		{
			name: "mode with a priced table is kept",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"video-a": billing_setting.BillingModeVideoToken,
				},
				billing_setting.VideoTokenPriceField: map[string]any{
					"video-a": map[string]float64{"720p": 7},
				},
			},
			wantModes: map[string]any{"video-a": billing_setting.BillingModeVideoToken},
			wantPrice: true,
		},
		{
			name: "other billing modes are untouched",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"expr-a": billing_setting.BillingModeTieredExpr,
				},
			},
			wantModes: map[string]any{"expr-a": billing_setting.BillingModeTieredExpr},
		},
		{
			name: "task unit tier mode without a table is dropped",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"unit-a": billing_setting.BillingModeTaskUnitTier,
				},
			},
			wantModes: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dropUnpricedVideoTokenMode(tc.data)
			dropUnpricedTaskUnitTierMode(tc.data)

			modes, hasModes := tc.data[billing_setting.BillingModeField]
			if tc.wantModes == nil {
				assert.False(t, hasModes, "billing mode should have been removed entirely")
			} else {
				assert.Equal(t, tc.wantModes, modes)
			}
			_, hasPrice := tc.data[billing_setting.VideoTokenPriceField]
			assert.Equal(t, tc.wantPrice, hasPrice)
		})
	}
}

// Diffing must survive uncomparable values; `a == b` on two maps panics.
func TestDropUnpricedTaskUnitTierMode(t *testing.T) {
	cases := []struct {
		name      string
		data      map[string]any
		wantModes map[string]any
		wantPrice bool
	}{
		{
			name: "mode without a table is dropped",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"unit-a": billing_setting.BillingModeTaskUnitTier,
				},
			},
			wantModes: nil,
		},
		{
			name: "mode with an all-zero table is dropped",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"unit-a": billing_setting.BillingModeTaskUnitTier,
				},
				billing_setting.TaskUnitTierPriceField: map[string]any{
					"unit-a": map[string]float64{"720p": 0},
				},
			},
			wantModes: nil,
		},
		{
			name: "mode with a priced table is kept",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"unit-a": billing_setting.BillingModeTaskUnitTier,
				},
				billing_setting.TaskUnitTierPriceField: map[string]any{
					"unit-a": map[string]float64{"720p": 0.6},
				},
			},
			wantModes: map[string]any{"unit-a": billing_setting.BillingModeTaskUnitTier},
			wantPrice: true,
		},
		{
			name: "video_token mode is untouched",
			data: map[string]any{
				billing_setting.BillingModeField: map[string]any{
					"video-a": billing_setting.BillingModeVideoToken,
				},
				billing_setting.VideoTokenPriceField: map[string]any{
					"video-a": map[string]float64{"720p": 7},
				},
			},
			wantModes: map[string]any{"video-a": billing_setting.BillingModeVideoToken},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dropUnpricedTaskUnitTierMode(tc.data)
			modes, hasModes := tc.data[billing_setting.BillingModeField]
			if tc.wantModes == nil {
				assert.False(t, hasModes)
			} else {
				assert.Equal(t, tc.wantModes, modes)
			}
			_, hasPrice := tc.data[billing_setting.TaskUnitTierPriceField]
			assert.Equal(t, tc.wantPrice, hasPrice)
		})
	}
}

func TestBuildDifferences_TaskUnitTierPriceTable(t *testing.T) {
	const model = "kling-v3"
	localData := map[string]any{
		billing_setting.TaskUnitTierPriceField: map[string]map[string]float64{
			model: {"720p": 0.6},
		},
	}
	channels := videoTokenChannels("upstream-a", map[string]any{
		billing_setting.TaskUnitTierPriceField: map[string]any{
			model: map[string]float64{"720p": 0.9},
		},
	})
	differences := buildDifferences(localData, channels)
	item, ok := differences[model][billing_setting.TaskUnitTierPriceField]
	require.True(t, ok)
	assert.Equal(t, `{"720p":0.6}`, item.Current)
	assert.Equal(t, `{"720p":0.9}`, item.Upstreams["upstream-a"])
}

func TestValuesEqual_UncomparableValues(t *testing.T) {
	assert.True(t, valuesEqual(map[string]any{"a": 1.0}, map[string]any{"a": 1.0}))
	assert.False(t, valuesEqual(map[string]any{"a": 1.0}, map[string]any{"a": 2.0}))
	assert.False(t, valuesEqual(map[string]any{"a": 1.0}, "1"))
	assert.False(t, valuesEqual(nil, map[string]any{"a": 1.0}))
}
