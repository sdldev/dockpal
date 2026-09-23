package server

// Tests for the instance-scoped stack routes. Handlers depend on the narrow
// stackClient interface, so a fake is injected directly into the gin context
// (bypassing InstanceMiddleware) — no agent, docker daemon or compose CLI
// needed.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/docker"
)

type fakeStackClient struct {
	stacks   []docker.Stack
	stack    *docker.Stack
	saveErr  error
	actErr   error
	delErr   error
	networks []string
	globalEnv string

	savedName   string
	savedYAML   string
	savedENV    string
	savedIsAdd  bool
	lastAction  string
	lastService string
	deployCalls int
}

func (f *fakeStackClient) ListStacks(ctx context.Context) ([]docker.Stack, error) {
	return f.stacks, nil
}

func (f *fakeStackClient) GetStack(ctx context.Context, name string) (*docker.Stack, error) {
	if f.stack == nil {
		return nil, errors.New(`stack "` + name + `" not found`)
	}
	return f.stack, nil
}

func (f *fakeStackClient) SaveStack(ctx context.Context, name, composeYAML, composeENV string, isAdd bool) (*docker.Stack, error) {
	f.savedName, f.savedYAML, f.savedENV, f.savedIsAdd = name, composeYAML, composeENV, isAdd
	if f.saveErr != nil {
		return nil, f.saveErr
	}
	return &docker.Stack{Name: name, Managed: true, ComposeYAML: composeYAML, ComposeENV: composeENV}, nil
}

func (f *fakeStackClient) DeleteStack(ctx context.Context, name string) error {
	return f.delErr
}

func (f *fakeStackClient) StackAction(ctx context.Context, name, action string) (*docker.Stack, error) {
	f.lastAction = action
	if f.actErr != nil {
		return nil, f.actErr
	}
	return &docker.Stack{Name: name, Status: docker.StackStatusRunning}, nil
}

func (f *fakeStackClient) StackServiceAction(ctx context.Context, name, service, action string) (*docker.Stack, error) {
	f.lastService = service
	f.lastAction = action
	if f.actErr != nil {
		return nil, f.actErr
	}
	return &docker.Stack{Name: name, Status: docker.StackStatusRunning}, nil
}

func (f *fakeStackClient) DeployStackStreamed(ctx context.Context, name, composeYAML, composeENV string, isAdd bool, session *docker.DeploySession) error {
	f.deployCalls++
	session.Emit("up", "deploying "+name, "running")
	return nil
}

func (f *fakeStackClient) ListDockerNetworks(ctx context.Context) ([]string, error) {
	return f.networks, nil
}

func (f *fakeStackClient) GetGlobalEnv(ctx context.Context) (string, error) {
	return f.globalEnv, nil
}

func (f *fakeStackClient) SetGlobalEnv(ctx context.Context, content string) error {
	f.globalEnv = content
	return nil
}

func instanceStackRouter(t *testing.T, fake stackClient) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/instances/test")
	g.Use(func(c *gin.Context) {
		c.Set("agent_client", fake)
		c.Set("role", "admin")
		c.Next()
	})
	registerInstanceStackRoutes(g)
	return r
}

func doInstanceJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(b))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInstanceStacksCRUD(t *testing.T) {
	fake := &fakeStackClient{
		stacks: []docker.Stack{{Name: "web", Status: "draft"}},
		stack:  &docker.Stack{Name: "web", Status: "running", Managed: true},
	}
	r := instanceStackRouter(t, fake)

	w := doInstanceJSON(t, r, http.MethodGet, "/api/instances/test/stacks", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "web") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}

	w = doInstanceJSON(t, r, http.MethodGet, "/api/instances/test/stacks/web", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}

	w = doInstanceJSON(t, r, http.MethodPost, "/api/instances/test/stacks", map[string]string{
		"name": "web", "compose": "services: {}\n", "env": "A=1\n",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if fake.savedName != "web" || !fake.savedIsAdd || fake.savedENV != "A=1\n" {
		t.Errorf("save args: %+v", fake)
	}

	w = doInstanceJSON(t, r, http.MethodPut, "/api/instances/test/stacks/web", map[string]string{
		"compose": "services: {}\n# v2",
	})
	if w.Code != http.StatusOK || fake.savedIsAdd {
		t.Fatalf("update: %d isAdd=%v", w.Code, fake.savedIsAdd)
	}

	w = doInstanceJSON(t, r, http.MethodDelete, "/api/instances/test/stacks/web", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

func TestInstanceStacksErrorTaxonomy(t *testing.T) {
	fake := &fakeStackClient{}
	r := instanceStackRouter(t, fake)

	// GetStack on nil stack → "not found" → 404
	w := doInstanceJSON(t, r, http.MethodGet, "/api/instances/test/stacks/ghost", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get ghost: %d %s", w.Code, w.Body.String())
	}

	// Save conflict → 409
	fake.saveErr = errors.New(`stack "web" already exists`)
	w = doInstanceJSON(t, r, http.MethodPost, "/api/instances/test/stacks", map[string]string{
		"name": "web", "compose": "services: {}\n",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body.String())
	}

	// Busy → 409
	fake.saveErr = nil
	fake.actErr = errors.New("another operation is already running for this stack")
	w = doInstanceJSON(t, r, http.MethodPost, "/api/instances/test/stacks/web/stop", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("busy: %d %s", w.Code, w.Body.String())
	}

	// Invalid → 400
	fake.actErr = errors.New("invalid compose YAML")
	w = doInstanceJSON(t, r, http.MethodPost, "/api/instances/test/stacks/web/up", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid: %d %s", w.Code, w.Body.String())
	}
}

func TestInstanceStackActionsAndServices(t *testing.T) {
	fake := &fakeStackClient{}
	r := instanceStackRouter(t, fake)

	for _, action := range []string{"up", "start", "stop", "restart", "down", "update"} {
		w := doInstanceJSON(t, r, http.MethodPost, "/api/instances/test/stacks/web/"+action, nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s: %d %s", action, w.Code, w.Body.String())
		}
		if fake.lastAction != action {
			t.Errorf("lastAction = %q, want %q", fake.lastAction, action)
		}
	}

	w := doInstanceJSON(t, r, http.MethodPost, "/api/instances/test/stacks/web/services/db/restart", nil)
	if w.Code != http.StatusOK || fake.lastService != "db" {
		t.Fatalf("service restart: %d svc=%q", w.Code, fake.lastService)
	}

	// Invalid service name rejected
	w = doInstanceJSON(t, r, http.MethodPost, "/api/instances/test/stacks/web/services/bad%20name/restart", nil)
	if w.Code == http.StatusOK {
		t.Errorf("expected invalid service rejection")
	}
}

func TestInstanceDeployStack(t *testing.T) {
	fake := &fakeStackClient{}
	r := instanceStackRouter(t, fake)

	w := doInstanceJSON(t, r, http.MethodPost, "/api/instances/test/stacks/web/deploy", map[string]any{
		"compose": "services: {}\n", "env": "", "is_add": true,
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
}

func TestInstanceStackMeta(t *testing.T) {
	fake := &fakeStackClient{networks: []string{"net1", "net2"}, globalEnv: "G=1\n"}
	r := instanceStackRouter(t, fake)

	w := doInstanceJSON(t, r, http.MethodGet, "/api/instances/test/stacks/meta/networks", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "net1") {
		t.Fatalf("networks: %d %s", w.Code, w.Body.String())
	}

	w = doInstanceJSON(t, r, http.MethodGet, "/api/instances/test/stacks/meta/globalenv", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "G=1") {
		t.Fatalf("globalenv get: %d %s", w.Code, w.Body.String())
	}

	w = doInstanceJSON(t, r, http.MethodPut, "/api/instances/test/stacks/meta/globalenv", map[string]string{
		"content": "G=2\n",
	})
	if w.Code != http.StatusOK || fake.globalEnv != "G=2\n" {
		t.Fatalf("globalenv set: %d %q", w.Code, fake.globalEnv)
	}
}