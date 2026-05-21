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
	"github.com/sdldev/dockpal/internal/registry"
	"github.com/sdldev/dockpal/internal/traefik"
	"github.com/sdldev/dockpal/internal/tunnel"
	"github.com/sdldev/dockpal/internal/update"
	"github.com/sdldev/dockpal/internal/validator"
)

func RegisterRoutes(r *gin.Engine, dockerClient *docker.Client, jwtSecret string, database *db.DB, versionService *update.VersionService, updateService *update.UpdateService, agentMgr *agent.Manager) {
	api := r.Group("/api")

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
    <link href="https://fonts.googleapis.com/css?family=Montserrat:300,400,700|Roboto:300,400,700" rel="stylesheet">
    <style>body { margin: 0; padding: 0; }</style>
  </head>
  <body>
    <redoc spec-url='/api/docs/swagger.json'></redoc>
    <script src="https://cdn.jsdelivr.net/npm/redoc@next/bundles/redoc.standalone.js"> </script>
  </body>
</html>`)
	})

	// Rate limiters
	loginRateLimiter := NewRateLimiter()
	mutationRateLimiter := NewRateLimiter()

	// Auth (unprotected)
	api.POST("/login", RateLimitMiddleware(loginRateLimiter), func(c *gin.Context) { auth.HandleLogin(c, jwtSecret, database) })

	// Webhooks public trigger
	api.POST("/webhooks/deploy/:webhook_id", HandleWebhookDeploy(database, agentMgr, jwtSecret))

	baseProtected := api.Group("")
	baseProtected.Use(AuthMiddleware(jwtSecret, database))

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
	instances := baseProtected.Group("/instances/:instance_id")
	instances.Use(InstanceMiddleware(agentMgr, database, jwtSecret))
	RegisterInstanceScopedRoutes(instances)

	// Registry credentials
	registryManager := registry.NewManager(database, jwtSecret)

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
	protected.GET("/containers/:id/logs", func(c *gin.Context) {
		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
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
			err := client.DeployComposeStreamed(context.Background(), req.Name, req.Compose, session, registryAuths)
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
	r.GET("/api/deploy/stream/:id", deployStreamWS)

	// Instance-scoped WebSocket endpoint for deploy log streaming.
	// Same logic as above but matches the instance-scoped URL pattern used by the frontend.
	r.GET("/api/instances/:instance_id/deploy/stream/:id", deployStreamWS)

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

		if err := client.DeployCompose(c.Request.Context(), req.Name, req.Compose, registryAuths); err != nil {
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to read compose file: %s", err.Error())})
			return
		}

		// Get auth headers for registries
		registryAuths := getRegistryAuths(registryManager, string(composeData))

		client, err := agentMgr.GetClient("local")
		if err != nil {
			internalError(c, err)
			return
		}

		if err := client.DeployCompose(c.Request.Context(), projectName, string(composeData), registryAuths); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("deploy failed: %s", err.Error())})
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
		if err := client.DeployCompose(c.Request.Context(), name, compose, registryAuths); err != nil {
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
			err := client.DeployComposeStreamed(context.Background(), name, compose, session, registryAuths)
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

	// Version (protected with auth)
	protected.GET("/system/version", func(c *gin.Context) {
		HandleGetVersion(c, versionService)
	})

	// Update (requires admin authentication)
	protected.POST("/system/update", func(c *gin.Context) {
		HandleUpdate(c, updateService, database)
	})

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
