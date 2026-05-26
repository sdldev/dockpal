package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
)

type protectionFakeAgentClient struct {
	detail       *docker.ContainerDetail
	inspectErr   error
	removeCalled bool
	updateCalled bool
	force        bool
}

func (f *protectionFakeAgentClient) ListContainers(context.Context, bool) ([]docker.ContainerInfo, error) {
	return nil, nil
}

func (f *protectionFakeAgentClient) InspectContainer(context.Context, string) (*docker.ContainerDetail, error) {
	if f.inspectErr != nil {
		return nil, f.inspectErr
	}
	return f.detail, nil
}

func (f *protectionFakeAgentClient) StartContainer(context.Context, string) error { return nil }
func (f *protectionFakeAgentClient) StopContainer(context.Context, string) error  { return nil }
func (f *protectionFakeAgentClient) RestartContainer(context.Context, string) error {
	return nil
}
func (f *protectionFakeAgentClient) RemoveContainer(_ context.Context, _ string, force bool) error {
	f.removeCalled = true
	f.force = force
	return nil
}
func (f *protectionFakeAgentClient) EditContainer(context.Context, string, docker.ContainerEditRequest) (*docker.ContainerDetail, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) UpdateContainerImage(context.Context, string, string) (*docker.ContainerDetail, error) {
	f.updateCalled = true
	return f.detail, nil
}
func (f *protectionFakeAgentClient) GetContainerStats(context.Context, string) (*docker.ContainerStats, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) ContainerLogs(context.Context, string, string) (io.ReadCloser, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) DeployCompose(context.Context, string, string, map[string]string, bool) error {
	return nil
}
func (f *protectionFakeAgentClient) DeployComposeStreamed(context.Context, string, string, *docker.DeploySession, map[string]string, bool) error {
	return nil
}
func (f *protectionFakeAgentClient) ListImages(context.Context) ([]docker.ImageInfo, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) PullImage(context.Context, string) error { return nil }
func (f *protectionFakeAgentClient) PullImageWithAuth(context.Context, string, string) error {
	return nil
}
func (f *protectionFakeAgentClient) RemoveImage(context.Context, string) error { return nil }
func (f *protectionFakeAgentClient) CheckImageUpdate(context.Context, string) (*docker.ImageUpdateResult, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) ForcePullImage(context.Context, string, string) error { return nil }
func (f *protectionFakeAgentClient) GetHostInfo(context.Context) (*agent.HostInfo, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) GetHostStats(context.Context) (*agent.HostStats, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) Ping(context.Context) error { return nil }
func (f *protectionFakeAgentClient) Close() error               { return nil }

// App auto-update stubs (added in task 5.4 to satisfy the AgentClient
// interface). The container-protection tests do not exercise these paths.
func (f *protectionFakeAgentClient) ListApps(context.Context) ([]docker.AppSummary, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) ListAppUpdates(context.Context, string, int) ([]db.AppUpdateRecord, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) GetAppUpdate(context.Context, string) (*db.AppUpdateRecord, error) {
	return nil, nil
}
func (f *protectionFakeAgentClient) TriggerAppUpdate(context.Context, string) (string, error) {
	return "", nil
}
func (f *protectionFakeAgentClient) SetAppAutoUpdate(context.Context, string, bool) error {
	return nil
}

func TestIsProtectedDockpalAgentContainer(t *testing.T) {
	tests := []struct {
		name   string
		detail *docker.ContainerDetail
		want   bool
	}{
		{
			name: "dockpal agent image",
			detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
				Name:  "dockpal-agent",
				Image: "ghcr.io/sdldev/dockpal-agent:latest",
			}},
			want: true,
		},
		{
			name: "leading slash docker name",
			detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
				Name:  "/dockpal-agent",
				Image: "docker.io/sdldev/dockpal-agent:latest",
			}},
			want: true,
		},
		{
			name: "agent env marker",
			detail: &docker.ContainerDetail{
				ContainerInfo: docker.ContainerInfo{Name: "dockpal-agent", Image: "busybox:latest"},
				Env:           []string{"DOCKPAL_MODE=edge", "DOCKPAL_TOKEN=secret"},
			},
			want: true,
		},
		{
			name: "same name without agent identity",
			detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
				Name:  "dockpal-agent",
				Image: "nginx:latest",
			}},
			want: false,
		},
		{
			name: "user container",
			detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
				Name:  "my-app",
				Image: "ghcr.io/example/app:latest",
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProtectedDockpalAgentContainer(tt.detail); got != tt.want {
				t.Fatalf("isProtectedDockpalAgentContainer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdateImageProtectedDockpalAgentContainerForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &protectionFakeAgentClient{detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
		Name:  "dockpal-agent",
		Image: "ghcr.io/sdldev/dockpal-agent:latest",
	}}}

	r := gin.New()
	r.POST("/api/instances/:instance_id/containers/:id/update-image", func(c *gin.Context) {
		c.Set("agent_client", fake)
		c.Set("instance_id", c.Param("instance_id"))
	}, handleInstanceUpdateContainerImage)

	req := httptest.NewRequest(http.MethodPost, "/api/instances/local/containers/dockpal-agent/update-image", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusForbidden, w.Body.String())
	}
	if fake.updateCalled {
		t.Fatal("UpdateContainerImage was called for protected container")
	}
}

func TestUpdateImageUserContainerCallsAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &protectionFakeAgentClient{detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
		Name:  "web",
		Image: "nginx:latest",
	}}}

	r := gin.New()
	r.POST("/api/instances/:instance_id/containers/:id/update-image", func(c *gin.Context) {
		c.Set("agent_client", fake)
		c.Set("instance_id", c.Param("instance_id"))
	}, handleInstanceUpdateContainerImage)

	req := httptest.NewRequest(http.MethodPost, "/api/instances/local/containers/web/update-image", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if !fake.updateCalled {
		t.Fatal("UpdateContainerImage was not called")
	}
}

func TestEnsureContainerRemovable(t *testing.T) {
	protectedClient := &protectionFakeAgentClient{detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
		Name:  "dockpal-agent",
		Image: "ghcr.io/sdldev/dockpal-agent:latest",
	}}}
	if err := ensureContainerRemovable(context.Background(), protectedClient, "dockpal-agent"); !errors.Is(err, errProtectedDockpalAgentContainer) {
		t.Fatalf("expected protected container error, got %v", err)
	}
	if protectedClient.removeCalled {
		t.Fatal("RemoveContainer was called for protected container")
	}

	normalClient := &protectionFakeAgentClient{detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
		Name:  "web",
		Image: "nginx:latest",
	}}}
	if err := ensureContainerRemovable(context.Background(), normalClient, "web"); err != nil {
		t.Fatalf("expected normal container to be removable, got %v", err)
	}

	inspectErr := errors.New("inspect failed")
	errorClient := &protectionFakeAgentClient{inspectErr: inspectErr}
	if err := ensureContainerRemovable(context.Background(), errorClient, "web"); !errors.Is(err, inspectErr) {
		t.Fatalf("expected inspect error, got %v", err)
	}
	if errorClient.removeCalled {
		t.Fatal("RemoveContainer was called after inspect error")
	}
}

func TestHandleInstanceRemoveContainerRejectsProtectedContainer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fakeClient := &protectionFakeAgentClient{detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
		Name:  "dockpal-agent",
		Image: "ghcr.io/sdldev/dockpal-agent:latest",
	}}}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("role", auth.RoleOperator)
		c.Set("instance_id", "remote-1")
		c.Set("agent_client", agent.AgentClient(fakeClient))
		c.Next()
	})
	r.DELETE("/containers/:id", RequireRole(auth.RoleOperator), handleInstanceRemoveContainer)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/containers/dockpal-agent?force=true", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d: %s", http.StatusForbidden, w.Code, w.Body.String())
	}
	if fakeClient.removeCalled {
		t.Fatal("RemoveContainer was called for protected container")
	}
}

func TestHandleInstanceRemoveContainerAllowsUserContainer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fakeClient := &protectionFakeAgentClient{detail: &docker.ContainerDetail{ContainerInfo: docker.ContainerInfo{
		Name:  "web",
		Image: "nginx:latest",
	}}}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("role", auth.RoleOperator)
		c.Set("instance_id", "remote-1")
		c.Set("agent_client", agent.AgentClient(fakeClient))
		c.Next()
	})
	r.DELETE("/containers/:id", RequireRole(auth.RoleOperator), handleInstanceRemoveContainer)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/containers/web?force=true", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}
	if !fakeClient.removeCalled {
		t.Fatal("RemoveContainer was not called for normal container")
	}
	if !fakeClient.force {
		t.Fatal("force flag was not passed through")
	}
}
