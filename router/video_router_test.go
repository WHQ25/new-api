package router

import (
	"net/http"
	"testing"

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
