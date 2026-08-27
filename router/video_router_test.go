package router

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Ark inbound routes live under /api, which is otherwise owned by the
// dashboard API group. Registering both must not collide in gin's route tree.
func TestArkVideoRoutesRegisterAlongsideApiRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	require.NotPanics(t, func() {
		SetApiRouter(engine)
		SetVideoRouter(engine)
	})

	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	assert.True(t, registered[http.MethodPost+" /api/v3/contents/generations/tasks"])
	assert.True(t, registered[http.MethodGet+" /api/v3/contents/generations/tasks/:task_id"])
}

// 模型广场把 ark-video 的默认 path/method 直接渲染进调用示例。common 不能依赖
// middleware，那条路径是抄过去的字面量——它必须真的指向一条已注册的路由，否则
// 客户会照着示例打到 404。
func TestArkVideoDefaultEndpointMatchesRegisteredRoute(t *testing.T) {
	info, ok := common.GetDefaultEndpointInfo(constant.EndpointTypeArkVideo)
	require.True(t, ok, "ark-video has no default endpoint info")

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetVideoRouter(engine)

	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	assert.True(t, registered[info.Method+" "+info.Path],
		"ark-video advertises %s %s but no such route is registered", info.Method, info.Path)
}

// 可灵 3.0 官方协议的入站路由挂在根路径上（官方把型号编在路径里），必须与仪表盘
// 和其它 relay 路由共存而不冲突。
func TestKlingV3RoutesRegisterAlongsideApiRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	require.NotPanics(t, func() {
		SetApiRouter(engine)
		SetVideoRouter(engine)
	})

	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	assert.True(t, registered[http.MethodPost+" /text-to-video/kling-3.0"])
	assert.True(t, registered[http.MethodPost+" /image-to-video/kling-3.0"])
	assert.True(t, registered[http.MethodPost+" /omni-video/kling-3.0-omni"])
	assert.True(t, registered[http.MethodGet+" /tasks"])
}

// 模型广场把 kling-video 的默认 path/method 直接渲染进调用示例。common 不能依赖
// middleware，那条路径是抄过去的字面量——它必须真的指向一条已注册的路由。
func TestKlingV3DefaultEndpointMatchesRegisteredRoute(t *testing.T) {
	info, ok := common.GetDefaultEndpointInfo(constant.EndpointTypeKlingVideo)
	require.True(t, ok, "kling-video has no default endpoint info")

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NotPanics(t, func() { SetVideoRouter(engine) })

	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	assert.True(t, registered[info.Method+" "+info.Path],
		"default endpoint %s %s is not a registered route", info.Method, info.Path)
}
