package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/git"
	"github.com/sdldev/dockpal/internal/health"
	"github.com/sdldev/dockpal/internal/metrics"
	"github.com/sdldev/dockpal/internal/registry"
	"github.com/sdldev/dockpal/internal/traefik"
	"github.com/sdldev/dockpal/internal/tunnel"
	"github.com/sdldev/dockpal/internal/validator"
)

func RegisterRoutes(ctx context.Context, r *gin.Engine, dockerClient *docker.Client, jwtSecret string, database *db.DB, agentMgr *agent.Manager, dataDir string, dbPath string, version string) {
	// Health check endpoints (public, no authentication required)
	healthHandlers := health.NewHandlers(database, dataDir, dockerClient.RawClient(), "v"+version)
	healthHandlers.RegisterHealthRoutes(r)

	registerAPIVersionCompatibility(r)

	api := r.Group("/api")
	api.Use(legacyAPIWarningMiddleware())

	// API Docs (Redoc + OpenAPI spec)
	api.GET("/docs/swagger.json", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.String(http.StatusOK, SwaggerJSON)
	})
	api.GET("/docs", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, `<!DOCTYPE html>
<html>
  <head>
    <title>Dockpal API Documentation</title>
    <meta charset="utf-8"/>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
      body { margin: 0; padding: 2rem; font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #111827; background: #f9fafb; }
      main { max-width: 960px; margin: 0 auto; background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; padding: 2rem; }
      a { color: #2563eb; }
      code { background: #f3f4f6; padding: 0.125rem 0.375rem; border-radius: 4px; }
    </style>
  </head>
  <body>
    <main>
      <h1>Dockpal API Documentation</h1>
      <p>The OpenAPI specification is available locally at <a href="/api/v1/docs/swagger.json"><code>/api/v1/docs/swagger.json</code></a>.</p>
      <p>Legacy clients can still access <a href="/api/docs/swagger.json"><code>/api/docs/swagger.json</code></a>.</p>
    </main>
  </body>
</html>`)
	})

	// Prometheus metrics endpoint (public)
	api.GET("/metrics", func(c *gin.Context) {
		metrics.Handler().ServeHTTP(c.Writer, c.Request)
	})

	// Public client-facing configuration (task 8.2). The UI reads this on
	// boot — before the user logs in — to decide whether to render the
	// auto-update toggle and "Update now" affordances. The endpoint is
	// intentionally unauthenticated and exposes only feature flags, never
	// any secret or operator-scoped value.
	//
	// `auto_update_enabled` reflects the worker's state for the local edge
	// process (DOCKPAL_AUTO_UPDATE_ENABLED). Remote agents have their own
	// worker; the UI banner is a global hint and per-instance overrides
	// are out of scope (see design.md "Components and Interfaces").
	api.GET("/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"auto_update_enabled": globalAutoUpdateWorker.Enabled(),
		})
	})

	// Rate limiters
	loginRateLimiter := NewRateLimiterWithPolicy(LoginRateLimit)
	readRateLimiter := NewRateLimiterWithPolicy(ReadRateLimit)
	mutationRateLimiter := NewRateLimiterWithPolicy(MutationRateLimit)
	webhookRateLimiter := NewRateLimiterWithPolicy(WebhookRateLimit)
	readLimit := RateLimitMiddleware(readRateLimiter)
	mutationLimit := RateLimitMiddleware(mutationRateLimiter)

	// Auth (unprotected)
	api.POST("/login", RateLimitMiddleware(loginRateLimiter), func(c *gin.Context) { auth.HandleLogin(c, jwtSecret, database) })

	// Webhooks public trigger
	api.POST("/webhooks/deploy/:webhook_id", RateLimitMiddleware(webhookRateLimiter), HandleWebhookDeploy(database, agentMgr, jwtSecret))

	baseProtected := api.Group("")
	baseProtected.Use(AuthMiddleware(jwtSecret, database))
	baseProtected.Use(methodRateLimit(readLimit, mutationLimit))

	viewerGroup := baseProtected.Group("")
	viewerGroup.Use(RequireRole(auth.RoleViewer))

	operatorGroup := baseProtected.Group("")
	operatorGroup.Use(RequireRole(auth.RoleOperator))

	adminGroup := baseProtected.Group("")
	adminGroup.Use(RequireRole(auth.RoleAdmin))

	protected := &roleRouterWrapper{
		viewerGroup:   viewerGroup,
		operatorGroup: operatorGroup,
		adminGroup:    adminGroup,
	}

	// Auth protected
	protected.POST("/logout", func(c *gin.Context) { auth.HandleLogout(c, database) })
	protected.POST("/auth/reset-password", RateLimitMiddleware(mutationRateLimiter), func(c *gin.Context) { auth.HandleResetPassword(c, database) })

	// Profile (all authenticated users)
	baseProtected.GET("/profile", func(c *gin.Context) { auth.HandleGetProfile(c, database) })
	baseProtected.PUT("/profile/password", RateLimitMiddleware(mutationRateLimiter), func(c *gin.Context) { auth.HandleChangePassword(c, database) })

	// User management (admin only)
	adminGroup.GET("/users", func(c *gin.Context) { auth.HandleListUsers(c, database) })
	adminGroup.PUT("/users/:username/role", func(c *gin.Context) { auth.HandleUpdateUserRole(c, database) })
	adminGroup.GET("/api-keys", handleListAPIKeys(database))
	adminGroup.POST("/api-keys", handleCreateAPIKey(database))
	adminGroup.DELETE("/api-keys/:id", handleDeleteAPIKey(database))

	// Backup (admin only)
	adminGroup.POST("/backup", HandleTriggerBackup(database, dataDir))

	// Webhooks management
	protected.GET("/webhooks", HandleListWebhooks(database))
	protected.POST("/webhooks", HandleCreateWebhook(database))
	protected.DELETE("/webhooks/:webhook_id", HandleDeleteWebhook(database))

	// Instance management routes (new)
	logsManager := NewInstallLogsManager()
	RegisterInstanceRoutes(baseProtected, database, agentMgr, jwtSecret, logsManager)

	// Agent WebSocket endpoint (unauthenticated — agent uses token in message)
	r.GET("/api/agent/connect", HandleAgentConnect(database, agentMgr))

	// Instance-scoped operations (new route group)
	instanceBase := api.Group("/instances/:instance_id")
	instanceBase.Use(InstanceMiddleware(agentMgr, database, jwtSecret))
	instanceBase.GET("/containers/:id/logs", readLimit, handleInstanceContainerLogs)

	instances := baseProtected.Group("/instances/:instance_id")
	instances.Use(InstanceMiddleware(agentMgr, database, jwtSecret))
	RegisterInstanceScopedRoutes(instances)

	// Registry credentials
	registryManager := registry.NewManager(database, jwtSecret)

	// Image update monitor
	imageUpdateMonitor := docker.NewImageUpdateMonitor(dockerClient, func(imageRef string) (string, error) {
		return registryManager.GetAuthHeader(imageRef)
	})
	imageUpdateMonitor.Start()

	// Auto-update wiring (task 5.2):
	//   - AppUpdateFeed broadcasts stage events to SSE subscribers.
	//   - AutoUpdateWorker consumes ImageUpdateMonitor cycle events and
	//     drives the per-app pull → recreate → verify → rollback pipeline.
	//
	// The worker uses docker.AppUpdateFeedPayload (not server.AppUpdateFeedEvent
	// directly) to avoid a server → docker import cycle. We provide a small
	// adapter that translates the worker payload into the server event type
	// before calling feed.Publish.
	//
	// The compose YAML for an app is resolved by name from the local-instance
	// services bucket. Apps that have not been deployed via Dockpal (or that
	// have no compose body persisted) cause TriggerApp to return an error,
	// matching the design for "compose not configured for app".
	//
	// instanceID is "local" because this worker runs in the local edge
	// process. Remote agents wire their own AutoUpdateWorker inside the
	// agent process (task 6.4).
	feed := NewAppUpdateFeed()

	// getCompose resolves the compose YAML for a project (dockpal.project
	// label). The project name corresponds to db.Service.Name on the local
	// instance, which is the only instance the worker manages today.
	// We search all services because some may have been deployed via the
	// instance-scoped route (InstanceID="local") while others via the
	// legacy route (InstanceID="").
	getCompose := func(project string) (string, error) {
		services, err := database.ListServices()
		if err != nil {
			return "", err
		}
		for _, s := range services {
			if s.Name == project && (s.InstanceID == "" || s.InstanceID == "local") {
				return s.Compose, nil
			}
		}
		return "", fmt.Errorf("compose not found for project %q", project)
	}

	// feedAdapter translates the worker's internal payload into the
	// server-side feed event. The payload struct mirrors the event shape
	// field-for-field so this is a one-to-one copy; the indirection exists
	// only to avoid a server → docker import cycle.
	feedAdapter := func(p docker.AppUpdateFeedPayload) {
		feed.Publish(AppUpdateFeedEvent{
			AttemptID:  p.AttemptID,
			InstanceID: p.InstanceID,
			App:        p.App,
			Stage:      p.Stage,
			ErrorCode:  p.ErrorCode,
			Message:    p.Message,
			At:         p.At,
		})
	}

	worker := docker.NewAutoUpdateWorker(
		dockerClient,
		imageUpdateMonitor,
		database,
		feedAdapter,
		registryManager.GetAuthHeader,
		getCompose,
		"local", // instanceID="local" — this worker drives the local edge process
	)
	// Install Prometheus instrumentation hooks (task 9.1, R10.1-R10.3).
	// The worker stays decoupled from internal/metrics through this
	// indirection, which keeps the metrics package importable from
	// internal/server (where the registrar lives) without forcing a
	// docker → metrics → agent → docker import cycle.
	worker.SetMetricsHooks(docker.AutoUpdateMetricsHooks{
		Attempt:       metrics.AutoUpdateAttempt,
		Duration:      metrics.AutoUpdateDuration,
		PendingUpdate: metrics.SetAppsPendingUpdate,
	})
	// Install webhook lister so the worker can send best-effort notifications
	// on rolled_back or failed (rollback_failed) outcomes (task 10.1, R3.5).
	worker.SetWebhookLister(database.ListNotificationWebhooks)
	worker.Start(ctx)

	// Expose the feed and worker to handler tasks (5.3, 5.4) and the
	// agent client local impl (6.2) via package-level references. They
	// are reassigned per RegisterRoutes call so tests stay isolated.
	globalAppUpdateFeed = feed
	globalAutoUpdateWorker = worker
	// globalDockerClient and globalImageUpdateMonitor let the
	// instance-scoped handlers (task 5.4) reuse the local docker layer
	// when instance_id == "local". globalRegistryManager exposes the
	// in-process registry manager for the same path so the local SetApp
	// AutoUpdate handler can resolve registry auths during the redeploy.
	globalDockerClient = dockerClient
	globalImageUpdateMonitor = imageUpdateMonitor
	globalRegistryManager = registryManager

	// Wire the agent.LocalClient app-ops dependencies (task 6.2). The
	// LocalClient methods ListApps / ListAppUpdates / GetAppUpdate /
	// TriggerAppUpdate / SetAppAutoUpdate previously returned
	// errAppOpsNotWired stubs; with the worker, monitor, store, and a
	// SetAutoUpdate closure they delegate properly. The closure mirrors
	// PATCH /apps/:name/auto-update: rewrite the compose YAML via
	// docker.SetServiceLabel, persist db.Service, and redeploy with
	// forcePull=false. Threading this work through a closure keeps the
	// agent package free of *db.DB and *registry.Manager imports.
	localSetAutoUpdate := func(ctx context.Context, app string, enabled bool) error {
		if app == "" {
			return fmt.Errorf("set auto-update: empty app")
		}
		services, err := database.ListServices()
		if err != nil {
			return err
		}
		var svc *db.Service
		for i := range services {
			if services[i].Name == app && (services[i].InstanceID == "" || services[i].InstanceID == "local") {
				svc = &services[i]
				break
			}
		}
		if svc == nil {
			return fmt.Errorf("app %q not found", app)
		}
		if svc.Compose == "" {
			return fmt.Errorf("app %q has no compose YAML to patch", app)
		}
		labelValue := "true"
		if !enabled {
			labelValue = "" // empty string removes the label
		}
		newCompose, err := docker.SetServiceLabel(svc.Compose, "dockpal.auto-update", labelValue)
		if err != nil {
			return err
		}
		// Persist the updated compose body before redeploying so a redeploy
		// failure does not leave the DB out of sync with the actual
		// containers.
		updated := *svc
		updated.Compose = newCompose
		if err := database.SaveService(updated); err != nil {
			return err
		}
		registryAuths := getRegistryAuths(registryManager, newCompose)
		client, err := agentMgr.GetClient("local")
		if err != nil {
			return err
		}
		return client.DeployCompose(ctx, app, newCompose, registryAuths, false)
	}
	agentMgr.WireLocalAppOps(agent.LocalAppOps{
		Worker:        worker,
		Monitor:       imageUpdateMonitor,
		Store:         database,
		SetAutoUpdate: localSetAutoUpdate,
	})

	// =============================================================================
	// App auto-update HTTP endpoints (task 5.3).
	//
	// Routes:
	//   GET   /apps                           viewer+   list apps with update state
	//   GET   /apps/:name/updates             viewer+   list update history (limit=50)
	//   GET   /apps/:name/updates/:attemptID  viewer+   single record + events
	//   POST  /apps/:name/update              operator+ trigger manual update
	//   PATCH /apps/:name/auto-update         operator+ toggle auto-update label
	//   GET   /apps/updates/stream            viewer+   SSE stream of feed events
	//
	// Role enforcement comes from the roleRouterWrapper (GET → viewer,
	// POST/PATCH → operator). The endpoints below close over `worker`,
	// `feed`, `imageUpdateMonitor`, `database`, `registryManager`, and
	// `agentMgr` from the surrounding RegisterRoutes scope.

	protected.GET("/apps", func(c *gin.Context) {
		// The handler here lists local apps; the docker.Client.ListApps method
		// requires a *docker.Client (not the agent.AgentClient interface) so
		// we use dockerClient directly. The optional `instance_id` query is a
		// filter passthrough — when set to anything other than "local" or
		// empty the response is an empty slice, matching R9.3 (instance-scoped
		// requests use the /instances/:instance_id/apps route added in 5.4).
		instanceFilter := c.Query("instance_id")
		if instanceFilter != "" && instanceFilter != "local" {
			c.JSON(http.StatusOK, []docker.AppSummary{})
			return
		}
		apps, err := dockerClient.ListApps(c.Request.Context(), imageUpdateMonitor, database)
		if err != nil {
			internalError(c, err)
			return
		}
		// Stamp instance_id on each summary so the UI can route per-instance
		// without a second lookup.
		for i := range apps {
			apps[i].InstanceID = "local"
		}
		c.JSON(http.StatusOK, apps)
	})

	protected.GET("/apps/:name/updates", func(c *gin.Context) {
		name := c.Param("name")
		limit := 50
		if v := c.Query("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
				limit = n
			}
		}
		recs, err := database.ListAppUpdates(name, limit)
		if err != nil {
			internalError(c, err)
			return
		}
		// Always return a non-nil slice so the JSON encoder emits `[]`.
		if recs == nil {
			recs = []db.AppUpdateRecord{}
		}
		c.JSON(http.StatusOK, recs)
	})

	protected.GET("/apps/:name/updates/:attemptID", func(c *gin.Context) {
		attemptID := c.Param("attemptID")
		rec, err := database.GetAppUpdate(attemptID)
		if err != nil {
			internalError(c, err)
			return
		}
		if rec == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "attempt not found"})
			return
		}
		// Defensive cross-check: the attempt's App must match the URL :name
		// so the endpoint cannot be used to enumerate other apps' attempts.
		if name := c.Param("name"); name != "" && rec.App != name {
			c.JSON(http.StatusNotFound, gin.H{"error": "attempt not found"})
			return
		}
		c.JSON(http.StatusOK, rec)
	})

	protected.POST("/apps/:name/update", func(c *gin.Context) {
		if globalAutoUpdateWorker == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auto-update worker not configured"})
			return
		}
		name := c.Param("name")
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing app name"})
			return
		}

		// Resolve the actor for the App_Update_Record's `triggered_by` field.
		username := "user"
		if v, ok := c.Get("username"); ok {
			if s, ok := v.(string); ok && s != "" {
				username = s
			}
		}
		triggeredBy := "user:" + username

		// Audit logging (R8.4): every user-triggered TriggerApp call
		// records one `app_update_attempted` entry once the response code
		// is known. The defer reads c.Writer.Status() after the handler
		// has called c.JSON(...) so the audit `result` reflects the same
		// outcome the operator saw on the wire. The wrapping closure is
		// load-bearing: argument expressions to a deferred call are
		// evaluated at defer-registration time, but the status code is
		// only set later by c.JSON, so the read must happen inside the
		// deferred function body.
		defer func() {
			LogAppUpdateAttempt(c, database, dockerClient, name, auditAppUpdateResultFor(c.Writer.Status()))
		}()

		// Snapshot the latest attempt id so we can detect the new record once
		// the worker has persisted its first stage event. A nil/empty result
		// here just means this app has no prior attempts — that is normal.
		var prevAttempt string
		if recs, err := database.ListAppUpdates(name, 1); err == nil && len(recs) > 0 {
			prevAttempt = recs[0].AttemptID
		}

		// Run the pipeline asynchronously so the HTTP request returns
		// quickly. The TriggerApp call holds a per-app mutex; if another
		// trigger is already in flight, it returns an error containing
		// docker.ErrUpdateAlreadyRunning (mapped to HTTP 409 below).
		errCh := make(chan error, 1)
		go func() {
			// Use context.Background() so the pipeline can outlive the HTTP
			// request. Cancellation comes from the worker's own Stop() path.
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
				// TriggerApp finished without error before the poll picked up
				// a new record. Look up the most recent attempt to return its
				// id (the pipeline always saves at least one record on the
				// happy path).
				if recs, lerr := database.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
					c.JSON(http.StatusAccepted, gin.H{"attempt_id": recs[0].AttemptID})
					return
				}
				// No new record (cooldown/window skipped despite bypass=true,
				// or the trigger short-circuited before the first save).
				c.JSON(http.StatusAccepted, gin.H{"status": "ok"})
				return
			case <-ticker.C:
				if recs, lerr := database.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
					c.JSON(http.StatusAccepted, gin.H{"attempt_id": recs[0].AttemptID})
					return
				}
			case <-timeout.C:
				if recs, lerr := database.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
					c.JSON(http.StatusAccepted, gin.H{"attempt_id": recs[0].AttemptID})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "trigger did not produce a record in time"})
				return
			}
		}
	})

	protected.PATCH("/apps/:name/auto-update", func(c *gin.Context) {
		name := c.Param("name")
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing app name"})
			return
		}

		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: enabled is required"})
			return
		}

		// Locate the db.Service for this app on the local instance. The
		// project name on running containers (dockpal.project label) matches
		// db.Service.Name, so we look it up by name. Services may have been
		// deployed via the instance-scoped route (InstanceID="local") or the
		// legacy route (InstanceID="").
		services, err := database.ListServices()
		if err != nil {
			internalError(c, err)
			return
		}
		var svc *db.Service
		for i := range services {
			if services[i].Name == name && (services[i].InstanceID == "" || services[i].InstanceID == "local") {
				svc = &services[i]
				break
			}
		}
		if svc == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "app not found"})
			return
		}
		if svc.Compose == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "app has no compose YAML to patch"})
			return
		}

		// Rewrite the compose YAML: set or remove the dockpal.auto-update
		// label on every service. SetServiceLabel preserves comments and
		// sibling labels.
		labelValue := "true"
		if !req.Enabled {
			labelValue = "" // empty string removes the label
		}
		newCompose, err := docker.SetServiceLabel(svc.Compose, "dockpal.auto-update", labelValue)
		if err != nil {
			internalError(c, err)
			return
		}

		// Persist the updated compose body before redeploying so a redeploy
		// failure does not leave the DB out of sync with the actual containers.
		updated := *svc
		updated.Compose = newCompose
		if err := database.SaveService(updated); err != nil {
			internalError(c, err)
			return
		}

		// Recreate containers with the new label. forcePull=false means the
		// existing local image is reused; the only change is the container's
		// label set.
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		registryAuths := getRegistryAuths(registryManager, newCompose)
		if err := client.DeployCompose(c.Request.Context(), name, newCompose, registryAuths, false); err != nil {
			internalError(c, err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	protected.GET("/apps/updates/stream", func(c *gin.Context) {
		if globalAppUpdateFeed == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "feed not configured"})
			return
		}

		// SSE headers. X-Accel-Buffering=no disables buffering on
		// nginx/reverse proxies so events arrive at the client promptly.
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		ch, unsubscribe := globalAppUpdateFeed.Subscribe()
		defer unsubscribe()

		// Flush headers immediately so the client transitions out of "loading"
		// state even before the first event arrives.
		c.Writer.Flush()

		ctx := c.Request.Context()
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				data, err := json.Marshal(ev)
				if err != nil {
					// Skip malformed events rather than tearing down the stream.
					continue
				}
				if _, err := c.Writer.Write([]byte("data: ")); err != nil {
					return
				}
				if _, err := c.Writer.Write(data); err != nil {
					return
				}
				if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
					return
				}
				c.Writer.Flush()
			case <-ctx.Done():
				return
			}
		}
	})

	protected.GET("/registries", func(c *gin.Context) {
		list, err := registryManager.List()
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, list)
	})

	protected.POST("/registries", func(c *gin.Context) {
		var req registry.CreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: registry, username, and token are required"})
			return
		}
		cred, err := registryManager.Create(req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, cred)
	})

	protected.GET("/registries/:id", func(c *gin.Context) {
		cred, err := registryManager.Get(c.Param("id"))
		if err != nil {
			if errors.Is(err, registry.ErrCredentialNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, cred)
	})

	protected.PUT("/registries/:id", func(c *gin.Context) {
		var req registry.UpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if err := registryManager.Update(c.Param("id"), req); err != nil {
			if errors.Is(err, registry.ErrCredentialNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
	})

	protected.DELETE("/registries/:id", func(c *gin.Context) {
		if err := registryManager.Delete(c.Param("id")); err != nil {
			if errors.Is(err, registry.ErrCredentialNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	})

	protected.POST("/registries/:id/test", func(c *gin.Context) {
		result, err := registryManager.TestConnection(c.Param("id"))
		if err != nil {
			if errors.Is(err, registry.ErrCredentialNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})

	// Containers
	protected.GET("/containers", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		containers, err := client.ListContainers(c.Request.Context(), true)
		if err != nil {
			internalError(c, err)
			return
		}
		markProtectedContainerInfos(containers)
		c.JSON(http.StatusOK, containers)
	})

	protected.GET("/containers/:id", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		detail, err := client.InspectContainer(c.Request.Context(), c.Param("id"))
		if err != nil {
			internalError(c, err)
			return
		}
		markProtectedContainerDetail(detail)
		c.JSON(http.StatusOK, detail)
	})

	protected.POST("/containers/:id/start", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		if err := client.StartContainer(c.Request.Context(), c.Param("id")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "started"})
	})

	protected.POST("/containers/:id/stop", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		if err := client.StopContainer(c.Request.Context(), c.Param("id")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "stopped"})
	})

	protected.POST("/containers/:id/restart", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		if err := client.RestartContainer(c.Request.Context(), c.Param("id")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "restarted"})
	})

	protected.DELETE("/containers/:id", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		containerID := c.Param("id")
		force := c.Query("force") == "true"
		if err := ensureContainerRemovable(c.Request.Context(), client, containerID); err != nil {
			if errors.Is(err, errProtectedDockpalAgentContainer) {
				c.JSON(http.StatusForbidden, gin.H{"error": dockpalAgentProtectionReason, "protected": true})
				return
			}
			internalError(c, err)
			return
		}
		if err := client.RemoveContainer(c.Request.Context(), containerID, force); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "removed"})
	})

	// Container edit (in-place + recreate)
	protected.PUT("/containers/:id", func(c *gin.Context) {
		containerID := c.Param("id")

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		var req docker.ContainerEditRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		// Validate name if provided
		if req.Name != nil {
			if err := validator.ValidateContainerName(*req.Name); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid name: %s", err.Error())})
				return
			}
		}

		// Validate restart policy if provided
		if req.RestartPolicy != nil {
			validPolicies := map[string]bool{"no": true, "always": true, "unless-stopped": true, "on-failure": true}
			if !validPolicies[*req.RestartPolicy] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid restart policy: must be one of no, always, unless-stopped, on-failure"})
				return
			}
		}

		// Validate memory limit if provided (must be non-negative)
		if req.MemoryLimit != nil && *req.MemoryLimit < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "memory limit must be non-negative"})
			return
		}

		// Validate CPU limit if provided (must be non-negative; 0 means unlimited)
		if req.CPULimit != nil && *req.CPULimit < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CPU limit must be non-negative"})
			return
		}

		// Validate env vars if provided
		if req.Env != nil {
			for _, env := range *req.Env {
				if err := validator.ValidateEnvVarValue(env); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid env var: %s", err.Error())})
					return
				}
			}
		}

		// Validate ports if provided
		if req.Ports != nil {
			for _, pm := range *req.Ports {
				if pm.ContainerPort < 1 || pm.ContainerPort > 65535 {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid container port: %d", pm.ContainerPort)})
					return
				}
				if pm.HostPort < 1 || pm.HostPort > 65535 {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid host port: %d", pm.HostPort)})
					return
				}
				if pm.Protocol != "tcp" && pm.Protocol != "udp" && pm.Protocol != "" {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid protocol: %s (must be tcp or udp)", pm.Protocol)})
					return
				}
			}
		}

		// Validate volumes if provided
		if req.Volumes != nil {
			for _, vm := range *req.Volumes {
				if vm.ContainerPath == "" {
					c.JSON(http.StatusBadRequest, gin.H{"error": "volume container path cannot be empty"})
					return
				}
				if vm.HostPath == "" {
					c.JSON(http.StatusBadRequest, gin.H{"error": "volume host path cannot be empty"})
					return
				}
			}
		}

		// Warn if recreate is needed
		needsRecreate := req.Image != nil || req.Env != nil || req.Ports != nil || req.Volumes != nil
		if needsRecreate {
			if err := ensureContainerRemovable(c.Request.Context(), client, containerID); err != nil {
				if errors.Is(err, errProtectedDockpalAgentContainer) {
					c.JSON(http.StatusForbidden, gin.H{"error": "Dockpal agent container cannot be recreated from Dockpal", "protected": true})
					return
				}
				internalError(c, err)
				return
			}
		}

		detail, err := client.EditContainer(c.Request.Context(), containerID, req)
		if err != nil {
			internalError(c, err)
			return
		}

		response := gin.H{
			"status":    "updated",
			"container": detail,
		}
		if needsRecreate {
			response["recreated"] = true
		}
		c.JSON(http.StatusOK, response)
	})

	protected.GET("/containers/:id/stats", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		stats, err := client.GetContainerStats(c.Request.Context(), c.Param("id"))
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, stats)
	})

	// WebSocket logs
	api.GET("/containers/:id/logs", readLimit, func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		c.Set("jwt_secret", jwtSecret)
		c.Set("database", database)
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		if !authenticateWebSocketFirstMessage(conn, c) {
			conn.Close()
			return
		}

		reader, err := client.ContainerLogs(c.Request.Context(), c.Param("id"), "100")
		if err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte("Error: failed to retrieve container logs"))
			conn.Close()
			return
		}

		streamContainerLogs(conn, reader)
	})

	// Deploy
	deployManager := globalDeployManager

	// Streamed deploy endpoint - returns deploy session ID
	protected.POST("/deploy/stream", func(c *gin.Context) {
		var req struct {
			Name    string `json:"name" binding:"required"`
			Domain  string `json:"domain"`
			Compose string `json:"compose" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if err := validator.ValidateContainerName(req.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid name: %s", err.Error())})
			return
		}
		if req.Domain != "" {
			if err := validator.ValidateDomain(req.Domain); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid domain: %s", err.Error())})
				return
			}
		}

		// Get auth headers for registries
		registryAuths := getRegistryAuths(registryManager, req.Compose)

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		session := deployManager.CreateSession()

		// Run deploy in background goroutine
		go func() {
			err := client.DeployComposeStreamed(context.Background(), req.Name, req.Compose, session, registryAuths, false)
			if err == nil {
				database.SaveService(db.Service{
					ID:        generateID("svc"),
					Name:      req.Name,
					Type:      "compose",
					Domain:    req.Domain,
					Compose:   req.Compose,
					CreatedAt: time.Now().Unix(),
				})
				if req.Domain != "" {
					port := extractFirstPort(req.Compose)
					traefik.GenerateConfig(req.Domain, req.Name, port)
				}
			}
			// Clean up session after 30 seconds
			time.AfterFunc(30*time.Second, func() {
				deployManager.RemoveSession(session.ID)
			})
		}()

		c.JSON(http.StatusOK, gin.H{"deploy_id": session.ID})
	})

	// WebSocket endpoint for deploy log streaming (uses query param auth)
	// Note: WebSocket cannot send custom headers during upgrade, so token
	// must be passed as query param. The token is short-lived (30 days max)
	// and the endpoint is read-only (streaming logs only).
	deployStreamWS := handleDeployStreamWS(jwtSecret, database, deployManager)
	r.GET("/api/deploy/stream/:id", legacyAPIWarningMiddleware(), deployStreamWS)

	// Instance-scoped WebSocket endpoint for deploy log streaming.
	// Same logic as above but matches the instance-scoped URL pattern used by the frontend.
	r.GET("/api/instances/:instance_id/deploy/stream/:id", legacyAPIWarningMiddleware(), deployStreamWS)

	protected.POST("/deploy/compose", func(c *gin.Context) {
		var req struct {
			Name    string `json:"name" binding:"required"`
			Domain  string `json:"domain"`
			Compose string `json:"compose" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		if err := validator.ValidateContainerName(req.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid name: %s", err.Error())})
			return
		}

		// Get auth headers for registries
		registryAuths := getRegistryAuths(registryManager, req.Compose)

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		if err := client.DeployCompose(c.Request.Context(), req.Name, req.Compose, registryAuths, false); err != nil {
			internalError(c, err)
			return
		}

		database.SaveService(db.Service{
			ID:        generateID("svc"),
			Name:      req.Name,
			Type:      "compose",
			Domain:    req.Domain,
			Compose:   req.Compose,
			CreatedAt: time.Now().Unix(),
		})

		// Generate Traefik config when domain is specified
		if req.Domain != "" {
			port := extractFirstPort(req.Compose)
			if err := traefik.GenerateConfig(req.Domain, req.Name, port); err != nil {
				log.Printf("Warning: failed to generate traefik config: %v", err)
			}
		}

		c.JSON(http.StatusOK, gin.H{"status": "deployed"})
	})

	protected.POST("/deploy/git", func(c *gin.Context) {
		var req struct {
			Repo        string `json:"repo" binding:"required"`
			Branch      string `json:"branch"`
			ComposeFile string `json:"compose_file"`
			Name        string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		if err := validator.ValidateGitURL(req.Repo); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid repo: %s", err.Error())})
			return
		}
		if req.Branch != "" {
			if err := validator.ValidateBranchName(req.Branch); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid branch: %s", err.Error())})
				return
			}
		}

		// Auto-fetch GitHub token from stored registry credentials
		token, _ := registryManager.GetTokenForDomain("github.com")

		info, err := git.Clone(req.Repo, req.Branch, token)
		if err != nil {
			errMsg := err.Error()
			if strings.Contains(errMsg, "authentication") || strings.Contains(errMsg, "Authorization") ||
				strings.Contains(errMsg, "denied") || strings.Contains(errMsg, "not found") {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication failed: repository not accessible. Add a GitHub credential in Settings > Registry with registry 'github.com' and a PAT with repo scope."})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to clone repository: %s", errMsg)})
			return
		}

		if len(info.ComposeFiles) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no docker-compose file found in repository"})
			return
		}

		// If multiple compose files and none selected, return list for user to choose
		if len(info.ComposeFiles) > 1 && req.ComposeFile == "" {
			c.JSON(http.StatusOK, gin.H{"status": "select_compose", "compose_files": info.ComposeFiles, "info": info})
			return
		}

		// Determine which compose file to use
		selectedFile := req.ComposeFile
		if selectedFile == "" {
			selectedFile = info.ComposeFiles[0]
		}

		// Validate selected file exists in the list
		validFile := false
		for _, f := range info.ComposeFiles {
			if f == selectedFile {
				validFile = true
				break
			}
		}
		if !validFile {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("compose file '%s' not found in repository", selectedFile)})
			return
		}

		// Use repo name as project name (not full path), or user-provided name
		projectName := req.Name
		if projectName == "" {
			projectName = filepath.Base(info.Path)
		}
		if err := validator.ValidateContainerName(projectName); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid name: %s", err.Error())})
			return
		}

		composePath := filepath.Join(info.Path, selectedFile)
		composeData, err := os.ReadFile(composePath)
		if err != nil {
			internalError(c, err)
			return
		}

		// Get auth headers for registries
		registryAuths := getRegistryAuths(registryManager, string(composeData))

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		if err := client.DeployCompose(c.Request.Context(), projectName, string(composeData), registryAuths, false); err != nil {
			internalError(c, err)
			return
		}

		database.SaveService(db.Service{
			ID:        generateID("svc"),
			Name:      projectName,
			Type:      "git",
			Repo:      req.Repo,
			CreatedAt: time.Now().Unix(),
		})

		c.JSON(http.StatusOK, gin.H{"status": "deployed", "info": info})
	})

	// GitHub repository listing — uses stored github.com registry credential
	protected.GET("/github/repos", func(c *gin.Context) {
		token, _ := registryManager.GetTokenForDomain("github.com")
		if token == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No GitHub credential found. Add a registry with domain 'github.com' in Settings > Registry."})
			return
		}

		pageNum, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		if pageNum < 1 {
			pageNum = 1
		}
		perPageNum, _ := strconv.Atoi(c.DefaultQuery("per_page", "30"))
		if perPageNum < 1 || perPageNum > 100 {
			perPageNum = 30
		}

		apiURL := fmt.Sprintf("https://api.github.com/user/repos?sort=updated&direction=desc&page=%d&per_page=%d&type=all", pageNum, perPageNum)
		req, err := http.NewRequestWithContext(c.Request.Context(), "GET", apiURL, nil)
		if err != nil {
			internalError(c, err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github.v3+json")

		resp, err := githubHTTPClient.Do(req)
		if err != nil {
			internalError(c, err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "GitHub token is invalid or expired. Update the credential in Settings > Registry."})
			return
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			internalError(c, err)
			return
		}

		var repos []json.RawMessage
		if err := json.Unmarshal(body, &repos); err != nil {
			internalError(c, err)
			return
		}

		type repoSummary struct {
			FullName      string `json:"full_name"`
			CloneURL      string `json:"clone_url"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
			Description   string `json:"description"`
			UpdatedAt     string `json:"updated_at"`
		}

		var results []repoSummary
		for _, raw := range repos {
			var r struct {
				FullName      string `json:"full_name"`
				CloneURL      string `json:"clone_url"`
				DefaultBranch string `json:"default_branch"`
				Private       bool   `json:"private"`
				Description   string `json:"description"`
				UpdatedAt     string `json:"updated_at"`
			}
			if err := json.Unmarshal(raw, &r); err == nil {
				results = append(results, repoSummary{
					FullName:      r.FullName,
					CloneURL:      r.CloneURL,
					DefaultBranch: r.DefaultBranch,
					Private:       r.Private,
					Description:   r.Description,
					UpdatedAt:     r.UpdatedAt,
				})
			}
		}

		c.JSON(http.StatusOK, results)
	})

	protected.GET("/services", func(c *gin.Context) {
		services, err := database.ListServices()
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, services)
	})

	protected.DELETE("/services/:id", func(c *gin.Context) {
		svc, err := database.GetService(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
			return
		}

		if svc.Type == "compose" {
			dockerClient.RemoveCompose(c.Request.Context(), svc.Name)
		}

		// Remove Traefik config when service has an associated domain
		if svc.Domain != "" {
			if err := traefik.RemoveDomain(svc.Name); err != nil {
				log.Printf("Warning: failed to remove traefik config for %s: %v", svc.Name, err)
			}
		}

		database.DeleteService(c.Param("id"))
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	})

	// Templates
	protected.GET("/templates", func(c *gin.Context) {
		templates, err := getCachedTemplates(5 * time.Minute)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, templates)
	})

	protected.GET("/templates/:id", func(c *gin.Context) {
		templates, err := getCachedTemplates(5 * time.Minute)
		if err != nil {
			internalError(c, err)
			return
		}
		for _, t := range templates {
			if t.ID == c.Param("id") {
				c.JSON(http.StatusOK, t)
				return
			}
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
	})

	protected.POST("/templates/:id/deploy", func(c *gin.Context) {
		templates, err := getCachedTemplates(5 * time.Minute)
		if err != nil {
			internalError(c, err)
			return
		}

		var tpl *Template
		for _, t := range templates {
			if t.ID == c.Param("id") {
				tpl = &t
				break
			}
		}
		if tpl == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
			return
		}

		var req struct {
			Env map[string]string `json:"env"`
		}
		c.ShouldBindJSON(&req)

		// Validate environment variable names and values
		for k, v := range req.Env {
			if err := validator.ValidateEnvVarName(k); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid env var name '%s': %s", k, err.Error())})
				return
			}
			if err := validator.ValidateEnvVarValue(v); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid env var value for '%s': %s", k, err.Error())})
				return
			}
		}

		compose := tpl.Compose
		for k, v := range req.Env {
			compose = strings.ReplaceAll(compose, "${"+k+"}", v)
		}

		// Get auth headers for registries
		registryAuths := getRegistryAuths(registryManager, compose)

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		name := tpl.ID + "-" + fmt.Sprintf("%d", time.Now().Unix())
		if err := client.DeployCompose(c.Request.Context(), name, compose, registryAuths, false); err != nil {
			internalError(c, err)
			return
		}

		database.SaveService(db.Service{
			ID:        generateID("svc"),
			Name:      name,
			Type:      "template",
			Compose:   compose,
			CreatedAt: time.Now().Unix(),
		})

		c.JSON(http.StatusOK, gin.H{"status": "deployed", "name": name})
	})

	// Streamed template deploy
	protected.POST("/templates/:id/deploy/stream", func(c *gin.Context) {
		templates, err := getCachedTemplates(5 * time.Minute)
		if err != nil {
			internalError(c, err)
			return
		}

		var tpl *Template
		for _, t := range templates {
			if t.ID == c.Param("id") {
				tpl = &t
				break
			}
		}
		if tpl == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
			return
		}

		var req struct {
			Env           map[string]string `json:"env"`
			Ports         map[string]int    `json:"ports"`
			CustomName    string            `json:"custom_name"`
			RestartPolicy string            `json:"restart_policy"`
			AutoRecover   bool              `json:"auto_recover"`
			Domain        string            `json:"domain"`
		}
		c.ShouldBindJSON(&req)

		compose := tpl.Compose
		for k, v := range req.Env {
			if err := validator.ValidateEnvVarName(k); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid env var name '%s': %s", k, err.Error())})
				return
			}
			if err := validator.ValidateEnvVarValue(v); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid env var value for '%s': %s", k, err.Error())})
				return
			}
			compose = strings.ReplaceAll(compose, "${"+k+"}", v)
		}
		// Replace port placeholders
		for _, p := range tpl.Ports {
			hostPort := p.Default
			if customPort, ok := req.Ports[fmt.Sprintf("%d", p.ContainerPort)]; ok && customPort > 0 {
				hostPort = customPort
			}
			oldPort := fmt.Sprintf("'%d:%d'", p.Default, p.ContainerPort)
			newPort := fmt.Sprintf("'%d:%d'", hostPort, p.ContainerPort)
			compose = strings.ReplaceAll(compose, oldPort, newPort)
		}
		// Apply restart policy override
		if req.RestartPolicy != "" && req.RestartPolicy != "unless-stopped" {
			compose = strings.ReplaceAll(compose, "unless-stopped", req.RestartPolicy)
		}
		// Add auto-recover label if requested
		if req.AutoRecover {
			compose = strings.ReplaceAll(compose, "image: ", "labels:\n      dockpal.auto-recover: \"true\"\n    image: ")
		}

		name := tpl.ID + "-" + fmt.Sprintf("%d", time.Now().Unix())
		if req.CustomName != "" {
			if err := validator.ValidateContainerName(req.CustomName); err == nil {
				name = req.CustomName
			}
		}

		// Get auth headers for registries
		registryAuths := getRegistryAuths(registryManager, compose)

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		session := deployManager.CreateSession()

		go func() {
			err := client.DeployComposeStreamed(context.Background(), name, compose, session, registryAuths, false)
			if err == nil {
				database.SaveService(db.Service{
					ID:        generateID("svc"),
					Name:      name,
					Type:      "template",
					Domain:    req.Domain,
					Compose:   compose,
					CreatedAt: time.Now().Unix(),
				})
				if req.Domain != "" {
					port := extractFirstPort(compose)
					traefik.GenerateConfig(req.Domain, name, port)
				}
			}
			time.AfterFunc(30*time.Second, func() {
				deployManager.RemoveSession(session.ID)
			})
		}()

		c.JSON(http.StatusOK, gin.H{"deploy_id": session.ID})
	})

	// Images
	protected.GET("/images", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		images, err := client.ListImages(c.Request.Context())
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, images)
	})

	protected.POST("/images/pull", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		var req struct {
			Image string `json:"image" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		// Try authenticated pull if credentials are available
		authHeader, _ := registryManager.GetAuthHeader(req.Image)
		if authHeader != "" {
			if err := client.PullImageWithAuth(c.Request.Context(), req.Image, authHeader); err != nil {
				internalError(c, err)
				return
			}
		} else {
			// Fallback to unauthenticated pull
			if err := client.PullImage(c.Request.Context(), req.Image); err != nil {
				internalError(c, err)
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "pulled"})
	})

	protected.DELETE("/images/:id", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		if err := client.RemoveImage(c.Request.Context(), c.Param("id")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "removed"})
	})

	// Image Update Mechanism
	protected.GET("/images/updates", func(c *gin.Context) {
		if imageUpdateMonitor == nil {
			c.JSON(http.StatusOK, gin.H{"updates": []docker.ImageUpdateStatus{}})
			return
		}
		updates := imageUpdateMonitor.GetAllStatuses()
		c.JSON(http.StatusOK, gin.H{"updates": updates})
	})

	protected.POST("/images/check", func(c *gin.Context) {
		var req struct {
			Image string `json:"image" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		result, err := client.CheckImageUpdate(c.Request.Context(), req.Image)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})

	protected.POST("/images/pull-force", func(c *gin.Context) {
		var req struct {
			Image string `json:"image" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		authHeader, _ := registryManager.GetAuthHeader(req.Image)
		if err := client.ForcePullImage(c.Request.Context(), req.Image, authHeader); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "pulled"})
	})

	// Image Prune
	protected.POST("/images/prune", RequireRole(auth.RoleOperator), func(c *gin.Context) {
		var req struct {
			DanglingOnly bool `json:"dangling_only"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			req.DanglingOnly = true
		}
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		result, err := client.PruneImages(c.Request.Context(), req.DanglingOnly)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})

	// File Manager
	protected.GET("/files", func(c *gin.Context) {
		containerID := c.Query("container")
		path := c.Query("path")
		if containerID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "container query param required"})
			return
		}
		files, err := dockerClient.ListFiles(c.Request.Context(), containerID, path)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, files)
	})

	protected.GET("/files/read", func(c *gin.Context) {
		content, err := dockerClient.ReadFile(c.Request.Context(), c.Query("container"), c.Query("path"))
		if err != nil {
			internalError(c, err)
			return
		}
		c.String(http.StatusOK, content)
	})

	protected.POST("/files/write", func(c *gin.Context) {
		var req struct {
			Container string `json:"container"`
			Path      string `json:"path"`
			Content   string `json:"content"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if err := dockerClient.WriteFile(c.Request.Context(), req.Container, req.Path, req.Content); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "written"})
	})

	protected.POST("/files/upload", func(c *gin.Context) {
		// Limit upload size to 10MB
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)
		file, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file required (max 10MB)"})
			return
		}
		src, err := file.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to open uploaded file"})
			return
		}
		defer src.Close()
		data, err := io.ReadAll(io.LimitReader(src, 10<<20))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read file"})
			return
		}
		filename := filepath.Base(file.Filename)
		if filename == "." || filename == string(filepath.Separator) || filename == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
			return
		}
		targetPath := filepath.Join(c.PostForm("path"), filename)
		if err := dockerClient.WriteFile(c.Request.Context(), c.PostForm("container"), targetPath, string(data)); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "uploaded"})
	})

	protected.GET("/files/download", func(c *gin.Context) {
		content, err := dockerClient.ReadFile(c.Request.Context(), c.Query("container"), c.Query("path"))
		if err != nil {
			internalError(c, err)
			return
		}
		c.Header("Content-Disposition", `attachment; filename="`+sanitizeFilename(filepath.Base(c.Query("path")))+`"`)
		c.String(http.StatusOK, content)
	})

	protected.DELETE("/files", func(c *gin.Context) {
		if err := dockerClient.DeleteFile(c.Request.Context(), c.Query("container"), c.Query("path")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	})

	// Container file write (RESTful endpoint)
	protected.POST("/containers/:id/files/write", func(c *gin.Context) {
		containerID := c.Param("id")
		var req struct {
			Path    string `json:"path" binding:"required"`
			Content string `json:"content" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: path and content are required"})
			return
		}
		if err := dockerClient.WriteFile(c.Request.Context(), containerID, req.Path, req.Content); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "written"})
	})

	// System
	protected.GET("/system/info", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		// Get host info and stats
		hostInfo, err := client.GetHostInfo(c.Request.Context())
		if err != nil {
			internalError(c, err)
			return
		}

		hostStats, err := client.GetHostStats(c.Request.Context())
		if err != nil {
			internalError(c, err)
			return
		}

		info := SystemInfo{
			Hostname:      hostInfo.Hostname,
			OS:            hostInfo.OS,
			CPUCores:      hostInfo.CPUCores,
			CPUPercent:    hostStats.CPUPercent,
			TotalRAM:      hostStats.TotalRAM,
			UsedRAM:       hostStats.UsedRAM,
			TotalDisk:     hostStats.TotalDisk,
			UsedDisk:      hostStats.UsedDisk,
			DockerVersion: hostInfo.DockerVersion,
		}
		c.JSON(http.StatusOK, info)
	})

	// Version and update routes removed

	// Audit logs (requires admin authentication)
	adminGroup.GET("/audit-logs", handleListAuditLogs(database))

	// WebSocket stats streaming
	protected.GET("/containers/:id/stats/ws", func(c *gin.Context) {
		handleStatsStream(c, agentMgr)
	})

	// Domains (Fase 4)
	protected.GET("/domains", func(c *gin.Context) {
		domains, err := database.ListDomains()
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, domains)
	})

	protected.POST("/domains", func(c *gin.Context) {
		var req struct {
			Name    string `json:"name" binding:"required"`
			Service string `json:"service" binding:"required"`
			Port    int    `json:"port" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if err := validator.ValidateDomain(req.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid domain: %s", err.Error())})
			return
		}
		domain := db.Domain{
			ID:      generateID("dom"),
			Domain:  req.Name,
			Service: req.Service,
			Port:    req.Port,
		}
		database.SaveDomain(domain)
		c.JSON(http.StatusOK, domain)
	})

	protected.DELETE("/domains/:id", func(c *gin.Context) {
		database.DeleteDomain(c.Param("id"))
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	})

	// Cloudflare Tunnel
	cfTunnel := tunnel.NewCloudflareTunnel(dockerClient.RawClient())

	protected.POST("/tunnel", func(c *gin.Context) {
		var req struct {
			Token string `json:"token" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "token is required"})
			return
		}
		if err := tunnel.ValidateTunnelToken(req.Token); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := cfTunnel.Deploy(c.Request.Context(), req.Token); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deployed"})
	})

	protected.DELETE("/tunnel", func(c *gin.Context) {
		if err := cfTunnel.Remove(c.Request.Context()); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "removed"})
	})
}
