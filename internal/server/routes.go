package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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

// checkOrigin validates WebSocket upgrade requests by comparing
// the Origin header's host against the request's Host header.
// Rejects empty/missing origins and mismatched hosts.
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}

	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}

	return u.Host == r.Host
}

var upgrader = websocket.Upgrader{
	CheckOrigin: checkOrigin,
}

type TemplatePort struct {
	Label         string `json:"label"`
	Default       int    `json:"default"`
	ContainerPort int    `json:"container_port"`
}

type Template struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	Icon        string         `json:"icon"`
	EnvRequired []string       `json:"env_required,omitempty"`
	Ports       []TemplatePort `json:"ports,omitempty"`
	Compose     string         `json:"compose"`
}

func loadTemplates() ([]Template, error) {
	// Try local templates directory first
	templates, err := loadTemplatesFromDir("templates")
	if err == nil && len(templates) > 0 {
		return templates, nil
	}

	// Fallback to system-wide directory
	templates, err = loadTemplatesFromDir("/opt/dockpal/templates")
	if err != nil {
		return nil, fmt.Errorf("no templates available: %w", err)
	}
	if len(templates) == 0 {
		return nil, fmt.Errorf("no template files found in fallback directory")
	}

	return templates, nil
}

// loadTemplatesFromDir reads all .json files in the given directory,
// unmarshals each into a Template, and returns the collected slice.
func loadTemplatesFromDir(dir string) ([]Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading templates directory %s: %w", dir, err)
	}

	var templates []Template

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading template file %s: %w", filePath, err)
		}

		var tmpl Template
		if err := json.Unmarshal(data, &tmpl); err != nil {
			return nil, fmt.Errorf("parsing template file %s: %w", filePath, err)
		}

		templates = append(templates, tmpl)
	}

	return templates, nil
}

func RegisterRoutes(r *gin.Engine, dockerClient *docker.Client, jwtSecret string, database *db.DB, versionService *update.VersionService, updateService *update.UpdateService) {
	api := r.Group("/api")

	// Rate limiters
	loginRateLimiter := NewRateLimiter()
	mutationRateLimiter := NewRateLimiter()

	// Auth (unprotected)
	api.POST("/login", RateLimitMiddleware(loginRateLimiter), func(c *gin.Context) { auth.HandleLogin(c, jwtSecret, database) })
	api.POST("/logout", func(c *gin.Context) { auth.HandleLogout(c, database) })

	protected := api.Group("")
	protected.Use(AuthMiddleware(jwtSecret, database))

	// Auth protected
	protected.POST("/auth/reset-password", RateLimitMiddleware(mutationRateLimiter), func(c *gin.Context) { auth.HandleResetPassword(c, database) })

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
			if err.Error() == "credential not found" {
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
			if err.Error() == "credential not found" {
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
			if err.Error() == "credential not found" {
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
			if err.Error() == "credential not found" {
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
		containers, err := dockerClient.ListContainers(c.Request.Context(), true)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, containers)
	})

	protected.GET("/containers/:id", func(c *gin.Context) {
		detail, err := dockerClient.InspectContainer(c.Request.Context(), c.Param("id"))
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, detail)
	})

	protected.POST("/containers/:id/start", func(c *gin.Context) {
		if err := dockerClient.StartContainer(c.Request.Context(), c.Param("id")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "started"})
	})

	protected.POST("/containers/:id/stop", func(c *gin.Context) {
		if err := dockerClient.StopContainer(c.Request.Context(), c.Param("id")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "stopped"})
	})

	protected.POST("/containers/:id/restart", func(c *gin.Context) {
		if err := dockerClient.RestartContainer(c.Request.Context(), c.Param("id")); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "restarted"})
	})

	protected.DELETE("/containers/:id", func(c *gin.Context) {
		force := c.Query("force") == "true"
		if err := dockerClient.RemoveContainer(c.Request.Context(), c.Param("id"), force); err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "removed"})
	})

	protected.GET("/containers/:id/stats", func(c *gin.Context) {
		stats, err := dockerClient.GetContainerStats(c.Request.Context(), c.Param("id"))
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, stats)
	})

	// WebSocket logs
	protected.GET("/containers/:id/logs", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		reader, err := dockerClient.ContainerLogs(c.Request.Context(), c.Param("id"), "100")
		if err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte("Error: failed to retrieve container logs"))
			return
		}
		defer reader.Close()

		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				conn.WriteMessage(websocket.TextMessage, buf[:n])
			}
			if err != nil {
				break
			}
		}
	})

	// Deploy
	deployManager := docker.NewDeployManager()

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

		session := deployManager.CreateSession()

		// Run deploy in background goroutine
		go func() {
			err := dockerClient.DeployComposeStreamed(context.Background(), req.Name, req.Compose, session, registryManager.GetAuthHeader)
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
	r.GET("/api/deploy/stream/:id", func(c *gin.Context) {
		// Auth via query param for WebSocket
		token := c.Query("token")
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := auth.ValidateJWTWithVersionCheck(token, jwtSecret, database)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		_ = claims
		// Verify origin matches to prevent CSRF via WebSocket
		origin := c.GetHeader("Origin")
		if origin != "" {
			u, parseErr := url.Parse(origin)
			if parseErr != nil || (u.Host != c.Request.Host && !strings.HasPrefix(u.Host, "localhost:")) {
				c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
				return
			}
		}

		session := deployManager.GetSession(c.Param("id"))
		if session == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "deploy session not found"})
			return
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			select {
			case event, ok := <-session.Events:
				if !ok {
					return
				}
				if err := conn.WriteJSON(event); err != nil {
					return
				}
			case <-session.Done:
				// Drain remaining events
				for {
					select {
					case event := <-session.Events:
						conn.WriteJSON(event)
					default:
						return
					}
				}
			}
		}
	})

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

		if err := dockerClient.DeployCompose(c.Request.Context(), req.Name, req.Compose, registryManager.GetAuthHeader); err != nil {
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

		composePath := filepath.Join(info.Path, selectedFile)
		composeData, err := os.ReadFile(composePath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to read compose file: %s", err.Error())})
			return
		}
		if err := dockerClient.DeployCompose(c.Request.Context(), projectName, string(composeData), registryManager.GetAuthHeader); err != nil {
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

		page := c.DefaultQuery("page", "1")
		perPage := c.DefaultQuery("per_page", "30")

		apiURL := fmt.Sprintf("https://api.github.com/user/repos?sort=updated&direction=desc&page=%s&per_page=%s&type=all", page, perPage)
		req, err := http.NewRequestWithContext(c.Request.Context(), "GET", apiURL, nil)
		if err != nil {
			internalError(c, err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github.v3+json")

		resp, err := http.DefaultClient.Do(req)
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
		templates, err := loadTemplates()
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, templates)
	})

	protected.GET("/templates/:id", func(c *gin.Context) {
		templates, err := loadTemplates()
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
		templates, err := loadTemplates()
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

		name := tpl.ID + "-" + fmt.Sprintf("%d", time.Now().Unix())
		if err := dockerClient.DeployCompose(c.Request.Context(), name, compose, registryManager.GetAuthHeader); err != nil {
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
		templates, err := loadTemplates()
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
		session := deployManager.CreateSession()

		go func() {
			err := dockerClient.DeployComposeStreamed(context.Background(), name, compose, session, registryManager.GetAuthHeader)
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
		images, err := dockerClient.ListImages(c.Request.Context())
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, images)
	})

	protected.POST("/images/pull", func(c *gin.Context) {
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
			if err := dockerClient.PullImageWithAuth(c.Request.Context(), req.Image, authHeader); err != nil {
				internalError(c, err)
				return
			}
		} else {
			// Fallback to unauthenticated pull
			if err := dockerClient.PullImage(c.Request.Context(), req.Image); err != nil {
				internalError(c, err)
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "pulled"})
	})

	protected.DELETE("/images/:id", func(c *gin.Context) {
		if err := dockerClient.RemoveImage(c.Request.Context(), c.Param("id")); err != nil {
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
		src, _ := file.Open()
		defer src.Close()
		data, err := io.ReadAll(io.LimitReader(src, 10<<20))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read file"})
			return
		}
		if err := dockerClient.WriteFile(c.Request.Context(), c.PostForm("container"), c.PostForm("path")+"/"+file.Filename, string(data)); err != nil {
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
		info := getSystemInfo(dockerClient)
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

	// WebSocket stats streaming
	protected.GET("/containers/:id/stats/ws", func(c *gin.Context) {
		handleStatsStream(c, dockerClient)
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

// SystemInfo contains host hardware metrics and Docker version information.
type SystemInfo struct {
	Hostname      string  `json:"hostname"`
	OS            string  `json:"os"`
	CPUCores      int     `json:"cpu_cores"`
	CPUPercent    float64 `json:"cpu_percent"`
	TotalRAM      uint64  `json:"total_ram"`
	UsedRAM       uint64  `json:"used_ram"`
	TotalDisk     uint64  `json:"total_disk"`
	UsedDisk      uint64  `json:"used_disk"`
	DockerVersion string  `json:"docker_version"`
}

// getSystemInfo gathers host metrics (CPU, RAM, disk) and the Docker daemon version.
func getSystemInfo(dockerClient *docker.Client) SystemInfo {
	hostname, _ := os.Hostname()

	var sysinfo syscall.Sysinfo_t
	syscall.Sysinfo(&sysinfo)

	var stat syscall.Statfs_t
	syscall.Statfs("/", &stat)

	totalDisk := stat.Blocks * uint64(stat.Bsize)
	freeDisk := stat.Bfree * uint64(stat.Bsize)

	dockerVersion := ""
	if ver, err := dockerClient.ServerVersion(context.Background()); err == nil {
		dockerVersion = ver
	}

	return SystemInfo{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		CPUCores:      runtime.NumCPU(),
		CPUPercent:    getCPUPercent(),
		TotalRAM:      uint64(sysinfo.Totalram),
		UsedRAM:       uint64(sysinfo.Totalram) - uint64(sysinfo.Freeram),
		TotalDisk:     totalDisk,
		UsedDisk:      totalDisk - freeDisk,
		DockerVersion: dockerVersion,
	}
}

// getCPUPercent reads /proc/stat twice with a 200ms interval to compute CPU usage.
func getCPUPercent() float64 {
	read := func() (idle, total uint64) {
		data, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, 0
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) == 0 {
			return 0, 0
		}
		fields := strings.Fields(lines[0])
		if len(fields) < 5 {
			return 0, 0
		}
		var sum uint64
		for i := 1; i < len(fields); i++ {
			val, _ := strconv.ParseUint(fields[i], 10, 64)
			sum += val
			if i == 4 {
				idle = val
			}
		}
		return idle, sum
	}

	idle1, total1 := read()
	time.Sleep(200 * time.Millisecond)
	idle2, total2 := read()

	totalDelta := float64(total2 - total1)
	if totalDelta == 0 {
		return 0
	}
	idleDelta := float64(idle2 - idle1)
	return (1.0 - idleDelta/totalDelta) * 100.0
}

func getHostname() string {
	h, _ := os.Hostname()
	return h
}

// handleStatsStream streams real-time container resource stats over WebSocket.
// It sends JSON stats every 2 seconds and stops on client disconnect or error.
func handleStatsStream(c *gin.Context, dockerClient *docker.Client) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	containerID := c.Param("id")
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// Check if container is running before starting the stream
	detail, err := dockerClient.InspectContainer(ctx, containerID)
	if err != nil {
		conn.WriteJSON(gin.H{"error": "container not found"})
		return
	}
	if detail.State != "running" {
		conn.WriteJSON(gin.H{"error": "container is not running"})
		return
	}

	// Monitor for client disconnect by reading messages in a goroutine
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Send initial stats immediately
	stats, err := dockerClient.GetContainerStats(ctx, containerID)
	if err != nil {
		log.Printf("[ERROR] stats stream: %v", err)
		conn.WriteJSON(gin.H{"error": "failed to get container stats"})
		return
	}
	if err := conn.WriteJSON(stats); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats, err := dockerClient.GetContainerStats(ctx, containerID)
			if err != nil {
				log.Printf("[ERROR] stats stream: %v", err)
				conn.WriteJSON(gin.H{"error": "failed to get container stats"})
				return
			}
			if err := conn.WriteJSON(stats); err != nil {
				return
			}
		}
	}
}

// sanitizeFilename removes CR/LF and quotes from a filename to prevent
// Content-Disposition header injection.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, `"`, "'")
	return name
}

// generateID creates a prefixed, unpredictable ID using crypto/rand.
func generateID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback should never happen
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%x", prefix, b)
}

// internalError returns a generic error message to the client while logging
// the real error. This prevents leaking internal details (file paths, DB
// errors, etc.) in API responses.
func internalError(c *gin.Context, err error) {
	log.Printf("[ERROR] %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

// extractFirstPort parses the compose YAML and extracts the first container port
// from the first service that has port bindings. Returns 80 as default if no ports found.
func extractFirstPort(composeYAML string) int {
	cf, err := docker.ParseComposeFile(composeYAML)
	if err != nil {
		return 80
	}
	for _, svc := range cf.Services {
		for _, portSpec := range svc.Ports {
			pb, err := docker.ParsePort(portSpec)
			if err == nil {
				return pb.ContainerPort
			}
		}
	}
	return 80
}
// HandleGetVersion handles the GET /api/system/version endpoint
func HandleGetVersion(c *gin.Context, versionService *update.VersionService) {
	if versionService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "version service not configured"})
		return
	}

	versionInfo, err := versionService.GetVersionInfo(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}

	c.JSON(http.StatusOK, versionInfo)
}
// HandleUpdate handles the POST /api/system/update endpoint
// It performs a streaming update with progress notifications
func HandleUpdate(c *gin.Context, updateService *update.UpdateService, database *db.DB) {
	if updateService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update service not configured"})
		return
	}

	// Get username from context (set by auth middleware)
	username, exists := c.Get("username")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Check if user is admin
	user, err := database.GetUser(username.(string))
	if err != nil || user.Username != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin access required"})
		return
	}

	// Parse request body
	var req struct {
		DownloadURL string `json:"downloadUrl" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "downloadUrl is required"})
		return
	}

	// Set up Server-Sent Events for streaming progress
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Helper to send progress updates
	sendProgress := func(status, message string, percentage int) {
		progress := update.UpdateProgress{
			Status:     status,
			Message:    message,
			Percentage: percentage,
		}
		data, _ := json.Marshal(progress)
		c.Writer.Write([]byte("data: "))
		c.Writer.Write(data)
		c.Writer.Write([]byte("\n\n"))
		c.Writer.Flush()
	}

	// Send initial progress
	sendProgress(update.StatusDownloading, "Starting download...", 0)

	// Check sudo access first
	hasSudo, err := updateService.CheckSudoAccess()
	if err != nil {
		sendProgress(update.StatusError, "Failed to check sudo access: " + err.Error(), 0)
		return
	}
	if !hasSudo {
		sendProgress(update.StatusError, "Update requires root privileges", 0)
		return
	}

	// Download the update
	downloadCtx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	downloadedPath, err := updateService.DownloadUpdate(downloadCtx, req.DownloadURL)
	if err != nil {
		sendProgress(update.StatusError, "Failed to download update: " + err.Error(), 0)
		return
	}
	defer os.Remove(downloadedPath)

	sendProgress(update.StatusInstalling, "Download complete, verifying binary...", 50)

	// Verify the binary
	if err := updateService.VerifyBinary(downloadedPath); err != nil {
		sendProgress(update.StatusError, "Binary verification failed: " + err.Error(), 0)
		return
	}

	sendProgress(update.StatusInstalling, "Binary verified, installing...", 70)

	// Install the binary
	installCtx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	if err := updateService.InstallBinary(installCtx, downloadedPath); err != nil {
		sendProgress(update.StatusError, "Failed to install binary: " + err.Error(), 0)
		return
	}

	sendProgress(update.StatusRestarting, "Service restarted successfully", 90)

	// Final success message
	time.Sleep(500 * time.Millisecond)
	sendProgress(update.StatusComplete, "Update completed successfully", 100)
}