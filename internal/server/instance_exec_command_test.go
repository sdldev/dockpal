// Tests for the instance-scoped one-shot exec command route
// (POST /api/instances/:instance_id/containers/:id/exec). Handlers are
// exercised directly through a gin context seeded with a fake AgentClient
// (bypassing InstanceMiddleware) — no docker daemon or agent required.
package server

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

// fakeExecAgentClient satisfies the slice of AgentClient the exec handler
// touches: InspectContainer (protection check) and ExecCommand.
type fakeExecAgentClient struct {
	protectionFakeAgentClient // embed the existing full fake; override what we assert on
	inspectDetail             *docker.ContainerDetail
	execCalls                 []docker.ExecRequest
	execResult                *docker.ExecCommandResult
	execErr                   error
}

func (f *fakeExecAgentClient) InspectContainer(context.Context, string) (*docker.ContainerDetail, error) {
	return f.inspectDetail, nil
}

func (f *fakeExecAgentClient) ExecCommand(_ context.Context, _ string, req docker.ExecRequest) (*docker.ExecCommandResult, error) {
	f.execCalls = append(f.execCalls, req)
	if f.execErr != nil {
		return nil, f.execErr
	}
	return f.execResult, nil
}

func execTestRouter(t *testing.T, fake *fakeExecAgentClient) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/instances/inst-test")
	g.Use(func(c *gin.Context) {
		c.Set("agent_client", fake)
		c.Set("role", "operator")
		c.Next()
	})
	g.POST("/containers/:id/exec", handleInstanceContainerExecCommand)
	return r
}

func doExec(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/instances/inst-test/containers/test-ctr/exec", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func plainDetail() *docker.ContainerDetail {
	return &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{ID: "test-ctr", Name: "test-ctr", Image: "alpine"}}
}

func TestExecCommandHandler_RunsWithAuditSummary(t *testing.T) {
	fake := &fakeExecAgentClient{
		inspectDetail: plainDetail(),
		execResult:    &docker.ExecCommandResult{ExitCode: 0, Stdout: "hello\n", DurationMS: 12},
	}
	r := execTestRouter(t, fake)
	w := doExec(t, r, docker.ExecRequest{Cmd: []string{"echo", "hello"}})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got docker.ExecCommandResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ExitCode != 0 || got.Stdout != "hello\n" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(fake.execCalls) != 1 || len(fake.execCalls[0].Cmd) != 2 {
		t.Fatalf("handler must forward the command verbatim, got %+v", fake.execCalls)
	}
}

func TestExecCommandHandler_RejectsEmptyAndBadInput(t *testing.T) {
	fake := &fakeExecAgentClient{inspectDetail: plainDetail()}
	r := execTestRouter(t, fake)

	cases := []struct {
		name string
		body any
		want int
	}{
		{"empty cmd", docker.ExecRequest{Cmd: []string{}}, http.StatusBadRequest},
		{"too many args", docker.ExecRequest{Cmd: make([]string, 65)}, http.StatusBadRequest},
		{"bad timeout", docker.ExecRequest{Cmd: []string{"ls"}, Timeout: 999}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doExec(t, r, tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d, body = %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if len(fake.execCalls) != 0 {
		t.Fatalf("rejected requests must not reach the client, got %d calls", len(fake.execCalls))
	}
}

func TestExecCommandHandler_ProtectsAgentContainer(t *testing.T) {
	fake := &fakeExecAgentClient{
		inspectDetail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
			ID: "dockpal-agent", Name: "dockpal-agent", Image: "ghcr.io/sdldev/dockpal-agent:latest",
		}},
	}
	r := execTestRouter(t, fake)
	w := doExec(t, r, docker.ExecRequest{Cmd: []string{"sh", "-c", "cat /run/secrets/token"}})

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", w.Code, w.Body.String())
	}
	if len(fake.execCalls) != 0 {
		t.Fatalf("protected container must never reach ExecCommand")
	}
}

func TestExecCommandHandler_PropagatesClientError(t *testing.T) {
	fake := &fakeExecAgentClient{
		inspectDetail: plainDetail(),
		execErr:       errors.New("exec unavailable: agent outdated"),
	}
	r := execTestRouter(t, fake)
	w := doExec(t, r, docker.ExecRequest{Cmd: []string{"ls"}})

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}
