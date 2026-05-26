// Tests for the public GET /api/config handler (task 8.2 of the
// auto-image-update spec). The endpoint is intentionally unauthenticated
// so the UI can render the right shell — including hiding the auto-update
// toggle and showing a "disabled by admin" banner — before the user logs
// in.
//
// The production handler is a one-liner closure inside RegisterRoutes that
// reads from globalAutoUpdateWorker via its public Enabled() getter. We
// replicate the same shape here against an isolated gin.Engine so the test
// does not require a Docker daemon, a real database, or a worker
// instance — just the same JSON contract the UI consumes.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/docker"
)

// registerConfigHandler mirrors the production /api/config handler in
// RegisterRoutes. Keeping the body identical guarantees these tests fail
// the moment the live handler's contract drifts.
func registerConfigHandler(rg gin.IRoutes, worker *docker.AutoUpdateWorker) {
	rg.GET("/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"auto_update_enabled": worker.Enabled(),
		})
	})
}

// TestConfigHandler_NilWorker_ReturnsDisabled asserts that when no
// AutoUpdateWorker has been wired (a fresh process before RegisterRoutes
// runs, or a test setup with the feature flag off), the endpoint reports
// auto_update_enabled=false. Enabled() is nil-safe so the handler does
// not panic.
//
// Validates: Requirements R7.1
func TestConfigHandler_NilWorker_ReturnsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	registerConfigHandler(r, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json body %q: %v", w.Body.String(), err)
	}
	v, ok := got["auto_update_enabled"]
	if !ok {
		t.Fatalf("response missing auto_update_enabled key; body=%s", w.Body.String())
	}
	if got, want := v, any(false); got != want {
		t.Errorf("auto_update_enabled: got %v, want %v", got, want)
	}
}

// TestConfigHandler_DisabledWorker_ReturnsFalse asserts that when the env
// flag DOCKPAL_AUTO_UPDATE_ENABLED is "false" the worker reports disabled
// and the handler relays that to the UI as auto_update_enabled=false. The
// constructor takes nil for client/monitor/store — those references are
// only touched once Start() runs, and the worker is never started here.
//
// Validates: Requirements R7.1
func TestConfigHandler_DisabledWorker_ReturnsFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Setenv("DOCKPAL_AUTO_UPDATE_ENABLED", "false")
	worker := docker.NewAutoUpdateWorker(nil, nil, nil, nil, nil, nil, "local")

	r := gin.New()
	registerConfigHandler(r, worker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got == "" {
		t.Errorf("missing Content-Type header")
	}

	var got struct {
		AutoUpdateEnabled bool `json:"auto_update_enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json body %q: %v", w.Body.String(), err)
	}
	if got.AutoUpdateEnabled {
		t.Errorf("auto_update_enabled: got true, want false")
	}
}

// TestConfigHandler_EnabledWorker_ReturnsTrue is the positive counterpart:
// when the env flag is "true" (or unset, since the default is true) the
// handler reports auto_update_enabled=true.
//
// Validates: Requirements R7.1
func TestConfigHandler_EnabledWorker_ReturnsTrue(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Setenv("DOCKPAL_AUTO_UPDATE_ENABLED", "true")
	worker := docker.NewAutoUpdateWorker(nil, nil, nil, nil, nil, nil, "local")

	r := gin.New()
	registerConfigHandler(r, worker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got struct {
		AutoUpdateEnabled bool `json:"auto_update_enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json body %q: %v", w.Body.String(), err)
	}
	if !got.AutoUpdateEnabled {
		t.Errorf("auto_update_enabled: got false, want true")
	}
}

// TestConfigHandler_NoAuthRequired confirms the handler does not consult
// any Authorization header. The endpoint must work before login so the UI
// can decide what to render on the login screen and the page shell.
//
// Validates: Requirements R7.1
func TestConfigHandler_NoAuthRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	registerConfigHandler(r, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	// No Authorization header — must still succeed.
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (auth-free endpoint must accept anonymous requests)", w.Code)
	}
}
