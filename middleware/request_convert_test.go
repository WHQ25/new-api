package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 官方协议入站中间件把厂商形状改写成 new-api 内部统一形状，改写结果必须真的到达
// 下游 handler。只设置 KeyRequestBody 是不够的：common.GetRequestBody 优先读
// KeyBodyStorage，而中间件自己调用的 UnmarshalBodyReusable 已经把改写前的 body
// 填进去了，覆盖会被静默忽略。分发器随后从 body 顶层读 model 选渠道，拿到原始
// body 就会因为厂商用的是 model_name / req_key 而报「模型名称未指定」。
func TestOfficialProtocolConvertersReplaceRequestBody(t *testing.T) {
	cases := []struct {
		name       string
		middleware gin.HandlerFunc
		newRequest func() *http.Request
		wantPath   string
		wantModel  string
		wantPrompt string
	}{
		{
			name:       "kling text2video",
			middleware: KlingRequestConvert(),
			newRequest: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/kling/v1/videos/text2video",
					bytes.NewBufferString(`{"model_name":"kling-v1","prompt":"a cat walking","duration":"5"}`))
			},
			wantPath:   "/v1/video/generations",
			wantModel:  "kling-v1",
			wantPrompt: "a cat walking",
		},
		{
			name:       "jimeng submit",
			middleware: JimengRequestConvert(),
			newRequest: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/jimeng/?Action=CVSync2AsyncSubmitTask",
					bytes.NewBufferString(`{"req_key":"jimeng_vgfm_t2v_l20","prompt":"a cat walking"}`))
			},
			wantPath:   "/v1/video/generations",
			wantModel:  "jimeng_vgfm_t2v_l20",
			wantPrompt: "a cat walking",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			var downstream map[string]any
			var downstreamPath string
			engine.Use(tc.middleware)
			engine.Any("/*any", func(c *gin.Context) {
				require.NoError(t, common.UnmarshalBodyReusable(c, &downstream))
				downstreamPath = c.Request.URL.Path
				c.Status(http.StatusOK)
			})

			request := tc.newRequest()
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(httptest.NewRecorder(), request)

			assert.Equal(t, tc.wantPath, downstreamPath)
			assert.Equal(t, tc.wantModel, downstream["model"])
			assert.Equal(t, tc.wantPrompt, downstream["prompt"])
			// 厂商专有参数整体进 metadata，供适配器还原成上游请求体。
			assert.NotEmpty(t, downstream["metadata"])
		})
	}
}
