package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupVideoTokenPrice(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":      `{"seedance-2":"video_token"}`,
		"billing_setting.video_token_price": `{"seedance-2":{"720p":7,"720p_video":4.2,"1080p":7.7}}`,
	}))

	assert.True(t, IsVideoTokenBilling("seedance-2"))
	assert.True(t, HasVideoTokenPrice("seedance-2"))

	price, key, err := LookupVideoTokenPrice("seedance-2", "720P", false)
	require.NoError(t, err)
	assert.Equal(t, "720p", key)
	assert.Equal(t, 7.0, price)

	price, key, err = LookupVideoTokenPrice("seedance-2", "720p", true)
	require.NoError(t, err)
	assert.Equal(t, "720p_video", key)
	assert.Equal(t, 4.2, price)

	_, key, err = LookupVideoTokenPrice("seedance-2", "4k", false)
	require.Error(t, err)
	assert.Equal(t, "4k", key)

	_, _, err = LookupVideoTokenPrice("seedance-2", "1440p", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported video resolution")

	_, _, err = LookupVideoTokenPrice("missing", "720p", false)
	require.Error(t, err)
}
