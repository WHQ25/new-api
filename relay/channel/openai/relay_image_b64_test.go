package openai

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func allowPrivateImageDownload(t *testing.T) {
	t.Helper()
	service.InitHttpClient()
	setting := system_setting.GetFetchSetting()
	prevSSRF := setting.EnableSSRFProtection
	prevMax := constant.MaxFileDownloadMB
	setting.EnableSSRFProtection = false
	if constant.MaxFileDownloadMB <= 0 {
		constant.MaxFileDownloadMB = 64
	}
	t.Cleanup(func() {
		setting.EnableSSRFProtection = prevSSRF
		constant.MaxFileDownloadMB = prevMax
	})
}

func TestConvertOpenAIImageURLsToB64(t *testing.T) {
	t.Parallel()

	downloads := map[string]string{
		"https://cdn.example/a.png": "aaa",
		"https://cdn.example/b.png": "bbb",
	}
	download := func(imageURL string) (string, error) {
		b64, ok := downloads[imageURL]
		if !ok {
			return "", fmt.Errorf("unexpected url %s", imageURL)
		}
		return b64, nil
	}

	t.Run("fills missing b64 from url and image_url", func(t *testing.T) {
		t.Parallel()
		body := []byte(`{"created":1,"data":[{"url":"https://cdn.example/a.png"},{"image_url":"https://cdn.example/b.png"}],"usage":{"total_tokens":3}}`)

		got, err := convertOpenAIImageURLsToB64(body, download)
		require.NoError(t, err)
		assert.Equal(t, "aaa", gjson.GetBytes(got, "data.0.b64_json").String())
		assert.Equal(t, "https://cdn.example/a.png", gjson.GetBytes(got, "data.0.url").String())
		assert.Equal(t, "bbb", gjson.GetBytes(got, "data.1.b64_json").String())
		assert.Equal(t, int64(3), gjson.GetBytes(got, "usage.total_tokens").Int())
	})

	t.Run("keeps existing b64 without downloading", func(t *testing.T) {
		t.Parallel()
		body := []byte(`{"data":[{"b64_json":"already","url":"https://cdn.example/missing.png"}]}`)

		got, err := convertOpenAIImageURLsToB64(body, func(string) (string, error) {
			t.Fatal("download should not be called when b64_json is present")
			return "", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "already", gjson.GetBytes(got, "data.0.b64_json").String())
	})

	t.Run("rejects url-only item when download fails", func(t *testing.T) {
		t.Parallel()
		body := []byte(`{"data":[{"url":"https://cdn.example/missing.png"}]}`)

		_, err := convertOpenAIImageURLsToB64(body, func(string) (string, error) {
			return "", errors.New("download failed")
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "data[0]")
	})

	t.Run("rejects item missing both b64 and url", func(t *testing.T) {
		t.Parallel()
		_, err := convertOpenAIImageURLsToB64([]byte(`{"data":[{}]}`), download)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing b64_json and url")
	})

	t.Run("rejects oversized data array", func(t *testing.T) {
		t.Parallel()
		items := make([]string, dto.MaxImageN+1)
		for i := range items {
			items[i] = `{"url":"https://cdn.example/a.png"}`
		}
		body := []byte(`{"data":[` + strings.Join(items, ",") + `]}`)

		_, err := convertOpenAIImageURLsToB64(body, download)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds max")
	})
}

func TestOpenaiImageHandlerConvertsURLWhenClientAsksForB64(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	imageBytes := []byte("fake-png")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes)
	}))
	t.Cleanup(server.Close)
	allowPrivateImageDownload(t)

	upstream := fmt.Sprintf(`{"created":1710000000,"data":[{"url":%q}],"usage":{"input_tokens":17,"output_tokens":6250,"total_tokens":6267}}`, server.URL)
	c, recorder, resp, info := newImageTestContext(t, upstream, "application/json", false)
	info.RelayMode = relayconstant.RelayModeImagesGenerations
	info.Request = &dto.ImageRequest{
		Prompt:         "a border collie",
		ResponseFormat: "b64_json",
	}

	usage, apiErr := OpenaiImageHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var payload dto.ImageResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Len(t, payload.Data, 1)
	require.NotEmpty(t, payload.Data[0].B64Json)
	assert.Equal(t, server.URL, payload.Data[0].Url)
	assert.Equal(t, 17, usage.PromptTokens)
	assert.Equal(t, 6250, usage.CompletionTokens)
}

func TestOpenaiImageHandlerLeavesURLWhenClientDoesNotAskForB64(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	upstream := `{"created":1710000000,"data":[{"url":"https://cdn.example/a.png"}]}`
	c, recorder, resp, info := newImageTestContext(t, upstream, "application/json", false)
	info.RelayMode = relayconstant.RelayModeImagesGenerations
	info.Request = &dto.ImageRequest{Prompt: "a border collie", ResponseFormat: "url"}

	usage, apiErr := OpenaiImageHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, upstream, recorder.Body.String())
}

func TestOpenaiImageHandlerErrorsWhenRequestedB64CannotBeFilled(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	c, _, resp, info := newImageTestContext(t, `{"data":[{"url":""}]}`, "application/json", false)
	info.Request = &dto.ImageRequest{Prompt: "a border collie", ResponseFormat: "b64_json"}

	_, apiErr := OpenaiImageHandler(c, info, resp)
	require.NotNil(t, apiErr)
}

func TestOpenaiImageJSONAsStreamConvertsURLWhenClientAsksForB64(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	imageBytes := []byte("fake-png")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes)
	}))
	t.Cleanup(server.Close)
	allowPrivateImageDownload(t)

	upstream := fmt.Sprintf(`{"created":1710000000,"data":[{"url":%q}]}`, server.URL)
	c, recorder, resp, info := newImageTestContext(t, upstream, "application/json", true)
	info.Request = &dto.ImageRequest{Prompt: "a border collie", ResponseFormat: "b64_json"}

	usage, apiErr := openaiImageJSONAsStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Contains(t, recorder.Body.String(), `"b64_json"`)
	require.NotContains(t, recorder.Body.String(), `"b64_json":""`)
}
