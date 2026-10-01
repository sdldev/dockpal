package server

// Tests for the Dockge-style /api/stacks routes. These exercise the real
// handlers (registerStackRoutes) with the compose CLI backend swapped out:
// the docker package's stackCLI seam (including StackUpStreamed) is faked,
// and composecli.Available is a stubbable var, so no docker daemon or
// compose plugin is needed for any test in this file.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/composecli"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
)

// fakeCLI implements docker's unexported stackCLI shape via RegisterStackCLI.
type fakeCLI struct {
	mu       sync.Mutex
	runs     [][]string
	lsOut    string
	locked   map[string]bool
	upErr    error // when set, StackUpStreamed emits an error event and returns it
	upCalled chan struct{}
}

func (f *fakeCLI) Run(ctx context.Context, dir string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, append([]string{dir}, args...))
	return nil
}

func (f *fakeCLI) Output(ctx context.Context, dir string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	if strings.HasPrefix(joined, "ls") {
		return f.lsOut, nil
	}
	return "", nil
}

func (f *fakeCLI) TryLock(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locked == nil {
		f.locked = map[string]bool{}
	}
	if f.locked[name] {
		return false
	}
	f.locked[name] = true
	return true
}

func (f *fakeCLI) Unlock(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.locked, name)
}

// StackUpStreamed fakes a deploy: emits one progress line (plus an error when
// upErr is set), closes the session like the real producer, and signals
// upCalled so tests can wait for the background goroutine.
func (f *fakeCLI) StackUpStreamed(ctx context.Context, name string, session *docker.DeploySession) error {
	defer session.Close()
	session.Emit("up", "fake compose up", "running")
	if f.upErr != nil {
		session.Emit("up", f.upErr.Error(), "error")
		if f.upCalled != nil {
			close(f.upCalled)
		}
		return f.upErr
	}
	session.Emit("up", "Stack is up", "done")
	if f.upCalled != nil {
		close(f.upCalled)
	}
	return nil
}

// stackTestEnv wires a gin engine with the stack routes plus a fake CLI and
// a temp compose base dir.
func stackTestEnv(t *testing.T) (*gin.Engine, *fakeCLI) {
	t.Helper()
	r, fake, _ := stackTestEnvDB(t)
	return r, fake
}

func stackTestEnvDB(t *testing.T) (*gin.Engine, *fakeCLI, *db.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKPAL_DATA_DIR", dataDir)

	fake := &fakeCLI{}
	docker.RegisterStackCLI(fake)
	docker.InvalidateComposeLsCache()
	t.Cleanup(func() {
		docker.RegisterStackCLI(nil)
		docker.InvalidateComposeLsCache()
	})

	database := newTestDB(t)

	r := gin.New()
	viewer := r.Group("/api")
	operator := r.Group("/api")
	registerStackRoutes(viewer, operator, database)
	return r, fake, database
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestStackCreateGetUpdateDelete(t *testing.T) {
	r, _ := stackTestEnv(t)

	// Create
	w := doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
		"name":    "web",
		"compose": "services:\n  app:\n    image: nginx:latest\n",
		"env":     "TAG=latest\n",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	// Duplicate create → 409
	w = doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
		"name":    "web",
		"compose": "services:\n  app:\n    image: nginx:latest\n",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate create: %d %s", w.Code, w.Body.String())
	}

	// Get
	w = doJSON(t, r, http.MethodGet, "/api/stacks/web", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var stack docker.Stack
	if err := json.Unmarshal(w.Body.Bytes(), &stack); err != nil {
		t.Fatal(err)
	}
	if !stack.Managed || !strings.Contains(stack.ComposeYAML, "nginx") || stack.ComposeENV != "TAG=latest\n" {
		t.Errorf("unexpected stack: %+v", stack)
	}

	// Update
	w = doJSON(t, r, http.MethodPut, "/api/stacks/web", map[string]string{
		"compose": "services:\n  app:\n    image: nginx:1.27\n",
		"env":     "",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "1.27") {
		t.Errorf("update not reflected: %s", w.Body.String())
	}

	// List
	w = doJSON(t, r, http.MethodGet, "/api/stacks", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "web") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}

	// Delete
	w = doJSON(t, r, http.MethodDelete, "/api/stacks/web", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}

	// Gone
	w = doJSON(t, r, http.MethodGet, "/api/stacks/web", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d %s", w.Code, w.Body.String())
	}
}

// TestStackMutationsAreAudited verifies every successful stack mutation
// leaves an audit record (audit-stack-container H4) — and that the compose /
// env content never lands in the audit details.
func TestStackMutationsAreAudited(t *testing.T) {
	r, _, database := stackTestEnvDB(t)

	w := doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
		"name":    "web",
		"compose": "services:\n  app:\n    image: nginx:latest\n",
		"env":     "SECRET=hunter2\n",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	logs, total, err := database.ListAuditLogs(100, 0)
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if total == 0 {
		t.Fatal("expected at least one audit entry after stack create")
	}
	var found bool
	for _, entry := range logs {
		if entry.Action == "stack.create" && entry.Resource == "stacks/web" {
			found = true
			if strings.Contains(entry.Details, "hunter2") || strings.Contains(entry.Details, "nginx") {
				t.Fatalf("audit details leak stack content: %q", entry.Details)
			}
		}
	}
	if !found {
		t.Fatalf("no stack.create audit entry found in %+v", logs)
	}
}

func TestStackValidationErrors(t *testing.T) {
	r, _ := stackTestEnv(t)

	// Bad name
	w := doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
		"name":    "Bad Name",
		"compose": "services: {}\n",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad name: %d %s", w.Code, w.Body.String())
	}

	// Bad YAML
	w = doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
		"name":    "ok",
		"compose": "services: [nope]",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad yaml: %d %s", w.Code, w.Body.String())
	}

	// Update missing stack → 404
	w = doJSON(t, r, http.MethodPut, "/api/stacks/ghost", map[string]string{
		"compose": "services: {}\n",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("update ghost: %d %s", w.Code, w.Body.String())
	}
}

func TestStackActions(t *testing.T) {
	r, fake := stackTestEnv(t)

	w := doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
		"name":    "web",
		"compose": "services:\n  app:\n    image: nginx:latest\n",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}

	for _, action := range []string{"up", "start", "stop", "restart", "down", "update"} {
		w := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/stacks/web/%s", action), nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}

	// up/start/stop/restart/down ran once each; update ran pull (+no up, ls empty → not running)
	fake.mu.Lock()
	runCount := len(fake.runs)
	fake.mu.Unlock()
	if runCount < 6 {
		t.Fatalf("expected >=6 runs, got %d", runCount)
	}

	// Service action
	w = doJSON(t, r, http.MethodPost, "/api/stacks/web/services/app/restart", nil)
	if w.Code != http.StatusOK {
		t.Errorf("service restart: %d %s", w.Code, w.Body.String())
	}

	// Invalid service name rejected before hitting compose
	w = doJSON(t, r, http.MethodPost, "/api/stacks/web/services/bad%20name/restart", nil)
	if w.Code == http.StatusOK {
		t.Errorf("expected invalid service name rejection, got 200")
	}
}

func TestStackActionBusyConflict(t *testing.T) {
	r, fake := stackTestEnv(t)

	w := doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
		"name":    "web",
		"compose": "services:\n  app:\n    image: nginx:latest\n",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}

	fake.locked = map[string]bool{"web": true}
	w = doJSON(t, r, http.MethodPost, "/api/stacks/web/stop", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("busy stop: %d %s", w.Code, w.Body.String())
	}
}

func TestDeployStackReturnsSessionID(t *testing.T) {
	r, fake := stackTestEnv(t)
	fake.upCalled = make(chan struct{})

	w := doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
		"name":    "web",
		"compose": "services:\n  app:\n    image: nginx:latest\n",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}

	w = doJSON(t, r, http.MethodPost, "/api/stacks/web/deploy", map[string]any{
		"compose": "services:\n  app:\n    image: nginx:1.27\n",
		"env":     "",
		"is_add":  false,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("deploy: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		DeployID string `json:"deploy_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.DeployID, "deploy-") {
		t.Errorf("unexpected deploy_id: %q", res.DeployID)
	}

	// Wait for the background deploy goroutine to reach the (fake) CLI.
	select {
	case <-fake.upCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("deploy goroutine never reached StackUpStreamed")
	}

	// Compose was saved before deploy kicked off.
	stack, err := docker.GetStack("web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stack.ComposeYAML, "1.27") {
		t.Errorf("deploy should save compose first")
	}
}

// TestDeployStackEventSequence asserts the exact events a successful and a
// failed deploy emit — the regression net for the silent-failure fix (H1)
// and the never-closed stream fix (C1).
func TestDeployStackEventSequence(t *testing.T) {
	drainEvents := func(session *docker.DeploySession) []docker.DeployEvent {
		var events []docker.DeployEvent
		timeout := time.After(5 * time.Second)
		for {
			select {
			case ev := <-session.Events:
				events = append(events, ev)
			case <-session.Done:
				// Drain whatever remains after the producer closed.
				for {
					select {
					case ev := <-session.Events:
						events = append(events, ev)
					default:
						return events
					}
				}
			case <-timeout:
				t.Fatal("session never closed — stream would hang forever")
				return nil
			}
		}
	}

	setup := func(t *testing.T, upErr error) (*gin.Engine, *fakeCLI) {
		t.Helper()
		r, fake := stackTestEnv(t)
		fake.upErr = upErr
		fake.upCalled = make(chan struct{})
		w := doJSON(t, r, http.MethodPost, "/api/stacks", map[string]string{
			"name":    "web",
			"compose": "services:\n  app:\n    image: nginx:latest\n",
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("create: %d", w.Code)
		}
		return r, fake
	}

	deploy := func(t *testing.T, r *gin.Engine) string {
		t.Helper()
		w := doJSON(t, r, http.MethodPost, "/api/stacks/web/deploy", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("deploy: %d %s", w.Code, w.Body.String())
		}
		var res struct {
			DeployID string `json:"deploy_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return res.DeployID
	}

	t.Run("success emits progress then done", func(t *testing.T) {
		r, fake := setup(t, nil)
		id := deploy(t, r)
		session := globalDeployManager.GetSession(id)
		if session == nil {
			t.Fatal("session not registered")
		}
		<-fake.upCalled
		events := drainEvents(session)

		var hasProgress, hasDone, hasError bool
		for _, ev := range events {
			if ev.Status == "running" {
				hasProgress = true
			}
			if ev.Step == "done" && ev.Status == "done" {
				hasDone = true
			}
			if ev.Status == "error" {
				hasError = true
			}
		}
		if !hasProgress || !hasDone || hasError {
			t.Fatalf("success sequence wrong (progress=%v done=%v error=%v): %+v", hasProgress, hasDone, hasError, events)
		}
	})

	t.Run("failure emits error event (H1)", func(t *testing.T) {
		r, fake := setup(t, errors.New("compose exploded"))
		id := deploy(t, r)
		session := globalDeployManager.GetSession(id)
		if session == nil {
			t.Fatal("session not registered")
		}
		<-fake.upCalled
		events := drainEvents(session)

		var hasError bool
		for _, ev := range events {
			if ev.Status == "error" && strings.Contains(ev.Message, "compose exploded") {
				hasError = true
			}
		}
		if !hasError {
			t.Fatalf("failed deploy emitted no error event: %+v", events)
		}
	})
}

// TestStackRoutesReturn501WithoutComposeCLI covers the compose-plugin-missing
// path on every guarded handler — previously untestable because Available()
// shelled out for real (H6).
func TestStackRoutesReturn501WithoutComposeCLI(t *testing.T) {
	prev := composecli.Available
	composecli.Available = func() bool { return false }
	t.Cleanup(func() { composecli.Available = prev })

	r, _ := stackTestEnv(t)

	guarded := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/stacks"},
		{http.MethodGet, "/api/stacks/meta/networks"},
		{http.MethodGet, "/api/stacks/web"},
		{http.MethodDelete, "/api/stacks/web"},
		{http.MethodPost, "/api/stacks/web/deploy"},
		{http.MethodPost, "/api/stacks/web/up"},
		{http.MethodPost, "/api/stacks/web/services/app/restart"},
	}
	for _, g := range guarded {
		w := doJSON(t, r, g.method, g.path, nil)
		if w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: got %d, want 501", g.method, g.path, w.Code)
		}
	}
}
