package server

import (
	"net/http"
	"net/http/httptest"
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
	registerAPIVersionCompatibility(r)
	api := r.Group("/api")
	api.Use(legacyAPIWarningMiddleware())

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

func TestAPIV1CompatibilityDispatchesToLegacyRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerAPIVersionCompatibility(r)
	api := r.Group("/api")
	api.Use(legacyAPIWarningMiddleware())
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	legacy := httptest.NewRecorder()
	legacyReq := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	r.ServeHTTP(legacy, legacyReq)
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy status = %d, want %d", legacy.Code, http.StatusOK)
	}
	if legacy.Header().Get("Warning") == "" {
		t.Fatal("legacy route missing Warning header")
	}

	versioned := httptest.NewRecorder()
	versionedReq := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	r.ServeHTTP(versioned, versionedReq)
	if versioned.Code != http.StatusOK {
		t.Fatalf("versioned status = %d, want %d", versioned.Code, http.StatusOK)
	}
	if versioned.Header().Get("Warning") != "" {
		t.Fatal("versioned route should not include legacy Warning header")
	}
}
