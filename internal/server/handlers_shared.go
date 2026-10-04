package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/validator"
)

// serveContainerLogsWS is the WebSocket body shared by the local and
// instance-scoped container log routes: upgrade on the shared Origin-checked
// upgrader, authenticate via query token or first message, then stream logs.
func serveContainerLogsWS(c *gin.Context, client agent.AgentClient, containerID string) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Auth: query token (browser WS) or first {token} message (API clients);
	// the query value may be a single-use ws-ticket or a raw JWT.
	if q := c.Query("token"); q != "" {
		role := resolveWSQueryRole(c, q)
		if role == "" || !auth.HasRole(role, auth.RoleViewer) {
			conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(4001, "authentication failed"))
			return
		}
	} else if !authenticateWebSocketFirstMessage(conn, c) {
		return
	}

	reader, err := client.ContainerLogs(c.Request.Context(), containerID, c.DefaultQuery("tail", "100"))
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("Error: failed to retrieve container logs"))
		return
	}

	streamContainerLogs(conn, reader)
}

// containerEditResult is what applyContainerEdit reports back to its caller.
type containerEditResult struct {
	Request       docker.ContainerEditRequest
	Detail        *docker.ContainerDetail
	NeedsRecreate bool
}

// applyContainerEdit is the request body shared by the local and
// instance-scoped container edit routes: bind, validate, protect the agent
// container when a recreate is needed and call EditContainer. It writes the
// error response itself and returns ok=false when the caller must stop; the
// caller owns audit logging and the success response shape.
func applyContainerEdit(c *gin.Context, client agent.AgentClient, containerID string) (*containerEditResult, bool) {
	var req docker.ContainerEditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return nil, false
	}

	if req.Name != nil {
		if err := validator.ValidateContainerName(*req.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid name: %s", err.Error())})
			return nil, false
		}
	}

	if req.RestartPolicy != nil {
		if err := validator.ValidateRestartPolicy(*req.RestartPolicy); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return nil, false
		}
	}

	if req.MemoryLimit != nil && *req.MemoryLimit < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "memory limit must be non-negative"})
		return nil, false
	}

	if req.CPULimit != nil && *req.CPULimit < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CPU limit must be non-negative"})
		return nil, false
	}

	if req.Env != nil {
		for _, env := range *req.Env {
			if err := validator.ValidateEnvVarValue(env); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid env var: %s", err.Error())})
				return nil, false
			}
		}
	}

	if req.Ports != nil {
		for _, pm := range *req.Ports {
			if err := validator.ValidatePortMapping(pm.HostPort, pm.ContainerPort, pm.Protocol); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return nil, false
			}
		}
	}

	if req.Volumes != nil {
		for _, vm := range *req.Volumes {
			if vm.ContainerPath == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "volume container path cannot be empty"})
				return nil, false
			}
			if vm.HostPath == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "volume host path cannot be empty"})
				return nil, false
			}
		}
	}

	needsRecreate := req.Image != nil || req.Env != nil || req.Ports != nil || req.Volumes != nil
	if needsRecreate {
		if err := ensureContainerRemovable(c.Request.Context(), client, containerID); err != nil {
			if errors.Is(err, errProtectedDockpalAgentContainer) {
				c.JSON(http.StatusForbidden, gin.H{"error": "Dockpal agent container cannot be recreated from Dockpal", "protected": true})
				return nil, false
			}
			internalError(c, err)
			return nil, false
		}
	}

	detail, err := client.EditContainer(c.Request.Context(), containerID, req)
	if err != nil {
		internalError(c, err)
		return nil, false
	}
	return &containerEditResult{Request: req, Detail: detail, NeedsRecreate: needsRecreate}, true
}

// triggeredByFor resolves the app-update audit actor from the request.
func triggeredByFor(c *gin.Context) string {
	username := "user"
	if v, ok := c.Get("username"); ok {
		if s, ok := v.(string); ok && s != "" {
			username = s
		}
	}
	return "user:" + username
}

// newAppUpdateAttempt returns the latest attempt id for the app when it
// differs from prevAttempt, or "" when no newer record exists. Called with
// an empty prevAttempt it doubles as the pre-trigger snapshot.
func newAppUpdateAttempt(database *db.DB, name, prevAttempt string) string {
	if recs, err := database.ListAppUpdates(name, 1); err == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
		return recs[0].AttemptID
	}
	return ""
}

// startLocalAppUpdate triggers the local auto-update worker asynchronously and
// polls the database for the new attempt record — the flow shared by the
// panel-wide and instance-scoped trigger routes. A concurrent run maps to
// HTTP 409 via ErrUpdateAlreadyRunning; it always writes a response before
// returning.
func startLocalAppUpdate(c *gin.Context, database *db.DB, name, triggeredBy, prevAttempt string) {
	errCh := make(chan error, 1)
	go func() {
		// context.Background(): the pipeline must outlive the HTTP request.
		errCh <- globalAutoUpdateWorker.TriggerApp(context.Background(), name, true, true, triggeredBy)
	}()

	timeout := time.NewTimer(5 * time.Second)
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
				internalError(c, err)
				return
			}
			// TriggerApp finished before the poll saw a record; return the newest attempt.
			if id := newAppUpdateAttempt(database, name, prevAttempt); id != "" {
				c.JSON(http.StatusAccepted, gin.H{"attempt_id": id})
				return
			}
			c.JSON(http.StatusAccepted, gin.H{"status": "ok"})
			return
		case <-ticker.C:
			if id := newAppUpdateAttempt(database, name, prevAttempt); id != "" {
				c.JSON(http.StatusAccepted, gin.H{"attempt_id": id})
				return
			}
		case <-timeout.C:
			if id := newAppUpdateAttempt(database, name, prevAttempt); id != "" {
				c.JSON(http.StatusAccepted, gin.H{"attempt_id": id})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "trigger did not produce a record in time"})
			return
		}
	}
}
