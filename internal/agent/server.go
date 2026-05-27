// Package agent — server.go exposes the agent-side HTTP handlers for the
// auto-image-update spec (task 6.4). Each handler delegates to the same
// AutoUpdateWorker, ImageUpdateMonitor, AppUpdateStore, and *docker.Client
// that the agent boots once at startup, so the manual /apps/* operations
// running on a remote agent share the cooldown, single-flight, and
// rollback semantics of the local pipeline.
//
// The endpoints registered here are the agent-side counterpart of the
// EdgeClient and DirectClient methods added in task 6.3:
//
//   - direct mode:  /agent/docker/apps/...   (HTTPS, bearer auth)
//   - edge mode:    /docker/apps/...         (WebSocket-multiplexed JSON)
//
// Both transports forward the same request bodies; the handlers in this
// file accept the gin context and respond with the JSON shape documented
// on the matching client method. The agent process owns the registration
// path (it decides whether to mount the routes under "/agent" for direct
// mode or to dispatch AgentRequest messages by path for edge mode); this
// file provides only the handler set so the registration site can choose
// the prefix.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
)

// AppHandlerDeps bundles the dependencies an agent process must wire on
// boot to expose the /apps/* surface. Every field is required except
// InstanceID which is optional and (when set) is stamped on every
// AppSummary returned by the list endpoint so the edge UI can route
// per-instance without a second lookup.
//
// The Worker field carries the per-app mutex and the rollback pipeline;
// the SetAutoUpdate field is a closure the agent provides because the
// PATCH redeploy path needs access to the agent's local services bucket
// and its registry manager — keeping it as a closure avoids leaking those
// concrete types into this package and lets the agent process layer the
// behavior however it likes (e.g. local docker DeployCompose vs another
// transport).
type AppHandlerDeps struct {
	// DockerClient is used by HandleListApps to enumerate compose
	// projects. The same instance is used by Worker for the pull/recreate
	// pipeline so the agent only opens one Docker connection.
	DockerClient *docker.Client

	// Monitor backs HandleListApps' update-availability fields. May be
	// nil; in that case AppServiceSummary.HasUpdate, LocalDigest, and
	// RemoteDigest are left empty.
	Monitor *docker.ImageUpdateMonitor

	// Store powers HandleListAppUpdates and HandleGetAppUpdate. It is
	// also passed to docker.Client.ListApps so each AppSummary can
	// include its most recent App_Update_Record. Required.
	Store db.AppUpdateStore

	// Worker is the agent-side AutoUpdateWorker. HandleTriggerAppUpdate
	// calls Worker.TriggerApp with bypassCooldown=true and
	// bypassWindow=true to mirror the manual semantics of POST
	// /apps/:name/update on the edge (R6.1).
	Worker *docker.AutoUpdateWorker

	// SetAutoUpdate is a closure that toggles the dockpal.auto-update
	// label on the project's compose YAML and redeploys with
	// forcePull=false (R1.4). The agent process supplies this so the
	// implementation can reuse the agent's local services bucket and
	// registry manager. It must return a typed error when the app does
	// not exist — HandleSetAppAutoUpdate maps the well-known sentinels
	// to HTTP status codes via errors.Is.
	SetAutoUpdate func(ctx context.Context, app string, enabled bool) error

	// InstanceID is stamped on every AppSummary returned by
	// HandleListApps when non-empty. The agent's registration code reads
	// this from the agent token / config so the edge UI can route
	// subsequent per-instance requests without an extra lookup.
	InstanceID string
}

// DockerHandlerDeps bundles the agent-side Docker API dependencies used by
// direct and edge transports for container/image operations that need to run
// inside the agent process.
func internalError(c *gin.Context, err error) {
	log.Printf("[ERROR] %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

type DockerHandlerDeps struct {
	DockerClient *docker.Client
}

// DockerHandler exposes transport-agnostic agent-side Docker handlers.
type DockerHandler struct {
	deps DockerHandlerDeps
}

// NewDockerHandler validates and returns an agent-side Docker handler.
func NewDockerHandler(deps DockerHandlerDeps) (*DockerHandler, error) {
	if deps.DockerClient == nil {
		return nil, fmt.Errorf("agent docker handler: DockerClient is required")
	}
	return &DockerHandler{deps: deps}, nil
}

// RegisterDockerRoutes mounts Docker container operations not covered by the
// app auto-update surface. The caller chooses /agent/docker for direct mode or
// /docker for edge mode.
func RegisterDockerRoutes(rg gin.IRoutes, h *DockerHandler) {
	rg.POST("/containers/:id/update-image", h.HandleUpdateContainerImage)
}

// HandleUpdateContainerImage force-pulls the current container image and
// recreates the container using the inspected Docker config. Registry auth is
// passed as the optional `auth` query parameter by DirectClient/EdgeClient.
func (h *DockerHandler) HandleUpdateContainerImage(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing container id"})
		return
	}
	detail, err := h.deps.DockerClient.UpdateContainerImage(c.Request.Context(), id, c.Query("auth"))
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

// AppHandler is the request-handling face of AppHandlerDeps. It is
// constructed once at agent boot and registered against the gin router
// (and/or used directly by the edge WebSocket dispatcher) for the
// lifetime of the process.
type AppHandler struct {
	deps AppHandlerDeps
}

// Common error sentinels returned by AppHandler so the registration
// site can pattern-match them via errors.Is when needed. These are
// intentionally exported so the (separately-built) agent process can
// translate them to HTTP responses if it dispatches /apps/* by hand
// instead of through gin.
var (
	// ErrAppNotFound is returned by SetAutoUpdate when the project is
	// unknown to the agent. HandleSetAppAutoUpdate maps it to HTTP 404.
	ErrAppNotFound = errors.New("app not found")
)

// NewAppHandler returns a fully-wired handler for the agent-side /apps
// surface. It validates the required fields up front so a misconfigured
// agent fails loudly at startup rather than on the first request.
func NewAppHandler(deps AppHandlerDeps) (*AppHandler, error) {
	if deps.DockerClient == nil {
		return nil, fmt.Errorf("agent app handler: DockerClient is required")
	}
	if deps.Store == nil {
		return nil, fmt.Errorf("agent app handler: Store is required")
	}
	if deps.Worker == nil {
		return nil, fmt.Errorf("agent app handler: Worker is required")
	}
	if deps.SetAutoUpdate == nil {
		return nil, fmt.Errorf("agent app handler: SetAutoUpdate is required")
	}
	return &AppHandler{deps: deps}, nil
}

// RegisterAppRoutes mounts the /apps/* handlers onto rg. The caller
// chooses the prefix:
//
//   - direct mode:  agent.RegisterAppRoutes(r.Group("/agent/docker"))
//     resulting in /agent/docker/apps, /agent/docker/apps/:name/updates, ...
//   - edge mode:    agent.RegisterAppRoutes(r.Group("/docker"))
//     resulting in /docker/apps, /docker/apps/:name/updates, ...
//
// The route shapes match the client paths from EdgeClient and
// DirectClient (task 6.3), so the agent does not need any path
// translation between the two transports.
//
// Authorization on the agent side is the bearer-token / WebSocket
// handshake that already gates every other /agent path; no additional
// role middleware is applied here because the agent itself is the
// trust boundary for its host.
func RegisterAppRoutes(rg gin.IRoutes, h *AppHandler) {
	rg.GET("/apps", h.HandleListApps)
	rg.GET("/apps/:name/updates", h.HandleListAppUpdates)
	// /apps/updates/:attemptID is registered before /apps/:name/... in
	// gin's tree because gin's static prefix routing prefers the literal
	// "updates" segment over the parameter binding. Registering both is
	// safe — a check inside the gin router test in route_registration
	// confirms the order does not panic.
	rg.GET("/apps/updates/:attemptID", h.HandleGetAppUpdate)
	rg.POST("/apps/:name/update", h.HandleTriggerAppUpdate)
	rg.PATCH("/apps/:name/auto-update", h.HandleSetAppAutoUpdate)
}

// HandleListApps returns the AppSummary slice for this agent. Mirrors
// GET /apps on the edge: it queries docker for every container with
// the dockpal.project label, folds in the ImageUpdateMonitor cache for
// has_update / digest fields, and asks the AppUpdateStore for the most
// recent record per app.
//
// InstanceID is stamped here when non-empty so the edge UI does not
// need a second lookup. An empty list is returned as `[]` (not null)
// to keep the response shape consistent with GET /apps on the edge.
func (h *AppHandler) HandleListApps(c *gin.Context) {
	apps, err := h.deps.DockerClient.ListApps(c.Request.Context(), h.deps.Monitor, h.deps.Store)
	if err != nil {
		internalError(c, err)
		return
	}
	if apps == nil {
		apps = []docker.AppSummary{}
	}
	if h.deps.InstanceID != "" {
		for i := range apps {
			apps[i].InstanceID = h.deps.InstanceID
		}
	}
	c.JSON(http.StatusOK, apps)
}

// HandleListAppUpdates returns up to ?limit= records for the named app,
// newest-first by StartedAt. The default limit matches the edge handler
// (50), and the upper bound matches the global retention cap (1000) so a
// caller can never request more than the agent could have stored.
func (h *AppHandler) HandleListAppUpdates(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing app name"})
		return
	}

	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	recs, err := h.deps.Store.ListAppUpdates(name, limit)
	if err != nil {
		internalError(c, err)
		return
	}
	if recs == nil {
		recs = []db.AppUpdateRecord{}
	}
	c.JSON(http.StatusOK, recs)
}

// HandleGetAppUpdate looks up one record by attempt id. A missing
// record yields HTTP 404 (with a JSON body) so the edge can detect the
// "no such attempt" case without parsing free-form errors.
func (h *AppHandler) HandleGetAppUpdate(c *gin.Context) {
	attemptID := c.Param("attemptID")
	if attemptID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing attempt id"})
		return
	}
	rec, err := h.deps.Store.GetAppUpdate(attemptID)
	if err != nil {
		internalError(c, err)
		return
	}
	if rec == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attempt not found"})
		return
	}
	c.JSON(http.StatusOK, rec)
}

// triggerAppRequestBody is the request shape POSTed by both EdgeClient
// and DirectClient (see task 6.3). The "app" field is informational —
// the agent uses the URL :name parameter as the source of truth so a
// mismatched body cannot route the trigger to a different project.
type triggerAppRequestBody struct {
	App string `json:"app"`
}

// triggerAppResponseBody is the JSON shape returned on success. Mirrors
// the body parsed by EdgeClient.TriggerAppUpdate /
// DirectClient.TriggerAppUpdate.
type triggerAppResponseBody struct {
	AttemptID string `json:"attempt_id"`
}

// HandleTriggerAppUpdate runs the manual auto-update pipeline for one
// app, mirroring POST /apps/:name/update on the edge. Manual semantics
// are baked in: bypassCooldown=true, bypassWindow=true (R6.1).
//
// The handler kicks the worker off in a background goroutine and polls
// the store for a new attempt id, returning HTTP 202 once the worker
// has persisted the first stage event. A worker error containing
// docker.ErrUpdateAlreadyRunning is mapped to HTTP 409 with the
// well-known JSON body {"error":"update_already_running"} so the edge
// can match it without parsing free-form text (R6.2).
//
// The poll deadline (5 seconds) gives the worker a generous amount of
// time to acquire the per-app mutex and write its first record, but
// matches the edge's POST /apps/:name/update handler so a stuck pipeline
// reports the same error path on both sides.
func (h *AppHandler) HandleTriggerAppUpdate(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing app name"})
		return
	}

	// Decoding the body is best-effort: the URL is the source of truth
	// for the app name, but a missing/invalid JSON body is still a 400
	// because the edge clients always send {"app": "<name>"} and a
	// strict response makes wire-format drift fail loudly.
	var body triggerAppRequestBody
	if c.Request.ContentLength > 0 || c.GetHeader("Content-Type") != "" {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
	}

	// Snapshot the latest attempt id so we can detect the new record
	// once the worker has persisted its first stage event. A nil/empty
	// result here just means this app has no prior attempts — that is
	// normal for the very first run.
	var prevAttempt string
	if recs, err := h.deps.Store.ListAppUpdates(name, 1); err == nil && len(recs) > 0 {
		prevAttempt = recs[0].AttemptID
	}

	// The pipeline runs under context.Background() rather than the
	// request context so a fast HTTP timeout does not abort the
	// in-flight pull/recreate. The worker's own Stop() path handles
	// agent shutdown.
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.deps.Worker.TriggerApp(context.Background(), name, true, true, "user:agent")
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
			// TriggerApp finished without error before the poll picked
			// up a new record. Look up the most recent attempt so we
			// can return its id (the pipeline saves at least one
			// record on the happy path).
			if recs, lerr := h.deps.Store.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
				c.JSON(http.StatusAccepted, triggerAppResponseBody{AttemptID: recs[0].AttemptID})
				return
			}
			// No new record (cooldown/window skipped despite bypass=true,
			// or the pipeline short-circuited before the first save).
			c.JSON(http.StatusAccepted, gin.H{"status": "ok"})
			return
		case <-ticker.C:
			if recs, lerr := h.deps.Store.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
				c.JSON(http.StatusAccepted, triggerAppResponseBody{AttemptID: recs[0].AttemptID})
				return
			}
		case <-timeout.C:
			if recs, lerr := h.deps.Store.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
				c.JSON(http.StatusAccepted, triggerAppResponseBody{AttemptID: recs[0].AttemptID})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "trigger did not produce a record in time"})
			return
		}
	}
}

// setAutoUpdateRequestBody is the request shape PATCHed by both
// EdgeClient and DirectClient (see task 6.3). The handler uses this
// boolean to either set or remove the dockpal.auto-update label on the
// running project (R1.4).
type setAutoUpdateRequestBody struct {
	Enabled bool `json:"enabled"`
}

// HandleSetAppAutoUpdate toggles the dockpal.auto-update label on the
// project's compose YAML and redeploys with forcePull=false (R1.4).
// The agent provides the actual rewrite + redeploy logic via the
// SetAutoUpdate closure on AppHandlerDeps so this handler can stay
// transport-agnostic; mapping back to HTTP status codes is the only
// transport-specific work performed here:
//
//   - 200 {"ok": true} on success
//   - 400 when the body is missing or malformed
//   - 404 when the app does not exist (errors.Is(err, ErrAppNotFound))
//   - 500 for any other error
func (h *AppHandler) HandleSetAppAutoUpdate(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing app name"})
		return
	}

	var body setAutoUpdateRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: enabled is required"})
		return
	}

	if err := h.deps.SetAutoUpdate(c.Request.Context(), name, body.Enabled); err != nil {
		if errors.Is(err, ErrAppNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "app not found"})
			return
		}
		internalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
