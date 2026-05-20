package server

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRouteRegistrationDoesNotPanic ensures that registering both the
// /instances/:instance_id CRUD group and the nested
// /instances/:instance_id/... scoped group on the same engine does not
// trigger gin's "wildcard conflict" panic. It does not exercise handler
// behavior; it only validates the route tree is well-formed.
func TestRouteRegistrationDoesNotPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")

	// CRUD endpoints under /api/instances
	api.GET("/instances", func(c *gin.Context) {})
	api.POST("/instances", func(c *gin.Context) {})
	api.GET("/instances/:instance_id", func(c *gin.Context) {})
	api.PUT("/instances/:instance_id", func(c *gin.Context) {})
	api.DELETE("/instances/:instance_id", func(c *gin.Context) {})
	api.POST("/instances/:instance_id/test", func(c *gin.Context) {})
	api.POST("/instances/:instance_id/rotate-token", func(c *gin.Context) {})

	// Scoped group nested under the same prefix
	scoped := api.Group("/instances/:instance_id")
	RegisterInstanceScopedRoutes(scoped)
}
