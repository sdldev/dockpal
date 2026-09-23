package server

// Tests for the Dockge-style /api/stacks routes. These exercise the real
// handlers (registerStackRoutes) with the compose CLI backend swapped out:
// composecli.Available is short-circuited via a dockerCLI stub and the
// docker package's stackCLI seam is faked, so no docker daemon or compose
// plugin is needed.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/docker"
)

// fakeCLI implements docker's unexported stackCLI shape via RegisterStackCLI.
type fakeCLI struct {
	mu     sync.Mutex
	runs   [][]string
	lsOut  string
	locked map[string]bool
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

// stackTestEnv wires a gin engine with the stack routes plus a fake CLI and
// a temp compose base dir.
func stackTestEnv(t *testing.T) (*gin.Engine, *fakeCLI) {
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
	t.Cleanup(func() { docker.RegisterStackCLI(nil) })

	r := gin.New()
	viewer := r.Group("/api")
	operator := r.Group("/api")
	registerStackRoutes(viewer, operator)
	return r, fake
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
	r, _ := stackTestEnv(t)

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

	// Compose was saved before deploy kicked off.
	stack, err := docker.GetStack("web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stack.ComposeYAML, "1.27") {
		t.Errorf("deploy should save compose first")
	}
}
