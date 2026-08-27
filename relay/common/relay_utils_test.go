package common

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeURLForLogMasksSensitiveQueryValues(t *testing.T) {
	rawURL := "https://example.test/v1beta/models/gemini:streamGenerateContent?alt=sse&key=sk-secret&access_token=ya29-secret&api-version=2024-02-01"

	got := SanitizeURLForLog(rawURL)

	assert.NotContains(t, got, "sk-secret")
	assert.NotContains(t, got, "ya29-secret")
	parsedURL, err := url.Parse(got)
	require.NoError(t, err)
	query := parsedURL.Query()
	assert.Equal(t, "***masked***", query.Get("key"))
	assert.Equal(t, "***masked***", query.Get("access_token"))
	assert.Equal(t, "sse", query.Get("alt"))
	assert.Equal(t, "2024-02-01", query.Get("api-version"))
}

func TestSanitizeURLForLogMasksAWSAndSecretLikeQueryKeys(t *testing.T) {
	rawURL := "https://example.test/path?X-Amz-Credential=credential&X-Amz-Signature=signature&session_token=session&client_secret=secret&model=gpt-test"

	got := SanitizeURLForLog(rawURL)

	assert.NotContains(t, got, "X-Amz-Credential=credential")
	assert.NotContains(t, got, "X-Amz-Signature=signature")
	assert.NotContains(t, got, "session_token=session")
	assert.NotContains(t, got, "client_secret=secret")
	parsedURL, err := url.Parse(got)
	require.NoError(t, err)
	query := parsedURL.Query()
	assert.Equal(t, "***masked***", query.Get("X-Amz-Credential"))
	assert.Equal(t, "***masked***", query.Get("X-Amz-Signature"))
	assert.Equal(t, "***masked***", query.Get("session_token"))
	assert.Equal(t, "***masked***", query.Get("client_secret"))
	assert.Equal(t, "gpt-test", query.Get("model"))
}

func TestSanitizeURLForLogKeepsURLWithoutSensitiveQuery(t *testing.T) {
	rawURL := "https://example.test/v1/chat/completions?api-version=2024-02-01&alt=sse"

	got := SanitizeURLForLog(rawURL)

	assert.Equal(t, rawURL, got)
}

func TestValidateMultipartDirectNormalizesImageField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.NewReader(`{"model":"wan2.7-i2v","prompt":"animate","image":" https://example.com/first.png "}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	info := &RelayInfo{
		TaskRelayInfo: &TaskRelayInfo{},
	}

	taskErr := ValidateMultipartDirect(context, info)

	require.Nil(t, taskErr)
	storedReq, err := GetTaskRequest(context)
	require.NoError(t, err)
	require.Equal(t, []string{"https://example.com/first.png"}, storedReq.Images)
	require.Equal(t, constant.TaskActionGenerate, info.Action)
}

// TestTaskDurationBounds guards the billing invariant that user-supplied
// video duration (a quota multiplier via OtherRatio "seconds") is bounded, so
// it can never overflow quota calculation into a negative charge.
func TestTaskDurationBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func(t *testing.T, body string) (*gin.Context, *RelayInfo) {
		request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = request
		return context, &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	}

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "huge duration is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","duration":9999999999}`,
			wantErr: true,
		},
		{
			name:    "huge seconds string is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","seconds":"9999999999"}`,
			wantErr: true,
		},
		{
			name:    "negative duration is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","duration":-8}`,
			wantErr: true,
		},
		{
			name: "normal duration is accepted",
			body: `{"model":"sora-2","prompt":"a cat","seconds":"8"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" (multipart direct)", func(t *testing.T) {
			context, info := newContext(t, tt.body)
			taskErr := ValidateMultipartDirect(context, info)
			if tt.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, "invalid_seconds", taskErr.Code)
			} else {
				require.Nil(t, taskErr)
			}
		})
		t.Run(tt.name+" (basic task request)", func(t *testing.T) {
			context, info := newContext(t, tt.body)
			taskErr := ValidateBasicTaskRequest(context, info, constant.TaskActionGenerate)
			if tt.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, "invalid_seconds", taskErr.Code)
			} else {
				require.Nil(t, taskErr)
			}
		})
	}
}

// 计费按精确小写 key 读 metadata，适配器却把整份 metadata 交给 encoding/json，
// 后者匹配字段名是大小写不敏感的。两边看到的不是同一份值时，请求会按一个档位收费、
// 按另一个档位生成——例如 GENERATE_AUDIO 按无声档收钱却生成有声视频。
func TestCanonicalizeMetadataKeys(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]any
		want     map[string]any
	}{
		{
			name:     "nil stays nil",
			metadata: nil,
			want:     nil,
		},
		{
			name:     "already canonical is untouched",
			metadata: map[string]any{"resolution": "720p", "generate_audio": true},
			want:     map[string]any{"resolution": "720p", "generate_audio": true},
		},
		{
			name:     "mixed case keys are lowered",
			metadata: map[string]any{"DURATION": 10, "GenerateAudio": true, "Resolution": "1080p"},
			want:     map[string]any{"duration": 10, "generateaudio": true, "resolution": "1080p"},
		},
		{
			name:     "an exact lowercase key wins a collision",
			metadata: map[string]any{"duration": 5, "DURATION": 10},
			want:     map[string]any{"duration": 5},
		},
		{
			// 上游的 content 项是结构体，大小写不敏感地吃下 "TYPE"/"VIDEO_URL"，
			// 计费侧却按精确小写键判断有没有视频输入。嵌套层不归一化 = 少收。
			name: "nested map keys inside slices are lowered too",
			metadata: map[string]any{
				"Content": []any{map[string]any{"TYPE": "video_url", "VideoUrl": map[string]any{"URL": "https://example.com/a.mp4"}}},
			},
			want: map[string]any{
				"content": []any{map[string]any{"type": "video_url", "videourl": map[string]any{"url": "https://example.com/a.mp4"}}},
			},
		},
		{
			// 通义万相的生成参数装在 parameters 这层里。
			name: "nested parameters container is lowered",
			metadata: map[string]any{
				"Parameters": map[string]any{"DURATION": 10, "Resolution": "1080P"},
			},
			want: map[string]any{
				"parameters": map[string]any{"duration": 10, "resolution": "1080P"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, CanonicalizeMetadataKeys(tc.metadata))
		})
	}
}

// 归一化之后，计费读到的时长/音频档必须与下发给上游的请求体一致。
func TestCanonicalizedMetadataDrivesBillingDimensions(t *testing.T) {
	req := TaskSubmitReq{
		Metadata: CanonicalizeMetadataKeys(map[string]any{
			"RESOLUTION":     "1080p",
			"DURATION":       10,
			"GENERATE_AUDIO": true,
		}),
	}
	assert.Equal(t, "1080p", req.RequestedResolution())
	assert.Equal(t, 10, req.RequestedOutputSeconds())
	assert.Equal(t, true, req.Metadata["generate_audio"])
}

// -1（由上游自选时长）是方舟的私有约定，只有显式开了 option 的适配器才放行。
// 默认放行会让 -1 一路级联到不认识它的下游节点：那边按自己的默认时长预扣，
// 而最终那个上游按时长区间的上界生成，差额由运营方承担。
func TestValidateTaskDurationBoundsGuardsSelfSelectSentinel(t *testing.T) {
	// 默认拒绝。
	for _, req := range []TaskSubmitReq{{Duration: DurationSelfSelect}, {Seconds: "-1"}} {
		taskErr := validateTaskDurationBounds(req)
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	}

	// 显式开启后放行。
	assert.Nil(t, validateTaskDurationBounds(TaskSubmitReq{Duration: DurationSelfSelect}, AllowSelfSelectedDuration()))
	assert.Nil(t, validateTaskDurationBounds(TaskSubmitReq{Seconds: "-1"}, AllowSelfSelectedDuration()))

	assert.Nil(t, validateTaskDurationBounds(TaskSubmitReq{Duration: 5}))

	// 放行哨兵不等于放行其它非法时长。
	for _, req := range []TaskSubmitReq{
		{Duration: -2},
		{Seconds: "-5"},
		{Duration: MaxTaskDurationSeconds + 1},
	} {
		taskErr := validateTaskDurationBounds(req, AllowSelfSelectedDuration())
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	}
}
