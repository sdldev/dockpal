package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIDocsRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	api := r.Group("/api")
	api.GET("/docs/swagger.json", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.String(http.StatusOK, SwaggerJSON)
	})
	api.GET("/docs", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, "mock-redoc-html")
	})

	// Test swagger.json
	t.Run("swagger.json", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/docs/swagger.json", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", w.Header().Get("Content-Type"))
		}
		if !strings.Contains(w.Body.String(), "Dockpal API") {
			t.Errorf("expected body to contain Dockpal API")
		}
	})

	// Test docs UI
	t.Run("docs UI", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/docs", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
		if w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("expected text/html; charset=utf-8, got %s", w.Header().Get("Content-Type"))
		}
		if w.Body.String() != "mock-redoc-html" {
			t.Errorf("expected mock-redoc-html, got %s", w.Body.String())
		}
	})
}
