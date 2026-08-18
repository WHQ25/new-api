package common

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVideoTotalTokens(t *testing.T) {
	t.Parallel()

	n := ParseVideoTotalTokens([]byte(`{"usage":{"total_tokens":243000}}`))
	assert.Equal(t, 243000, n)

	n = ParseVideoTotalTokens([]byte(`{"code":"success","data":{"usage":{"total_tokens":108000}}}`))
	assert.Equal(t, 108000, n)

	n = ParseVideoTotalTokens([]byte(`{"total_tokens":"972000"}`))
	assert.Equal(t, 972000, n)

	assert.Zero(t, ParseVideoTotalTokens([]byte(`{"usage":{"total_tokens":-1}}`)))
	assert.Zero(t, ParseVideoTotalTokens([]byte(`{"usage":{"total_tokens":"NaN"}}`)))

	// Parsing saturates only for safe int representation. Values above the
	// billing ceiling must survive to settlement, which is the layer that
	// enforces MaxVideoTotalTokens and audits the clamp.
	overCeiling := ParseVideoTotalTokens([]byte(`{"total_tokens":1e20}`))
	assert.Equal(t, math.MaxInt32, overCeiling)
	assert.Greater(t, overCeiling, MaxVideoTotalTokens)
}

func TestBoundedIntFromAny(t *testing.T) {
	t.Parallel()

	n, ok := BoundedIntFromAny(12.9, 100)
	require.True(t, ok)
	assert.Equal(t, 12, n)

	_, ok = BoundedIntFromAny(math.NaN(), 100)
	assert.False(t, ok)
	_, ok = BoundedIntFromAny(math.Inf(1), 100)
	assert.False(t, ok)
	_, ok = BoundedIntFromAny(-3, 100)
	assert.False(t, ok)

	n, ok = BoundedIntFromAny(int64(10_000), 50)
	require.True(t, ok)
	assert.Equal(t, 50, n)
}
