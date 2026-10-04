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
	"github.com/sdldev/dockpal/internal/composecli"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/git"
	"github.com/sdldev/dockpal/internal/health"
	"github.com/sdldev/dockpal/internal/metrics"
	"github.com/sdldev/dockpal/internal/registry"
	"github.com/sdldev/dockpal/internal/traefik"
	"github.com/sdldev/dockpal/internal/tunnel"
	"github.com/sdldev/dockpal/internal/update"
	"github.com/sdldev/dockpal/internal/validator"
)

// routeDeps carries the shared wiring the register* helpers below each need a
// slice of; RegisterRoutes builds it once after the route groups exist.
// wireLocalRuntime fills in the registry manager and image update monitor, and
// registerDeployRoutes stores the shared deploy session manager.
type routeDeps struct {
	ctx          context.Context
	r            *gin.Engine
	dockerClient *docker.Client
	jwtSecret    string
	database     *db.DB
	agentMgr     *agent.Manager
	dataDir      string
	version      string

	api           *gin.RouterGroup
	baseProtected *gin.RouterGroup
	protected     *roleRouterWrapper
	viewerGroup   *gin.RouterGroup
	operatorGroup *gin.RouterGroup
	adminGroup    *gin.RouterGroup
	readLimit     gin.HandlerFunc

	registryManager    *registry.Manager
	imageUpdateMonitor *docker.ImageUpdateMonitor
	deployManager      *docker.DeployManager
}

func RegisterRoutes(ctx context.Context, r *gin.Engine, dockerClient *docker.Client, jwtSecret string, database *db.DB, agentMgr *agent.Manager, dataDir string, dbPath string, version string) {
	healthHandlers := health.NewHandlers(database, dataDir, dockerClient.RawClient(), "v"+version)
	healthHandlers.RegisterHealthRoutes(r)

	registerAPIVersionCompatibility(r)

	api := r.Group("/api")
	api.Use(legacyAPIWarningMiddleware())

	readRateLimiter := NewRateLimiterWithPolicy(ReadRateLimit)
	mutationRateLimiter := NewRateLimiterWithPolicy(MutationRateLimit)
	readLimit := RateLimitMiddleware(readRateLimiter)
	mutationLimit := RateLimitMiddleware(mutationRateLimiter)

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

	deps := &routeDeps{
		ctx:           ctx,
		r:             r,
		dockerClient:  dockerClient,
		jwtSecret:     jwtSecret,
		database:      database,
		agentMgr:      agentMgr,
		dataDir:       dataDir,
		version:       version,
		api:           api,
		baseProtected: baseProtected,
		protected:     protected,
		viewerGroup:   viewerGroup,
		operatorGroup: operatorGroup,
		adminGroup:    adminGroup,
		readLimit:     readLimit,
	}

	registerPublicRoutes(api, version)
	registerUnauthenticatedRoutes(api, jwtSecret, database, agentMgr)
	registerAccountRoutes(deps)
	registerSystemUpdateRoutes(deps)
	registerWebhookAndInstanceRoutes(deps)
	wireLocalRuntime(deps)
	registerAppUpdateRoutes(deps)
	registerRegistryRoutes(deps)
	registerContainerRoutes(deps)
	registerLegacyLogStreamRoutes(deps)
	registerDeployRoutes(deps)
	registerDeployStreamRoutes(deps)
	registerGitHubRepoRoutes(deps)
	registerServiceRoutes(deps)
	registerTemplateRoutes(deps)
	registerTemplateDeployRoutes(deps)
	registerImageRoutes(deps)
	registerFileManagerRoutes(deps)
	registerSystemRoutes(deps)
}

// registerPublicRoutes serves the API docs, the legacy v1 alias and the public boot config.
func registerPublicRoutes(api *gin.RouterGroup, version string) {
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

	// Public boot config for the UI; intentionally unauthenticated — flags only, never secrets.
	api.GET("/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"auto_update_enabled": globalAutoUpdateWorker.Enabled(),
			"current_version":     version,
		})
	})
}

// registerUnauthenticatedRoutes wires login, the public webhook deploy trigger and the
// audit-log hook that auth calls back into.
func registerUnauthenticatedRoutes(api *gin.RouterGroup, jwtSecret string, database *db.DB, agentMgr *agent.Manager) {
	loginRateLimiter := NewRateLimiterWithPolicy(LoginRateLimit)
	webhookRateLimiter := NewRateLimiterWithPolicy(WebhookRateLimit)

	api.POST("/login", RateLimitMiddleware(loginRateLimiter), func(c *gin.Context) { auth.HandleLogin(c, jwtSecret, database) })

	api.POST("/webhooks/deploy/:webhook_id", RateLimitMiddleware(webhookRateLimiter), HandleWebhookDeploy(database, agentMgr, jwtSecret))

	// Audit hook indirection avoids an auth → server import cycle.
	auth.AuditHook = func(c *gin.Context, action, resource, status, details string) {
		LogAudit(c, database, action, resource, status, details)
	}
}

// registerAccountRoutes covers the metrics scrape, self-service account actions and the
// admin-only user, API-key, backup and SSH-key management.
func registerAccountRoutes(deps *routeDeps) {
	jwtSecret, database, dataDir, baseProtected, viewerGroup, adminGroup := deps.jwtSecret, deps.database, deps.dataDir, deps.baseProtected, deps.viewerGroup, deps.adminGroup

	// Metrics requires a viewer: the series amount to a full workload inventory.
	viewerGroup.GET("/metrics", func(c *gin.Context) {
		metrics.Handler().ServeHTTP(c.Writer, c.Request)
	})

	// Self-service actions; baseProtected already rate-limits POST/PUT.
	baseProtected.POST("/logout", func(c *gin.Context) { auth.HandleLogout(c, database) })
	baseProtected.POST("/auth/reset-password", func(c *gin.Context) { auth.HandleResetPassword(c, database) })

	baseProtected.GET("/profile", func(c *gin.Context) { auth.HandleGetProfile(c, database) })
	baseProtected.PUT("/profile/password", func(c *gin.Context) { auth.HandleChangePassword(c, database) })

	// Single-use tickets keep 4h JWTs out of WS URL query strings.
	baseProtected.GET("/ws-ticket", handleWSTicket)

	adminGroup.GET("/users", func(c *gin.Context) { auth.HandleListUsers(c, database) })
	adminGroup.PUT("/users/:username/role", func(c *gin.Context) { auth.HandleUpdateUserRole(c, database) })
	adminGroup.GET("/api-keys", handleListAPIKeys(database))
	adminGroup.POST("/api-keys", handleCreateAPIKey(database))
	adminGroup.DELETE("/api-keys/:id", handleDeleteAPIKey(database))

	adminGroup.POST("/backup", HandleTriggerBackup(database, dataDir))

	adminGroup.GET("/ssh-keys", HandleListSSHKeys(database))
	adminGroup.POST("/ssh-keys", HandleCreateSSHKey(database, jwtSecret))
	adminGroup.DELETE("/ssh-keys/:id", HandleDeleteSSHKey(database))
}

// registerSystemUpdateRoutes wires the release checker/manager and their endpoints.
func registerSystemUpdateRoutes(deps *routeDeps) {
	ctx, database, dataDir, version, viewerGroup, adminGroup := deps.ctx, deps.database, deps.dataDir, deps.version, deps.viewerGroup, deps.adminGroup

	// The checker polls GitHub in the background; the manager turns an admin's request into
	// a trigger file for the privileged systemd updater. Reassigned per call for test isolation.
	updateChecker := update.NewChecker(database, "", "", update.EnvCheckInterval())
	updateChecker.Start(ctx)
	updateManager := update.NewManager(database, updateChecker, dataDir, version)
	updateManager.FinalizeOnBoot()
	globalUpdateChecker = updateChecker
	globalUpdateManager = updateManager

	// Status is viewer-readable (NavHeader badge); check/trigger are admin-only.
	viewerGroup.GET("/system/update/status", func(c *gin.Context) {
		status, err := updateManager.Status()
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, status)
	})
	adminGroup.POST("/system/update/check", func(c *gin.Context) {
		updateChecker.CheckNow(c.Request.Context())
		status, err := updateManager.Status()
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, status)
	})
	adminGroup.POST("/system/update", func(c *gin.Context) {
		var body struct {
			Version string `json:"version"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Version == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "version is required"})
			return
		}
		username := c.GetString("username")
		state, err := updateManager.RequestUpdate(body.Version, username)
		if err != nil {
			switch err {
			case update.ErrUpdateInFlight:
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			case update.ErrInvalidTarget:
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			default:
				c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			}
			return
		}
		LogAudit(c, database, "system.update", "system", "success", "update to "+state.Target+" requested")
		c.JSON(http.StatusAccepted, gin.H{"status": state.Status, "target": state.Target})
	})
}

// registerWebhookAndInstanceRoutes registers webhook management, the instance install
// routes, the agent WebSocket and the instance-scoped operations.
func registerWebhookAndInstanceRoutes(deps *routeDeps) {
	jwtSecret, database, agentMgr, api, baseProtected, protected, viewerGroup, operatorGroup, readLimit, r := deps.jwtSecret, deps.database, deps.agentMgr, deps.api, deps.baseProtected, deps.protected, deps.viewerGroup, deps.operatorGroup, deps.readLimit, deps.r

	protected.GET("/webhooks", HandleListWebhooks(database))
	protected.POST("/webhooks", HandleCreateWebhook(database))
	protected.DELETE("/webhooks/:webhook_id", HandleDeleteWebhook(database))

	logsManager := NewInstallLogsManager()
	RegisterInstanceRoutes(baseProtected, database, agentMgr, jwtSecret, logsManager)

	r.GET("/api/agent/connect", HandleAgentConnect(database, agentMgr))

	instanceBase := api.Group("/instances/:instance_id")
	instanceBase.Use(InstanceMiddleware(agentMgr, database, jwtSecret))
	instanceBase.GET("/containers/:id/logs", readLimit, handleInstanceContainerLogs)

	instances := baseProtected.Group("/instances/:instance_id")
	instances.Use(InstanceMiddleware(agentMgr, database, jwtSecret))
	RegisterInstanceScopedRoutes(instances)

	composecli.Register()
	registerStackRoutes(viewerGroup, operatorGroup, database)
}

// wireLocalRuntime builds the local-instance runtime: registry manager, image update
// monitor, the auto-update feed and worker, and the agent LocalClient app-ops wiring.
// It stores the manager and monitor on deps for the endpoint groups below and exposes
// the feed/worker/docker layers through the package globals.
func wireLocalRuntime(deps *routeDeps) {
	ctx, dockerClient, jwtSecret, database, agentMgr := deps.ctx, deps.dockerClient, deps.jwtSecret, deps.database, deps.agentMgr

	registryManager := registry.NewManager(database, jwtSecret)

	imageUpdateMonitor := docker.NewImageUpdateMonitor(dockerClient, func(imageRef string) (string, error) {
		return registryManager.GetAuthHeader(imageRef)
	})
	imageUpdateMonitor.Start()

	// Auto-update wiring: the feed broadcasts stage events to SSE subscribers; the worker
	// drives the per-app pull → recreate → verify → rollback pipeline.
	feed := NewAppUpdateFeed()

	// getCompose resolves a project's compose YAML from the services bucket; both the
	// instance-scoped ("local") and legacy ("") deploy paths are searched.
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
		// Fallback: read the compose via the dockpal.compose label for CLI-deployed apps.
		containers, lErr := dockerClient.ListContainersWithLabel(context.Background(), "dockpal.project="+project)
		if lErr == nil && len(containers) > 0 {
			composePath := containers[0].Labels["dockpal.compose"]
			if composePath != "" {
				content, fsErr := os.ReadFile(composePath)
				if fsErr == nil && len(content) > 0 {
					// Persist to DB so subsequent calls hit the fast path.
					newSvc := db.Service{
						ID:         generateID("svc-"),
						InstanceID: "local",
						Name:       project,
						Type:       "compose",
						Compose:    string(content),
						CreatedAt:  time.Now().Unix(),
					}
					_ = database.SaveService(newSvc) // best-effort
					return string(content), nil
				}
			}
		}
		return "", fmt.Errorf("compose not found for project %q", project)
	}

	// feedAdapter avoids a server → docker import cycle.
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
	// Prometheus hooks keep the worker decoupled from internal/metrics.
	worker.SetMetricsHooks(docker.AutoUpdateMetricsHooks{
		Attempt:       metrics.AutoUpdateAttempt,
		Duration:      metrics.AutoUpdateDuration,
		PendingUpdate: metrics.SetAppsPendingUpdate,
	})
	worker.SetWebhookLister(database.ListNotificationWebhooks)
	worker.Start(ctx)

	// Globals are reassigned per RegisterRoutes call so tests stay isolated.
	globalAppUpdateFeed = feed
	globalAutoUpdateWorker = worker
	// Globals let instance-scoped handlers reuse the local docker layer for instance "local".
	globalDockerClient = dockerClient
	globalImageUpdateMonitor = imageUpdateMonitor
	globalRegistryManager = registryManager

	// Wire the agent LocalClient app-ops; the closure mirrors PATCH /apps/:name/auto-update
	// and keeps *db.DB out of the agent package.
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
		// Persist before redeploying so a failure cannot desync DB and containers.
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
	deps.registryManager = registryManager
	deps.imageUpdateMonitor = imageUpdateMonitor
}

// registerAppUpdateRoutes serves the app list, update history, manual update trigger,
// auto-update toggle and the SSE feed stream.
func registerAppUpdateRoutes(deps *routeDeps) {
	dockerClient, database, agentMgr, protected, registryManager, imageUpdateMonitor := deps.dockerClient, deps.database, deps.agentMgr, deps.protected, deps.registryManager, deps.imageUpdateMonitor

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
		// Lists local apps via *docker.Client (not the AgentClient interface); a non-local
		// instance_id filter yields an empty slice.
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
		// Defensive cross-check: the attempt must belong to the URL's app.
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

		username := "user"
		if v, ok := c.Get("username"); ok {
			if s, ok := v.(string); ok && s != "" {
				username = s
			}
		}
		triggeredBy := "user:" + username

		// The wrapping closure is load-bearing: c.Writer.Status() is only set after c.JSON,
		// so the audit result must be read inside the deferred body.
		defer func() {
			LogAppUpdateAttempt(c, database, dockerClient, name, auditAppUpdateResultFor(c.Writer.Status()))
		}()

		// Snapshot the latest attempt to detect the new record; empty means no prior attempts.
		var prevAttempt string
		if recs, err := database.ListAppUpdates(name, 1); err == nil && len(recs) > 0 {
			prevAttempt = recs[0].AttemptID
		}

		// Asynchronous trigger; a concurrent run maps to HTTP 409 via ErrUpdateAlreadyRunning.
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
				if recs, lerr := database.ListAppUpdates(name, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
					c.JSON(http.StatusAccepted, gin.H{"attempt_id": recs[0].AttemptID})
					return
				}
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

		// Match by service name; InstanceID "" or "local" (two deploy paths).
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

		labelValue := "true"
		if !req.Enabled {
			labelValue = "" // empty string removes the label
		}
		newCompose, err := docker.SetServiceLabel(svc.Compose, "dockpal.auto-update", labelValue)
		if err != nil {
			internalError(c, err)
			return
		}

		// Persist before redeploying so a failure cannot desync DB and containers.
		updated := *svc
		updated.Compose = newCompose
		if err := database.SaveService(updated); err != nil {
			internalError(c, err)
			return
		}

		// forcePull=false: only the label changes; the existing image is reused.
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

		// X-Accel-Buffering=no keeps reverse proxies from buffering the stream.
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		// Without this the server-wide WriteTimeout would cut SSE off 60s in.
		clearWriteDeadline(c)

		ch, unsubscribe := globalAppUpdateFeed.Subscribe()
		defer unsubscribe()

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
}

// registerRegistryRoutes serves the registry credential CRUD and connection test.
func registerRegistryRoutes(deps *routeDeps) {
	protected, registryManager := deps.protected, deps.registryManager

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
}

// registerContainerRoutes serves the local-instance container CRUD and edit endpoints.
func registerContainerRoutes(deps *routeDeps) {
	database, agentMgr, protected := deps.database, deps.agentMgr, deps.protected

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
		LogAudit(c, database, "container.start", "containers/"+c.Param("id"), "success", "")
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
		LogAudit(c, database, "container.stop", "containers/"+c.Param("id"), "success", "")
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
		LogAudit(c, database, "container.restart", "containers/"+c.Param("id"), "success", "")
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
		LogAudit(c, database, "container.remove", "containers/"+containerID, "success", fmt.Sprintf("force=%v", force))
		c.JSON(http.StatusOK, gin.H{"status": "removed"})
	})

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

		if req.Name != nil {
			if err := validator.ValidateContainerName(*req.Name); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid name: %s", err.Error())})
				return
			}
		}

		if req.RestartPolicy != nil {
			if err := validator.ValidateRestartPolicy(*req.RestartPolicy); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}

		if req.MemoryLimit != nil && *req.MemoryLimit < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "memory limit must be non-negative"})
			return
		}

		if req.CPULimit != nil && *req.CPULimit < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CPU limit must be non-negative"})
			return
		}

		if req.Env != nil {
			for _, env := range *req.Env {
				if err := validator.ValidateEnvVarValue(env); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid env var: %s", err.Error())})
					return
				}
			}
		}

		if req.Ports != nil {
			for _, pm := range *req.Ports {
				if err := validator.ValidatePortMapping(pm.HostPort, pm.ContainerPort, pm.Protocol); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}
			}
		}

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
		LogAudit(c, database, "container.edit", "containers/"+containerID, "success", fmt.Sprintf("recreated=%v", needsRecreate))

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
}

// registerLegacyLogStreamRoutes serves the legacy /api/containers/:id/logs stream.
func registerLegacyLogStreamRoutes(deps *routeDeps) {
	jwtSecret, database, agentMgr, api, readLimit := deps.jwtSecret, deps.database, deps.agentMgr, deps.api, deps.readLimit

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
		defer conn.Close()

		// Auth: query token (browser WS) or first {token} message (API clients); the query
		// value may be a single-use ws-ticket or a raw JWT.
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

		reader, err := client.ContainerLogs(c.Request.Context(), c.Param("id"), c.DefaultQuery("tail", "100"))
		if err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte("Error: failed to retrieve container logs"))
			return
		}

		streamContainerLogs(conn, reader)
	})
}

// registerDeployRoutes serves the streamed compose deploy and stores the shared
// session manager on deps for the deploy stream and template deploy below.
func registerDeployRoutes(deps *routeDeps) {
	database, agentMgr, protected, registryManager := deps.database, deps.agentMgr, deps.protected, deps.registryManager

	deployManager := globalDeployManager

	protected.POST("/deploy/stream", func(c *gin.Context) {
		var req struct {
			Name          string `json:"name" binding:"required"`
			Domain        string `json:"domain"`
			Compose       string `json:"compose" binding:"required"`
			RestartPolicy string `json:"restart_policy"`
			AutoStart     *bool  `json:"auto_start"`
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

		req.Compose = ensureAutoStart(req.Compose, req.RestartPolicy, req.AutoStart)

		registryAuths := getRegistryAuths(registryManager, req.Compose)

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		session := deployManager.CreateSession()

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
					if cfgErr := traefik.GenerateConfig(req.Domain, req.Name, port); cfgErr != nil {
						// The deploy itself already succeeded; a failed Traefik
						// config must at least be visible in the logs, or the
						// domain silently stays unrouted.
						log.Printf("deploy %s: traefik config for %s: %v", req.Name, req.Domain, cfgErr)
					}
				}
			}
			time.AfterFunc(30*time.Second, func() {
				deployManager.RemoveSession(session.ID)
			})
		}()

		c.JSON(http.StatusOK, gin.H{"deploy_id": session.ID})
	})
	deps.deployManager = deployManager
}

// registerDeployStreamRoutes serves the deploy-session WebSocket stream.
func registerDeployStreamRoutes(deps *routeDeps) {
	jwtSecret, database, agentMgr, baseProtected, protected, registryManager, deployManager := deps.jwtSecret, deps.database, deps.agentMgr, deps.baseProtected, deps.protected, deps.registryManager, deps.deployManager

	// Registered under baseProtected: AuthMiddleware + rate limiting apply; the handler's
	// own query-token auth is defense in depth. Read-only, viewer role.
	deployStreamWS := handleDeployStreamWS(jwtSecret, database, deployManager)
	baseProtected.GET("/deploy/stream/:id", legacyAPIWarningMiddleware(), RequireRole(auth.RoleViewer), deployStreamWS)

	baseProtected.GET("/instances/:instance_id/deploy/stream/:id", legacyAPIWarningMiddleware(), RequireRole(auth.RoleViewer), deployStreamWS)

	protected.POST("/deploy/compose", func(c *gin.Context) {
		var req struct {
			Name          string `json:"name" binding:"required"`
			Domain        string `json:"domain"`
			Compose       string `json:"compose" binding:"required"`
			RestartPolicy string `json:"restart_policy"`
			AutoStart     *bool  `json:"auto_start"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		if err := validator.ValidateContainerName(req.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid name: %s", err.Error())})
			return
		}

		req.Compose = ensureAutoStart(req.Compose, req.RestartPolicy, req.AutoStart)

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

		if len(info.ComposeFiles) > 1 && req.ComposeFile == "" {
			c.JSON(http.StatusOK, gin.H{"status": "select_compose", "compose_files": info.ComposeFiles, "info": info})
			return
		}

		selectedFile := req.ComposeFile
		if selectedFile == "" {
			selectedFile = info.ComposeFiles[0]
		}

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
		composeYAML := ensureAutoStart(string(composeData), "", nil)

		registryAuths := getRegistryAuths(registryManager, composeYAML)

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		if err := client.DeployCompose(c.Request.Context(), projectName, composeYAML, registryAuths, false); err != nil {
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
}

// registerGitHubRepoRoutes serves the stored-credential GitHub repository listing.
func registerGitHubRepoRoutes(deps *routeDeps) {
	protected, registryManager := deps.protected, deps.registryManager

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
}

// registerServiceRoutes serves the deployed-services list and deletion.
func registerServiceRoutes(deps *routeDeps) {
	dockerClient, database, protected := deps.dockerClient, deps.database, deps.protected

	protected.GET("/services", func(c *gin.Context) {
		services, err := database.ListServices()
		if err != nil {
			internalError(c, err)
			return
		}
		// Compose bodies embed secrets — viewers get metadata only; the list view doesn't use it.
		if !auth.HasRole(c.GetString("role"), auth.RoleOperator) {
			redacted := make([]db.Service, len(services))
			copy(redacted, services)
			for i := range redacted {
				redacted[i].Compose = ""
			}
			c.JSON(http.StatusOK, redacted)
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

		if svc.Domain != "" {
			if err := traefik.RemoveDomain(svc.Name); err != nil {
				log.Printf("Warning: failed to remove traefik config for %s: %v", svc.Name, err)
			}
		}

		database.DeleteService(c.Param("id"))
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	})

}

// registerTemplateRoutes serves the deploy template catalog and single deploy.
func registerTemplateRoutes(deps *routeDeps) {
	database, agentMgr, protected, registryManager := deps.database, deps.agentMgr, deps.protected, deps.registryManager

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
		compose = ensureAutoStart(compose, "", nil)

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

}

// registerTemplateDeployRoutes serves the streamed template deploy.
func registerTemplateDeployRoutes(deps *routeDeps) {
	database, agentMgr, protected, registryManager, deployManager := deps.database, deps.agentMgr, deps.protected, deps.registryManager, deps.deployManager

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
			AutoStart     *bool             `json:"auto_start"`
			AutoRecover   bool              `json:"auto_recover"`
			Domain        string            `json:"domain"`
			NetworkMode   string            `json:"network_mode"`
			CustomNetwork string            `json:"custom_network"`
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
		for _, p := range tpl.Ports {
			hostPort := p.Default
			if customPort, ok := req.Ports[fmt.Sprintf("%d", p.ContainerPort)]; ok && customPort > 0 {
				if err := validator.ValidatePort(customPort); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid host port for %s: %s", p.Label, err.Error())})
					return
				}
				hostPort = customPort
			}
			oldPort := fmt.Sprintf("'%d:%d'", p.Default, p.ContainerPort)
			newPort := fmt.Sprintf("'%d:%d'", hostPort, p.ContainerPort)
			compose = strings.ReplaceAll(compose, oldPort, newPort)
		}
		compose = applyNetworkMode(compose, req.NetworkMode, req.CustomNetwork)
		// Normalize restart policy so apps come back up after a host reboot.
		compose = ensureAutoStart(compose, req.RestartPolicy, req.AutoStart)
		if req.AutoRecover {
			compose = strings.ReplaceAll(compose, "image: ", "labels:\n      dockpal.auto-recover: \"true\"\n    image: ")
		}

		name := tpl.ID + "-" + fmt.Sprintf("%d", time.Now().Unix())
		if req.CustomName != "" {
			if err := validator.ValidateContainerName(req.CustomName); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid app name: %s", err.Error())})
				return
			}
			name = req.CustomName
		}

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
}

// registerImageRoutes serves image listing, inspection, update status, pull and prune.
func registerImageRoutes(deps *routeDeps) {
	agentMgr, protected, registryManager, imageUpdateMonitor := deps.agentMgr, deps.protected, deps.registryManager, deps.imageUpdateMonitor

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
		authHeader, _ := registryManager.GetAuthHeader(req.Image)
		if authHeader != "" {
			if err := client.PullImageWithAuth(c.Request.Context(), req.Image, authHeader); err != nil {
				internalError(c, err)
				return
			}
		} else {
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
}

// registerFileManagerRoutes serves the operator-gated container file browser and writes.
func registerFileManagerRoutes(deps *routeDeps) {
	dockerClient, protected, operatorGroup := deps.dockerClient, deps.protected, deps.operatorGroup

	// File reads run docker exec cat/ls — the same capability class as the exec route,
	// so a viewer must not reach them.
	operatorGroup.GET("/files", func(c *gin.Context) {
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

	operatorGroup.GET("/files/read", func(c *gin.Context) {
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

	operatorGroup.GET("/files/download", func(c *gin.Context) {
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
}

// registerSystemRoutes serves host info, audit logs, stats streaming, domains and the
// Cloudflare tunnel.
func registerSystemRoutes(deps *routeDeps) {
	dockerClient, database, agentMgr, protected, adminGroup := deps.dockerClient, deps.database, deps.agentMgr, deps.protected, deps.adminGroup

	protected.GET("/system/info", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

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

	// Audit logs (requires admin authentication)
	adminGroup.GET("/audit-logs", handleListAuditLogs(database))

	protected.GET("/containers/:id/stats/ws", func(c *gin.Context) {
		handleStatsStream(c, agentMgr)
	})

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
		if err := database.SaveDomain(domain); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, domain)
	})

	protected.DELETE("/domains/:id", func(c *gin.Context) {
		if err := database.DeleteDomain(c.Param("id")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	})

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
