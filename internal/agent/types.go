package agent

import (
	"context"
	"encoding/json"
	"io"

	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
)

// HostInfo contains system information about the host machine.
type HostInfo struct {
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	CPUCores      int    `json:"cpu_cores"`
	TotalMemory   uint64 `json:"total_memory"`
	DockerVersion string `json:"docker_version"`
}

// HostStats contains real-time resource usage statistics.
type HostStats struct {
	CPUPercent float64 `json:"cpu_percent"`
	UsedRAM    uint64  `json:"used_ram"`
	TotalRAM   uint64  `json:"total_ram"`
	UsedDisk   uint64  `json:"used_disk"`
	TotalDisk  uint64  `json:"total_disk"`
}

// AgentClient defines the interface for interacting with a Docker host agent.
// This interface allows for both local Docker connections and remote agent connections.
type AgentClient interface {
	// Container operations
	ListContainers(ctx context.Context, all bool) ([]docker.ContainerInfo, error)
	InspectContainer(ctx context.Context, id string) (*docker.ContainerDetail, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string) error
	RestartContainer(ctx context.Context, id string) error
	RemoveContainer(ctx context.Context, id string, force bool) error
	EditContainer(ctx context.Context, id string, req docker.ContainerEditRequest) (*docker.ContainerDetail, error)
	UpdateContainerImage(ctx context.Context, id string, registryAuth string) (*docker.ContainerDetail, error)
	GetContainerStats(ctx context.Context, id string) (*docker.ContainerStats, error)
	ContainerLogs(ctx context.Context, id string, tail string) (io.ReadCloser, error)

	// Compose operations
	DeployCompose(ctx context.Context, name, composeYAML string, registryAuths map[string]string, forcePull bool) error
	DeployComposeStreamed(ctx context.Context, name, composeYAML string, session *docker.DeploySession, registryAuths map[string]string, forcePull bool) error

	// Image operations
	ListImages(ctx context.Context) ([]docker.ImageInfo, error)
	PullImage(ctx context.Context, image string) error
	PullImageWithAuth(ctx context.Context, image, registryAuth string) error
	RemoveImage(ctx context.Context, id string) error
	CheckImageUpdate(ctx context.Context, image string) (*docker.ImageUpdateResult, error)
	ForcePullImage(ctx context.Context, image, registryAuth string) error
	PruneImages(ctx context.Context, danglingOnly bool) (*docker.PruneResult, error)

	// Host operations
	GetHostInfo(ctx context.Context) (*HostInfo, error)
	GetHostStats(ctx context.Context) (*HostStats, error)

	// App auto-update operations (auto-image-update spec).
	// ListApps returns one summary per compose project on the instance,
	// folding in update-availability and the most recent update record.
	ListApps(ctx context.Context) ([]docker.AppSummary, error)
	// ListAppUpdates returns up to limit App_Update_Records for a given
	// project ordered newest-first by StartedAt. A non-positive limit
	// means "no limit"; concrete implementations may cap it.
	ListAppUpdates(ctx context.Context, app string, limit int) ([]db.AppUpdateRecord, error)
	// GetAppUpdate fetches a single App_Update_Record by attempt id and
	// returns (nil, nil) when the record does not exist.
	GetAppUpdate(ctx context.Context, attemptID string) (*db.AppUpdateRecord, error)
	// TriggerAppUpdate runs the manual auto-update pipeline for one app
	// and returns the new attempt id once the worker has persisted the
	// first stage event.
	TriggerAppUpdate(ctx context.Context, app string) (string, error)
	// SetAppAutoUpdate enables or disables the dockpal.auto-update label on
	// the running containers of the project by rewriting its compose YAML
	// and redeploying with forcePull=false.
	SetAppAutoUpdate(ctx context.Context, app string, enabled bool) error

	// Connection
	Ping(ctx context.Context) error
	Close() error
}

// AgentRequest represents a request from edge to agent for HTTP-like operations.
type AgentRequest struct {
	RequestID string            `json:"request_id"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Query     map[string]string `json:"query,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`
}

// AgentResponse represents a response from agent to edge for HTTP-like operations.
type AgentResponse struct {
	RequestID string          `json:"request_id"`
	Status    int             `json:"status"`
	Body      json.RawMessage `json:"body,omitempty"`
	Stream    bool            `json:"stream"`
	Chunk     int             `json:"chunk,omitempty"`
	Data      string          `json:"data,omitempty"`
}
