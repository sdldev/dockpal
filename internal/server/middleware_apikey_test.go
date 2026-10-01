package server

// Tests for the X-API-Key authentication path (audit-auth T3) — previously
// the only credential type with zero test coverage.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
)

func setupAPIKeyEngine(t *testing.T, database *db.DB) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.Use(AuthMiddleware("test-secret", database))
	r.GET("/api/whoami", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"username": c.GetString("username"),
			"role":     c.GetString("role"),
		})
	})
	// Role-gated echo to prove the key's role flows into RequireRole.
	admin := r.Group("/api/admin")
	admin.Use(RequireRole(auth.RoleAdmin))
	admin.GET("/echo", func(c *gin.Context) { c.String(http.StatusOK, "admin-ok") })
	return r
}

func apiKeyRequest(t *testing.T, r *gin.Engine, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAuthMiddleware_APIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newTestDB(t)

	const rawKey = "dpk-test-key-123"
	if err := database.SaveAPIKey(db.APIKey{
		ID:      "key-1",
		Name:    "ci-bot",
		KeyHash: auth.HashAPIKey(rawKey),
		Role:    "admin",
	}); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	t.Run("valid key authenticates with stored role", func(t *testing.T) {
		rec := apiKeyRequest(t, setupAPIKeyEngine(t, database), "/api/whoami", rawKey)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !contains(body, "api-key:ci-bot") || !contains(body, "admin") {
			t.Fatalf("unexpected identity: %s", body)
		}
	})

	t.Run("valid key role flows into RequireRole", func(t *testing.T) {
		rec := apiKeyRequest(t, setupAPIKeyEngine(t, database), "/api/admin/echo", rawKey)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown key → 401", func(t *testing.T) {
		rec := apiKeyRequest(t, setupAPIKeyEngine(t, database), "/api/whoami", "dpk-does-not-exist")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("hash mismatch → 401", func(t *testing.T) {
		// Same length/charset as the real key but different content →
		// different SHA-256, no match.
		rec := apiKeyRequest(t, setupAPIKeyEngine(t, database), "/api/whoami", "dpk-test-key-999")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("viewer-role key rejected from admin route", func(t *testing.T) {
		if err := database.SaveAPIKey(db.APIKey{
			ID:      "key-2",
			Name:    "readonly",
			KeyHash: auth.HashAPIKey("dpk-viewer-key"),
			Role:    "viewer",
		}); err != nil {
			t.Fatalf("seed viewer key: %v", err)
		}
		rec := apiKeyRequest(t, setupAPIKeyEngine(t, database), "/api/admin/echo", "dpk-viewer-key")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("deleted key fails", func(t *testing.T) {
		if err := database.DeleteAPIKey("key-1"); err != nil {
			t.Fatalf("delete key: %v", err)
		}
		rec := apiKeyRequest(t, setupAPIKeyEngine(t, database), "/api/whoami", rawKey)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 after deletion", rec.Code)
		}
	})

	t.Run("no credentials at all → 401", func(t *testing.T) {
		rec := apiKeyRequest(t, setupAPIKeyEngine(t, database), "/api/whoami", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}
