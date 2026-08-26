package newapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateKlingTaskUnitTierMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		model    string
		req      relaycommon.TaskSubmitReq
		raw      map[string]any
		wantKey  string
		wantUnit float64
	}{
		{name: "omni default 720p silent", model: "kling-v3-omni", wantKey: "720p", wantUnit: 5},
		{name: "omni keling alias", model: "keling-v3-omni", wantKey: "720p", wantUnit: 5},
		{
			name:     "omni 1080p audio",
			model:    "kling-v3-omni",
			req:      relaycommon.TaskSubmitReq{Size: "1080P", Duration: 8},
			raw:      map[string]any{"audio_generation": true},
			wantKey:  "1080p_audio",
			wantUnit: 8,
		},
		{
			name:    "omni 720p ref video beats audio",
			model:   "kling-v3-omni",
			req:     relaycommon.TaskSubmitReq{Size: "720P"},
			raw:     map[string]any{"audio_generation": true, "file_infos": []any{map[string]any{"Category": "Video", "Url": "https://x/v.mp4"}}},
			wantKey: "720p_ref_video", wantUnit: 5,
		},
		{
			name:    "omni 4k ref video",
			model:   "kling-v3-omni",
			req:     relaycommon.TaskSubmitReq{Size: "4K"},
			raw:     map[string]any{"file_infos": []any{map[string]any{"category": "video"}}},
			wantKey: "4k_ref_video", wantUnit: 5,
		},
		{
			name:    "omni 4k silent",
			model:   "kling-v3-omni",
			req:     relaycommon.TaskSubmitReq{Size: "4k"},
			wantKey: "4k", wantUnit: 5,
		},
		{
			name:    "omni 4k audio same key family",
			model:   "kling-v3-omni",
			req:     relaycommon.TaskSubmitReq{Size: "2160p"},
			raw:     map[string]any{"audio_generation": true},
			wantKey: "4k_audio", wantUnit: 5,
		},
		{
			name:    "v3 720p audio",
			model:   "kling-v3",
			req:     relaycommon.TaskSubmitReq{Size: "720P"},
			raw:     map[string]any{"audio_generation": true},
			wantKey: "720p_audio", wantUnit: 5,
		},
		{
			name:    "v3 1080p voice beats audio",
			model:   "kling-v3",
			req:     relaycommon.TaskSubmitReq{Size: "1080P"},
			raw:     map[string]any{"audio_generation": true, "element_voice_id": "869048851066937391"},
			wantKey: "1080p_voice", wantUnit: 5,
		},
		{
			name:    "v3 4k voice from nested ext_info",
			model:   "kling-v3",
			req:     relaycommon.TaskSubmitReq{Size: "4K"},
			raw:     map[string]any{"ext_info": `{"AdditionalParameters":"{\"voice_list\":[{\"voice_id\":1}]}"}`},
			wantKey: "4k_voice", wantUnit: 5,
		},
		{
			name:    "v3 keling alias silent",
			model:   "keling-v3",
			req:     relaycommon.TaskSubmitReq{Size: "fhd"},
			wantKey: "1080p", wantUnit: 5,
		},
		{
			name:    "image first frame is not ref video",
			model:   "kling-v3-omni",
			req:     relaycommon.TaskSubmitReq{Size: "720P"},
			raw:     map[string]any{"file_infos": []any{map[string]any{"Category": "Image", "Usage": "FirstFrame"}}},
			wantKey: "720p", wantUnit: 5,
		},
		{
			name:     "requested seconds from seconds field",
			model:    "kling-v3",
			req:      relaycommon.TaskSubmitReq{Seconds: "10", Size: "hd"},
			wantKey:  "720p",
			wantUnit: 10,
		},
		{
			name:     "max duration is allowed",
			model:    "kling-v3",
			req:      relaycommon.TaskSubmitReq{Duration: relaycommon.MaxTaskDurationSeconds},
			wantKey:  "720p",
			wantUnit: float64(relaycommon.MaxTaskDurationSeconds),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := estimateKlingTaskUnitTier(tc.model, tc.req, tc.raw)
			require.NoError(t, err)
			assert.Equal(t, tc.wantKey, got.TierKey)
			assert.Equal(t, tc.wantUnit, got.Units)
		})
	}
}

func TestEstimateKlingTaskUnitTierRejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		model string
		req   relaycommon.TaskSubmitReq
		raw   map[string]any
		want  string
	}{
		{name: "unknown model", model: "kling-v2", want: "unsupported kling model"},
		{name: "kling substring is not enough", model: "my-kling-v3-omni-preview", want: "unsupported kling model"},
		{name: "unknown resolution", model: "kling-v3", req: relaycommon.TaskSubmitReq{Size: "1440p"}, want: "unsupported kling resolution"},
		{name: "duration over max", model: "kling-v3", req: relaycommon.TaskSubmitReq{Duration: relaycommon.MaxTaskDurationSeconds + 1}, want: "exceeds"},
		{name: "audio_generation wrong type", model: "kling-v3", raw: map[string]any{"audio_generation": "true"}, want: "audio_generation must be a boolean"},
		{name: "file_infos wrong type", model: "kling-v3-omni", raw: map[string]any{"file_infos": "video"}, want: "file_infos must be an array"},
		{name: "voice_list wrong type", model: "kling-v3", raw: map[string]any{"voice_list": "id"}, want: "voice_list must be an array"},
		{name: "element_voice_id wrong type", model: "kling-v3", raw: map[string]any{"element_voice_id": 123}, want: "element_voice_id must be a string"},
		{name: "ext_info wrong type", model: "kling-v3", raw: map[string]any{"ext_info": 1}, want: "ext_info must be an object or JSON string"},
		{name: "ext_info invalid json", model: "kling-v3", raw: map[string]any{"ext_info": "{not-json"}, want: "ext_info must be valid JSON"},
		{
			name:  "malformed nested ext_info does not fall to audio",
			model: "kling-v3",
			req:   relaycommon.TaskSubmitReq{Size: "720P"},
			raw: map[string]any{
				"audio_generation": true,
				"ext_info": map[string]any{
					"AdditionalParameters": `{"voice_list":[{"voice_id":1}`,
				},
			},
			want: "malformed nested JSON",
		},
		{
			name:  "malformed file_infos json string",
			model: "kling-v3-omni",
			raw:   map[string]any{"file_infos": `[{"Category":"Video"`},
			want:  "malformed nested JSON",
		},
		{
			name:  "malformed voice_list json string",
			model: "kling-v3",
			raw:   map[string]any{"voice_list": `[{"voice_id":1}`},
			want:  "malformed nested JSON",
		},
		{
			name:  "malformed subject_infos json string",
			model: "kling-v3",
			raw:   map[string]any{"subject_infos": `{"element_voice_id":"abc"`},
			want:  "malformed nested JSON",
		},
		{name: "size wrong type", model: "kling-v3", raw: map[string]any{"size": 720}, want: "size must be a string"},
		{
			name:  "voice hit still rejects malformed ext_info",
			model: "kling-v3",
			req:   relaycommon.TaskSubmitReq{Size: "720P"},
			raw: map[string]any{
				"element_voice_id": "voice-1",
				"ext_info": map[string]any{
					"AdditionalParameters": `{"voice_list":[{"voice_id":1}`,
				},
			},
			want: "malformed nested JSON",
		},
		{
			name:  "voice list hit still rejects malformed file_infos",
			model: "kling-v3",
			req:   relaycommon.TaskSubmitReq{Size: "720P"},
			raw: map[string]any{
				"voice_list": []any{map[string]any{"voice_id": "1"}},
				"file_infos": `[{"Category":"Video"`,
			},
			want: "malformed nested JSON",
		},
		{
			name:  "ref video hit still rejects malformed subject_infos",
			model: "kling-v3-omni",
			req:   relaycommon.TaskSubmitReq{Size: "720P"},
			raw: map[string]any{
				"file_infos":    []any{map[string]any{"Category": "Video"}},
				"subject_infos": `{"element_voice_id":"abc"`,
			},
			want: "malformed nested JSON",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := estimateKlingTaskUnitTier(tc.model, tc.req, tc.raw)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestEstimateTaskUnitTierUsesUpstreamModelFamily(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"custom-kling","prompt":"p","size":"720P","audio_generation":true}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "p", Size: "720P"})

	a := &TaskAdaptor{}
	got, err := a.EstimateTaskUnitTier(c, &relaycommon.RelayInfo{
		OriginModelName: "custom-kling",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "kling-v3",
			IsModelMapped:     true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "720p_audio", got.TierKey)
	assert.Equal(t, 5.0, got.Units)
}

func TestEstimateTaskUnitTierRejectsMalformedNestedExtInfoOnRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"kling-v3","prompt":"p","size":"720P","audio_generation":true,"ext_info":{"AdditionalParameters":"{\"voice_list\":[{\"voice_id\":1}"}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "p", Size: "720P"})

	a := &TaskAdaptor{}
	_, err := a.EstimateTaskUnitTier(c, &relaycommon.RelayInfo{OriginModelName: "kling-v3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed nested JSON")
}

func TestBuildRequestBodyPreservesKlingFieldsAndMapsModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"kling-v3-omni","prompt":"a cat","size":"1080P","audio_generation":true,"file_infos":[{"Category":"Video","Url":"https://x/v.mp4"}],"element_voice_id":"abc","ext_info":{"keep":1}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		OriginModelName: "kling-v3-omni",
		ChannelMeta: &relaycommon.ChannelMeta{
			IsModelMapped:     true,
			UpstreamModelName: "official-kling-v3-omni",
		},
	}
	a := &TaskAdaptor{}
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, common.Unmarshal(payload, &got))
	assert.Equal(t, "official-kling-v3-omni", got["model"])
	assert.Equal(t, "a cat", got["prompt"])
	assert.Equal(t, "1080P", got["size"])
	assert.Equal(t, true, got["audio_generation"])
	assert.Equal(t, "abc", got["element_voice_id"])
	require.Contains(t, got, "file_infos")
	require.Contains(t, got, "ext_info")
}

func TestBuildRequestBodyUsesOriginModelWhenUnmapped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"kling-v3","prompt":"p","voice_list":[{"voice_id":1}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		OriginModelName: "kling-v3",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	a := &TaskAdaptor{}
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, common.Unmarshal(payload, &got))
	assert.Equal(t, "kling-v3", got["model"])
	assert.Equal(t, info.UpstreamModelName, "kling-v3")
	require.Contains(t, got, "voice_list")
}
