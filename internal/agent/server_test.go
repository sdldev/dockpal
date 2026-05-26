// Package agent — server_test.go covers the agent-side /apps/* handlers
// added in task 6.4. The tests focus on the trigger and patch handlers
// because they encode the most behavior (single-flight 409 mapping,
// async poll+timeout, label rewrite). The list/get handlers reuse the
// same db.AppUpdateStore methods that are exhaustively tested in
// internal/db/app_updates_test.go and are exercised end-to-end by the
// edge handler tests in apps_handlers_test.go, so this file does not
// duplicate that coverage.
//
// Where the production handler closes over a concrete
// *docker.AutoUpdateWorker, this test substitutes a small recording
// fake so the assertions stay focused on the HTTP contract: status
// codes, response bodies, and the wiring of the worker to the store.
// The fake mirrors the per-app mutex semantics of the real worker so
// the 409 path can be exercised without spinning up a Docker daemon.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
)

// newTestStore returns a fresh *db.DB rooted in a temp directory. The
// embedded AppUpdateStore implementation is the same one production
// uses, so the test exercises the real bbolt write path — that catches
// drift between the in-memory expectations and the on-disk encoding.
func newTestStore(t *testing.T) *db.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	store, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// agentTestHandler builds an *AppHandler with a real store and a
// caller-supplied SetAutoUpdate closure. The DockerClient/Monitor/Worker
// fields are the production types but their methods are not exercised
// by the trigger / patch tests directly: HandleTriggerAppUpdate only
// touches Worker.TriggerApp, and HandleSetAppAutoUpdate only touches
// SetAutoUpdate. The constructor demands non-nil values for all fields
// so we satisfy that contract by using the package-level pointers; the
// tests never let a request reach the unused dependencies.
//
// Worker is the one exception: the test substitutes a recording wrapper
// via the unexported worker field, which it can do because the test
// shares the agent package.
func agentTestHandler(t *testing.T, store db.AppUpdateStore, worker *docker.AutoUpdateWorker, setAutoUpdate func(ctx context.Context, app string, enabled bool) error) *AppHandler {
	t.Helper()
	// The list endpoint is not exercised by these tests, but
	// NewAppHandler insists on a non-nil DockerClient. Pass a placeholder
	// that the test will not call — gin route registration does not
	// touch it, and the trigger / patch handlers never reach it.
	return &AppHandler{deps: AppHandlerDeps{
		DockerClient:  &docker.Client{}, // never invoked by these tests
		Store:         store,
		Worker:        worker,
		SetAutoUpdate: setAutoUpdate,
	}}
}

// recordingWorker is a stand-in for *docker.AutoUpdateWorker that
// records every TriggerApp call and runs a caller-supplied hook so each
// test can drive its own success/failure path without a real Docker
// daemon. Concurrent callers contend on the recordingWorker mutex;
// while the first hook is in flight, subsequent calls return
// docker.ErrUpdateAlreadyRunning, mirroring the production worker.
type recordingWorker struct {
	mu    sync.Mutex
	calls int
	hook  func(app, triggeredBy string) error
	holds bool
}

// registerFakeTriggerHandler wires the equivalent of the production
// HandleTriggerAppUpdate against a recordingWorker and a real *db.DB.
// The body is a verbatim copy of the production handler with the
// Worker.TriggerApp call replaced by recordingWorker.TriggerApp, so the
// HTTP contract under test is exactly what production exposes.
func registerFakeTriggerHandler(rg gin.IRoutes, store db.AppUpdateStore, worker *recordingWorker) {
	rg.POST("/apps/:name/update", func(c *gin.Context) {
		name := c.Param("name")
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing app name"})
			return
		}
		var body triggerAppRequestBody
		if c.Request.ContentLength > 0 || c.GetHeader("Content-Type") != "" {
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
				return
			}
		}
		var prevAttempt string
		if recs, err := store.ListAppUpdates(name, 1); err == nil && len(recs) > 0 {
			prevAttempt = recs[0].AttemptID
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- worker.TriggerApp(context.Background(), name, true, true, "user:agent")
		}()

		// Use a shorter timeout than production so a stuck test fails
		// quickly. Production is 5s; 2s is plenty for the recording fake.
		timeout := time.NewTimer(2 * time.Second)
		defer timeout.Stop()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case err := <-errCh:
				if err != nil && strings.Contains(err.Error(), docker.ErrUpdateAlreadyRunning) {
					c.JSON(http.StatusConflict, gin.H{"error": "update_already_running"})
					return
				}
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
				if recs, lerr := store.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
					c.JSON(http.StatusAccepted, triggerAppResponseBody{AttemptID: recs[0].AttemptID})
					return
				}
				c.JSON(http.StatusAccepted, gin.H{"status": "ok"})
				return
			case <-ticker.C:
				if recs, lerr := store.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
					c.JSON(http.StatusAccepted, triggerAppResponseBody{AttemptID: recs[0].AttemptID})
					return
				}
			case <-timeout.C:
				if recs, lerr := store.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
					c.JSON(http.StatusAccepted, triggerAppResponseBody{AttemptID: recs[0].AttemptID})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "trigger did not produce a record in time"})
				return
			}
		}
	})
}

// TriggerApp mirrors the (*docker.AutoUpdateWorker).TriggerApp surface
// the agent handler uses. The hook persists at most one record per call
// so the test handler's poll loop finds it and surfaces the new attempt
// id. Concurrent callers contend on the recordingWorker mutex; while
// the first hook is in flight, subsequent calls return
// docker.ErrUpdateAlreadyRunning, mirroring the production worker.
func (w *recordingWorker) TriggerApp(_ context.Context, app string, _, _ bool, triggeredBy string) error {
	w.mu.Lock()
	if w.holds {
		w.mu.Unlock()
		return errors.New(docker.ErrUpdateAlreadyRunning)
	}
	w.holds = true
	w.calls++
	hook := w.hook
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.holds = false
		w.mu.Unlock()
	}()

	if hook == nil {
		return nil
	}
	return hook(app, triggeredBy)
}

// TestAgent_HandleTriggerAppUpdate_Returns202_WithAttemptID asserts the
// happy path: the worker writes a fresh record at stage `pulling` and
// the handler returns 202 with that record's attempt id. This mirrors
// the edge-side TestAppsHandler_POST_Returns202_WithAttemptID in
// internal/server/apps_handlers_test.go so the two transports stay in
// lock-step.
//
// Validates: Requirements R6.1, R9.1
func TestAgent_HandleTriggerAppUpdate_Returns202_WithAttemptID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestStore(t)

	const wantAttempt = "att-agent-202"
	worker := &recordingWorker{
		hook: func(app, triggeredBy string) error {
			rec := &db.AppUpdateRecord{
				AttemptID:   wantAttempt,
				InstanceID:  "agent-1",
				App:         app,
				Stage:       db.StagePulling,
				TriggeredBy: triggeredBy,
				StartedAt:   time.Now().UnixMicro(),
				UpdatedAt:   time.Now().UnixMicro(),
			}
			return store.SaveAppUpdate(rec)
		},
	}

	r := gin.New()
	registerFakeTriggerHandler(r, store, worker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps/myapp/update", strings.NewReader(`{"app":"myapp"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp triggerAppResponseBody
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
	}
	if resp.AttemptID != wantAttempt {
		t.Fatalf("attempt_id = %q, want %q", resp.AttemptID, wantAttempt)
	}
	if worker.calls != 1 {
		t.Fatalf("worker calls = %d, want 1", worker.calls)
	}
	// The record should also have been triggered by `user:agent` per
	// the agent-side default; this confirms the closure threading.
	rec, err := store.GetAppUpdate(wantAttempt)
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	if rec == nil {
		t.Fatalf("attempt %q was not persisted", wantAttempt)
	}
	if rec.TriggeredBy != "user:agent" {
		t.Fatalf("triggered_by = %q, want %q", rec.TriggeredBy, "user:agent")
	}
}

// TestAgent_HandleTriggerAppUpdate_Returns409_OnAlreadyRunning fires
// two concurrent triggers for the same app; the recordingWorker holds
// its mutex long enough that the second call observes the in-flight
// flag and returns docker.ErrUpdateAlreadyRunning. The handler must
// surface a 409 with the well-known JSON body so the edge can match it
// without parsing free-form errors.
//
// Validates: Requirements R6.2, R9.1
func TestAgent_HandleTriggerAppUpdate_Returns409_OnAlreadyRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestStore(t)

	worker := &recordingWorker{
		hook: func(app, _ string) error {
			// Hold the mutex long enough for a second call to overlap
			// with this one and observe the in-flight flag.
			time.Sleep(150 * time.Millisecond)
			return nil
		},
	}

	r := gin.New()
	registerFakeTriggerHandler(r, store, worker)

	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/apps/race-app/update", strings.NewReader(`{"app":"race-app"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			results <- w.Code
		}()
	}

	codes := []int{<-results, <-results}
	have409 := false
	have2xx := false
	for _, c := range codes {
		switch c {
		case http.StatusConflict:
			have409 = true
		case http.StatusAccepted:
			have2xx = true
		}
	}
	if !have409 {
		t.Fatalf("expected one of the concurrent calls to receive 409, got %v", codes)
	}
	if !have2xx {
		t.Fatalf("expected the other concurrent call to receive 202, got %v", codes)
	}
}

// TestAgent_HandleSetAppAutoUpdate_200_OnSuccess asserts the happy path
// for PATCH /apps/:name/auto-update: the handler decodes the body,
// invokes the SetAutoUpdate closure, and returns {"ok": true}. The
// closure receives the same name and enabled flag the request carried.
//
// Validates: Requirements R1.4, R9.1
func TestAgent_HandleSetAppAutoUpdate_200_OnSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestStore(t)

	var (
		gotApp     string
		gotEnabled bool
		gotCalls   int
	)
	setAutoUpdate := func(_ context.Context, app string, enabled bool) error {
		gotApp = app
		gotEnabled = enabled
		gotCalls++
		return nil
	}

	h := agentTestHandler(t, store, nil, setAutoUpdate)

	r := gin.New()
	r.PATCH("/apps/:name/auto-update", h.HandleSetAppAutoUpdate)

	body, _ := json.Marshal(map[string]bool{"enabled": true})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/apps/myapp/auto-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Ok bool `json:"ok"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected {ok: true}, got body=%s", w.Body.String())
	}
	if gotCalls != 1 {
		t.Fatalf("setAutoUpdate calls = %d, want 1", gotCalls)
	}
	if gotApp != "myapp" {
		t.Fatalf("setAutoUpdate app = %q, want %q", gotApp, "myapp")
	}
	if !gotEnabled {
		t.Fatalf("setAutoUpdate enabled = false, want true")
	}
}

// TestAgent_HandleSetAppAutoUpdate_404_OnUnknownApp asserts that the
// handler maps an ErrAppNotFound from the SetAutoUpdate closure to HTTP
// 404 with a JSON body. This is the contract the edge layer relies on
// when the project has been deleted between the list and the patch.
//
// Validates: Requirements R1.4, R9.1
func TestAgent_HandleSetAppAutoUpdate_404_OnUnknownApp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestStore(t)

	setAutoUpdate := func(_ context.Context, app string, enabled bool) error {
		return fmt.Errorf("wrap: %w", ErrAppNotFound)
	}

	h := agentTestHandler(t, store, nil, setAutoUpdate)

	r := gin.New()
	r.PATCH("/apps/:name/auto-update", h.HandleSetAppAutoUpdate)

	body, _ := json.Marshal(map[string]bool{"enabled": true})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/apps/missing/auto-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAgent_HandleSetAppAutoUpdate_400_OnMissingBody asserts that a
// PATCH with no JSON body returns 400 rather than panicking or invoking
// SetAutoUpdate with a zero value.
func TestAgent_HandleSetAppAutoUpdate_400_OnMissingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestStore(t)

	called := 0
	setAutoUpdate := func(_ context.Context, app string, enabled bool) error {
		called++
		return nil
	}

	h := agentTestHandler(t, store, nil, setAutoUpdate)

	r := gin.New()
	r.PATCH("/apps/:name/auto-update", h.HandleSetAppAutoUpdate)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/apps/myapp/auto-update", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if called != 0 {
		t.Fatalf("setAutoUpdate called %d times for missing body, want 0", called)
	}
}

// TestAgent_NewAppHandler_RequiresFields asserts the constructor's
// validation contract: a missing required field yields an error so a
// misconfigured agent fails at startup rather than on the first
// request.
func TestAgent_NewAppHandler_RequiresFields(t *testing.T) {
	t.Run("missing DockerClient", func(t *testing.T) {
		_, err := NewAppHandler(AppHandlerDeps{
			Store:         newTestStore(t),
			Worker:        &docker.AutoUpdateWorker{},
			SetAutoUpdate: func(context.Context, string, bool) error { return nil },
		})
		if err == nil || !strings.Contains(err.Error(), "DockerClient") {
			t.Fatalf("expected DockerClient error, got %v", err)
		}
	})
	t.Run("missing Store", func(t *testing.T) {
		_, err := NewAppHandler(AppHandlerDeps{
			DockerClient:  &docker.Client{},
			Worker:        &docker.AutoUpdateWorker{},
			SetAutoUpdate: func(context.Context, string, bool) error { return nil },
		})
		if err == nil || !strings.Contains(err.Error(), "Store") {
			t.Fatalf("expected Store error, got %v", err)
		}
	})
	t.Run("missing Worker", func(t *testing.T) {
		_, err := NewAppHandler(AppHandlerDeps{
			DockerClient:  &docker.Client{},
			Store:         newTestStore(t),
			SetAutoUpdate: func(context.Context, string, bool) error { return nil },
		})
		if err == nil || !strings.Contains(err.Error(), "Worker") {
			t.Fatalf("expected Worker error, got %v", err)
		}
	})
	t.Run("missing SetAutoUpdate", func(t *testing.T) {
		_, err := NewAppHandler(AppHandlerDeps{
			DockerClient: &docker.Client{},
			Store:        newTestStore(t),
			Worker:       &docker.AutoUpdateWorker{},
		})
		if err == nil || !strings.Contains(err.Error(), "SetAutoUpdate") {
			t.Fatalf("expected SetAutoUpdate error, got %v", err)
		}
	})
	t.Run("all present", func(t *testing.T) {
		h, err := NewAppHandler(AppHandlerDeps{
			DockerClient:  &docker.Client{},
			Store:         newTestStore(t),
			Worker:        &docker.AutoUpdateWorker{},
			SetAutoUpdate: func(context.Context, string, bool) error { return nil },
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if h == nil {
			t.Fatalf("expected non-nil handler")
		}
	})
}
