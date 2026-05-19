package docker

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// DeployEvent represents a single log event during deployment.
type DeployEvent struct {
	Step    string `json:"step"`
	Message string `json:"message"`
	Status  string `json:"status"` // "running", "done", "error"
	Time    string `json:"time"`
}

// DeploySession tracks an active deployment and its event stream.
type DeploySession struct {
	ID     string
	Events chan DeployEvent
	Done   chan struct{}
}

// DeployManager manages active deploy sessions.
type DeployManager struct {
	mu       sync.Mutex
	sessions map[string]*DeploySession
}

// NewDeployManager creates a new DeployManager.
func NewDeployManager() *DeployManager {
	return &DeployManager{
		sessions: make(map[string]*DeploySession),
	}
}

// CreateSession creates a new deploy session and returns its ID.
func (dm *DeployManager) CreateSession() *DeploySession {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var id string
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback — should never happen
		id = fmt.Sprintf("deploy-%d", time.Now().UnixNano())
	} else {
		id = fmt.Sprintf("deploy-%x", b)
	}
	session := &DeploySession{
		ID:     id,
		Events: make(chan DeployEvent, 50),
		Done:   make(chan struct{}),
	}
	dm.sessions[id] = session
	return session
}

// GetSession retrieves a deploy session by ID.
func (dm *DeployManager) GetSession(id string) *DeploySession {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.sessions[id]
}

// RemoveSession removes a completed session.
func (dm *DeployManager) RemoveSession(id string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	delete(dm.sessions, id)
}

// Emit sends an event to the session's event channel.
func (s *DeploySession) Emit(step, message, status string) {
	select {
	case s.Events <- DeployEvent{
		Step:    step,
		Message: message,
		Status:  status,
		Time:    time.Now().Format("15:04:05"),
	}:
	default:
		// Channel full, skip
	}
}

// DeployComposeStreamed deploys services with progress streaming.
// If getAuthHeader is non-nil, it will be called per image to get registry credentials.
func (c *Client) DeployComposeStreamed(ctx context.Context, projectName, composeYAML string, session *DeploySession, getAuthHeader AuthHeaderFunc) error {
	defer close(session.Done)

	session.Emit("parse", "Parsing compose file...", "running")

	cf, err := ParseComposeFile(composeYAML)
	if err != nil {
		session.Emit("parse", fmt.Sprintf("Parse error: %s", err), "error")
		return fmt.Errorf("failed to parse compose: %w", err)
	}
	session.Emit("parse", fmt.Sprintf("Found %d service(s)", len(cf.Services)), "done")

	// Resolve start order
	session.Emit("resolve", "Resolving dependency order...", "running")
	startOrder, err := ResolveStartOrder(cf)
	if err != nil {
		session.Emit("resolve", fmt.Sprintf("Dependency error: %s", err), "error")
		return fmt.Errorf("failed to resolve service start order: %w", err)
	}
	session.Emit("resolve", fmt.Sprintf("Start order: %v", startOrder), "done")

	// Track containers created so we can clean up on failure
	var createdContainers []string
	cleanup := func() {
		if len(createdContainers) == 0 {
			return
		}
		session.Emit("cleanup", "Cleaning up partial deployment...", "running")
		for _, name := range createdContainers {
			c.cli.ContainerRemove(context.Background(), name, client.ContainerRemoveOptions{Force: true})
		}
		session.Emit("cleanup", "Removed partial containers", "done")
	}

	// Pull images
	for _, svcName := range startOrder {
		svc := cf.Services[svcName]
		session.Emit("pull", fmt.Sprintf("Pulling %s...", svc.Image), "running")

		registryAuth := ""
		if getAuthHeader != nil {
			auth, err := getAuthHeader(svc.Image)
			if err == nil {
				registryAuth = auth
			}
		}

		if err := c.pullImageIfNeeded(ctx, svc.Image, registryAuth); err != nil {
			suggestion := diagnoseDeployError(err.Error())
			session.Emit("pull", fmt.Sprintf("Failed to pull %s: %s", svc.Image, err), "error")
			if suggestion != "" {
				session.Emit("hint", suggestion, "error")
			}
			// Add auth failure hint if credentials were used
			if registryAuth != "" && (strings.Contains(err.Error(), "authentication failed") ||
				strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "denied")) {
				domain := extractImageDomain(svc.Image)
				session.Emit("hint", fmt.Sprintf("💡 Authentication failed for %s — credentials may be expired. Update them in Settings > Registry.", domain), "error")
			}
			return fmt.Errorf("failed to pull image for %s: %w", svcName, err)
		}
		session.Emit("pull", fmt.Sprintf("Image %s ready", svc.Image), "done")
	}

	// Write compose file
	session.Emit("write", "Saving compose file...", "running")
	if err := writeComposeFile(projectName, composeYAML); err != nil {
		session.Emit("write", fmt.Sprintf("Write error: %s", err), "error")
		return err
	}
	session.Emit("write", "Compose file saved", "done")

	// Create and start containers
	for _, svcName := range startOrder {
		svc := cf.Services[svcName]
		containerName := fmt.Sprintf("%s_%s", projectName, svcName)

		session.Emit("create", fmt.Sprintf("Creating container %s...", containerName), "running")
		if err := c.createAndStartService(ctx, projectName, svcName, svc, cf); err != nil {
			suggestion := diagnoseDeployError(err.Error())
			session.Emit("create", fmt.Sprintf("Failed: %s", err), "error")
			if suggestion != "" {
				session.Emit("hint", suggestion, "error")
			}
			cleanup()
			return err
		}
		createdContainers = append(createdContainers, containerName)
		session.Emit("create", fmt.Sprintf("Container %s started ✓", containerName), "done")
	}

	session.Emit("complete", "Deployment complete!", "done")
	return nil
}

// diagnoseDeployError analyzes a Docker error message and returns a user-friendly
// recommendation for how to fix it.
func diagnoseDeployError(errMsg string) string {
	switch {
	case strings.Contains(errMsg, "address already in use") || strings.Contains(errMsg, "port is already allocated"):
		return "💡 Port conflict: another service is using this port. Stop the existing service or change the port mapping in the compose config."
	case strings.Contains(errMsg, "No such image"):
		return "💡 Image not found: check the image name and tag. Make sure it exists on Docker Hub or your registry."
	case strings.Contains(errMsg, "name is already in use"):
		return "💡 Container name conflict: a container with this name already exists. Remove it first from the Containers page."
	case strings.Contains(errMsg, "permission denied"):
		return "💡 Permission denied: Dockpal may need elevated privileges, or the volume path doesn't exist."
	case strings.Contains(errMsg, "network not found"):
		return "💡 Network not found: create the Docker network first, or remove the networks section from compose."
	case strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "context deadline"):
		return "💡 Timeout: Docker daemon is slow or unresponsive. Check Docker service status."
	case strings.Contains(errMsg, "disk space") || strings.Contains(errMsg, "no space left"):
		return "💡 Disk full: free up disk space with 'docker system prune' or remove unused images."
	case strings.Contains(errMsg, "manifest unknown") || strings.Contains(errMsg, "not found"):
		return "💡 Image tag not found: the specified version may not exist. Try using ':latest' instead."
	default:
		return ""
	}
}
