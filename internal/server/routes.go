package server

import (
	"context"
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
	data, err := os.ReadFile("templates/templates.json")
	if err != nil {
		data, err = os.ReadFile("/opt/dockpal/templates.json")
		if err != nil {
			return nil, err
		}
	}
	var templates []Template
	if err := json.Unmarshal(data, &templates); err != nil {
		return nil, err
	}
	return templates, nil
}

func RegisterRoutes(r *gin.Engine, dockerClient *docker.Client, jwtSecret string, database *db.DB, versionService *update.VersionService, updateService *update.UpdateService) {
	api := r.Group("/api")

	// Rate limiter for login endpoint
	loginRateLimiter := NewRateLimiter()

	// Auth (unprotected)
	api.POST("/login", RateLimitMiddleware(loginRateLimiter), func(c *gin.Context) { auth.HandleLogin(c, jwtSecret, database) })
	api.POST("/logout", func(c *gin.Context) { auth.HandleLogout(c) })

	protected := api.Group("")
	protected.Use(AuthMiddleware(jwtSecret, database))

	// Auth protected
	protected.POST("/auth/reset-password", func(c *gin.Context) { auth.HandleResetPassword(c, database) })

	// Containers
	protected.GET("/containers", func(c *gin.Context) {
		containers, err := dockerClient.ListContainers(c.Request.Context(), true)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, containers)
	})

	protected.GET("/containers/:id", func(c *gin.Context) {
		detail, err := dockerClient.InspectContainer(c.Request.Context(), c.Param("id"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, detail)
	})

	protected.POST("/containers/:id/start", func(c *gin.Context) {
		if err := dockerClient.StartContainer(c.Request.Context(), c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "started"})
	})

	protected.POST("/containers/:id/stop", func(c *gin.Context) {
		if err := dockerClient.StopContainer(c.Request.Context(), c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "stopped"})
	})

	protected.POST("/containers/:id/restart", func(c *gin.Context) {
		if err := dockerClient.RestartContainer(c.Request.Context(), c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "restarted"})
	})

	protected.DELETE("/containers/:id", func(c *gin.Context) {
		force := c.Query("force") == "true"
		if err := dockerClient.RemoveContainer(c.Request.Context(), c.Param("id"), force); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "removed"})
	})

	protected.GET("/containers/:id/stats", func(c *gin.Context) {
		stats, err := dockerClient.GetContainerStats(c.Request.Context(), c.Param("id"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			conn.WriteMessage(websocket.TextMessage, []byte("Error: "+err.Error()))
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

		session := deployManager.CreateSession()

		// Run deploy in background goroutine
		go func() {
			err := dockerClient.DeployComposeStreamed(context.Background(), req.Name, req.Compose, session)
			if err == nil {
				database.SaveService(db.Service{
					ID:        fmt.Sprintf("svc-%d", time.Now().UnixNano()),
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
	r.GET("/api/deploy/stream/:id", func(c *gin.Context) {
		// Auth via query param for WebSocket
		token := c.Query("token")
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		if _, err := auth.ValidateJWTWithVersionCheck(token, jwtSecret, database); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
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

		if err := dockerClient.DeployCompose(c.Request.Context(), req.Name, req.Compose); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		database.SaveService(db.Service{
			ID:        fmt.Sprintf("svc-%d", time.Now().UnixNano()),
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
			Repo   string `json:"repo" binding:"required"`
			Branch string `json:"branch"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		if err := validator.ValidateGitURL(req.Repo); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid repo: %s", err.Error())})
			return
		}

		info, err := git.Clone(req.Repo, req.Branch)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if info.ComposeFile != "" {
			composeData, err := os.ReadFile(info.ComposeFile)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			if err := dockerClient.DeployCompose(c.Request.Context(), info.Path, string(composeData)); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}

		database.SaveService(db.Service{
			ID:        fmt.Sprintf("svc-%d", time.Now().UnixNano()),
			Name:      info.Path,
			Type:      "git",
			Repo:      req.Repo,
			CreatedAt: time.Now().Unix(),
		})

		c.JSON(http.StatusOK, gin.H{"status": "deployed", "info": info})
	})

	protected.GET("/services", func(c *gin.Context) {
		services, err := database.ListServices()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, templates)
	})

	protected.GET("/templates/:id", func(c *gin.Context) {
		templates, err := loadTemplates()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

		// Validate environment variable names
		for k := range req.Env {
			if err := validator.ValidateEnvVarName(k); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid env var name '%s': %s", k, err.Error())})
				return
			}
		}

		compose := tpl.Compose
		for k, v := range req.Env {
			compose = strings.ReplaceAll(compose, "${"+k+"}", v)
		}

		name := tpl.ID + "-" + fmt.Sprintf("%d", time.Now().Unix())
		if err := dockerClient.DeployCompose(c.Request.Context(), name, compose); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		database.SaveService(db.Service{
			ID:        fmt.Sprintf("svc-%d", time.Now().UnixNano()),
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			err := dockerClient.DeployComposeStreamed(context.Background(), name, compose, session)
			if err == nil {
				database.SaveService(db.Service{
					ID:        fmt.Sprintf("svc-%d", time.Now().UnixNano()),
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		if err := dockerClient.PullImage(c.Request.Context(), req.Image); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "pulled"})
	})

	protected.DELETE("/images/:id", func(c *gin.Context) {
		if err := dockerClient.RemoveImage(c.Request.Context(), c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, files)
	})

	protected.GET("/files/read", func(c *gin.Context) {
		content, err := dockerClient.ReadFile(c.Request.Context(), c.Query("container"), c.Query("path"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "written"})
	})

	protected.POST("/files/upload", func(c *gin.Context) {
		file, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
			return
		}
		src, _ := file.Open()
		defer src.Close()
		data, _ := io.ReadAll(src)
		if err := dockerClient.WriteFile(c.Request.Context(), c.PostForm("container"), c.PostForm("path")+"/"+file.Filename, string(data)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "uploaded"})
	})

	protected.GET("/files/download", func(c *gin.Context) {
		content, err := dockerClient.ReadFile(c.Request.Context(), c.Query("container"), c.Query("path"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Header("Content-Disposition", "attachment; filename="+filepath.Base(c.Query("path")))
		c.String(http.StatusOK, content)
	})

	protected.DELETE("/files", func(c *gin.Context) {
		if err := dockerClient.DeleteFile(c.Request.Context(), c.Query("container"), c.Query("path")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		domain := db.Domain{
			ID:      fmt.Sprintf("dom-%d", time.Now().UnixNano()),
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deployed"})
	})

	protected.DELETE("/tunnel", func(c *gin.Context) {
		if err := cfTunnel.Remove(c.Request.Context()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		conn.WriteJSON(gin.H{"error": fmt.Sprintf("container not found: %s", err.Error())})
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
		conn.WriteJSON(gin.H{"error": err.Error()})
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
				conn.WriteJSON(gin.H{"error": err.Error()})
				return
			}
			if err := conn.WriteJSON(stats); err != nil {
				return
			}
		}
	}
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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