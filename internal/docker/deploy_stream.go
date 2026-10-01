package docker

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// DeployEvent represents a single log event during deployment.
type DeployEvent struct {
	Step    string `json:"step"`
	Message string `json:"message"`
	Status  string `json:"status"` // "running", "done", "error"
	Time    string `json:"time"`
}

// DeploySession tracks an active deployment and its event stream.
//
// Events are delivered to every registered subscriber (fan-out), not consumed
// competitively, so multiple WebSocket readers each see the full stream
// (audit-stack-container M2). Events emitted before a reader subscribes are
// replayed from the session history, so a client connecting after a fast
// deploy still sees every event (this was the "modal does nothing" bug: a
// sub-second deploy finished before the WS connected).
type DeploySession struct {
	ID     string
	Events chan DeployEvent // legacy: only pre-Subscribe replay writes here
	Done   chan struct{}

	closeOnce sync.Once

	subsMu  sync.Mutex
	subs    map[chan DeployEvent]struct{}
	history []DeployEvent
	dropped uint64
	closed  bool
}

// Subscribe registers a new event receiver. All events emitted so far are
// replayed into it first (the buffer is sized to hold the full history, so
// replay never drops and never blocks while the lock is held), then live
// events follow; the channel is closed when the session closes (or
// immediately after replay if it already closed). Callers must unsubscribe
// with Unsubscribe.
//
// IMPORTANT: never drain a closed subscription channel with a
// `select { case ev := <-ch: …; default: return }` loop — a closed channel
// yields zero values forever and `default` is never reached (this caused a
// hot loop). `for range ch` terminates correctly and is the only supported
// consumption pattern.
func (s *DeploySession) Subscribe() chan DeployEvent {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	ch := make(chan DeployEvent, len(s.history)+64)
	for _, ev := range s.history {
		ch <- ev
	}
	if s.closed {
		close(ch)
	} else {
		s.subs[ch] = struct{}{}
	}
	return ch
}

// Unsubscribe removes a subscriber channel.
func (s *DeploySession) Unsubscribe(ch chan DeployEvent) {
	s.subsMu.Lock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
	s.subsMu.Unlock()
}

// DroppedEvents reports how many events were dropped because a subscriber's
// buffer was full (audit-stack-container M2: drops are no longer silent).
func (s *DeploySession) DroppedEvents() uint64 {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	return s.dropped
}

// Close signals the end of the deploy stream. Safe to call more than once —
// every streaming producer must call it (directly or via defer) so WebSocket
// readers reliably see the stream end (audit-stack-container C1).
func (s *DeploySession) Close() {
	s.closeOnce.Do(func() {
		s.subsMu.Lock()
		s.closed = true
		for ch := range s.subs {
			close(ch)
			delete(s.subs, ch)
		}
		dropped := s.dropped
		s.subsMu.Unlock()
		if dropped > 0 {
			log.Printf("deploy session %s: %d events dropped (subscriber buffer full)", s.ID, dropped)
		}
		close(s.Done)
	})
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
		subs:   make(map[chan DeployEvent]struct{}),
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

// Emit sends an event to the session's legacy channel and every subscriber.
// Delivery is non-blocking per receiver; a full buffer drops the event for
// that receiver only, and the drop is counted (see DroppedEvents).
func (s *DeploySession) Emit(step, message, status string) {
	s.EmitEvent(DeployEvent{
		Step:    step,
		Message: message,
		Status:  status,
		Time:    time.Now().Format("15:04:05"),
	})
}

// EmitEvent delivers a pre-built event to every receiver. Use this when the
// event already exists (e.g. bridged from a remote agent's stream) instead of
// writing to the Events channel directly, which would bypass the fan-out.
// Events are appended to the session history so late subscribers replay them.
func (s *DeploySession) EmitEvent(ev DeployEvent) {
	s.subsMu.Lock()
	s.history = append(s.history, ev)
	for ch := range s.subs {
		select {
		case ch <- ev:
		default:
			s.dropped++
		}
	}
	s.subsMu.Unlock()
}

// DeployComposeStreamed deploys services with progress streaming.
// If getAuthHeader is non-nil, it will be called per image to get registry credentials.
// If forcePull is true, images are always pulled even if they already exist locally.
func (c *Client) DeployComposeStreamed(ctx context.Context, projectName, composeYAML string, session *DeploySession, getAuthHeader AuthHeaderFunc, forcePull bool) error {
	defer session.Close()

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

	// Track IDs of containers this deploy actually created so cleanup only
	// ever removes our own leftovers (audit-stack-container H2).
	var createdContainers []string
	cleanup := func() {
		if len(createdContainers) == 0 {
			return
		}
		session.Emit("cleanup", "Cleaning up partial deployment...", "running")
		for _, id := range createdContainers {
			if err := c.removeContainerByID(context.Background(), id); err == nil {
				session.Emit("cleanup", fmt.Sprintf("Removed leftover container %s", id), "done")
			}
		}
		session.Emit("cleanup", "Partial deployment cleaned up", "done")
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

		if err := c.pullImageIfNeeded(ctx, svc.Image, registryAuth, forcePull); err != nil {
			suggestion := DiagnoseDeployError(err.Error())
			session.Emit("pull", fmt.Sprintf("Failed to pull %s: %s", svc.Image, err), "error")
			if suggestion != "" {
				session.Emit("hint", suggestion, "error")
			}
			// Add auth failure hint
			if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "denied") {
				domain := extractImageDomain(svc.Image)
				if registryAuth != "" {
					session.Emit("hint", fmt.Sprintf("💡 Authentication failed for %s — credentials may be expired. Update them in Settings > Registry.", domain), "error")
				} else {
					session.Emit("hint", fmt.Sprintf("💡 No credentials found for %s. Add a registry credential in Settings > Registry (for ghcr.io, add with domain 'github.com').", domain), "error")
				}
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
		createdID, err := c.createAndStartService(ctx, projectName, svcName, svc, cf)
		if err != nil {
			suggestion := DiagnoseDeployError(err.Error())
			session.Emit("create", fmt.Sprintf("Failed: %s", err), "error")
			if suggestion != "" {
				session.Emit("hint", suggestion, "error")
			}
			cleanup()
			return err
		}
		// Track only containers this deploy actually created (by ID): a name
		// conflict with someone else's running container must never put that
		// container on the cleanup list (audit-stack-container H2).
		if createdID != "" {
			createdContainers = append(createdContainers, createdID)
		}
		session.Emit("create", fmt.Sprintf("Container %s started ✓", containerName), "done")
	}

	session.Emit("complete", "Deployment complete!", "done")
	return nil
}

// DiagnoseDeployError analyzes a Docker/compose error message and returns a
// user-friendly recommendation for how to fix it. Exported so the composecli
// (stack) deploy path can offer the same hints as the legacy moby path —
// the single owner of deploy-error diagnostics (audit-stack-container L3).
func DiagnoseDeployError(errMsg string) string {
	switch {
	case strings.Contains(errMsg, "address already in use") || strings.Contains(errMsg, "port is already allocated"):
		return "💡 Port conflict: another service is using this port. Stop the existing service or change the port mapping in the compose config."
	case strings.Contains(errMsg, "No such image"):
		return "💡 Image not found: check the image name and tag. Make sure it exists on Docker Hub or your registry."
	case strings.Contains(errMsg, "is already in use"):
		return "💡 Container name conflict: a container with this name already exists. Stop and remove it first from the Containers page, or use a different app name."
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
