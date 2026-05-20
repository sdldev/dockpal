package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/sdldev/dockpal/internal/validator"
)

// RegisterInstanceScopedRoutes registers container routes that operate on a specific instance.
// These routes expect the InstanceMiddleware to have already run and set agent_client and instance_id in context.
func RegisterInstanceScopedRoutes(g *gin.RouterGroup) {
	// Container routes
	g.GET("/containers", RequireRole(auth.RoleViewer), handleInstanceListContainers)
	g.GET("/containers/:id", RequireRole(auth.RoleViewer), handleInstanceInspectContainer)
	g.POST("/containers/:id/start", RequireRole(auth.RoleOperator), handleInstanceStartContainer)
	g.POST("/containers/:id/stop", RequireRole(auth.RoleOperator), handleInstanceStopContainer)
	g.POST("/containers/:id/restart", RequireRole(auth.RoleOperator), handleInstanceRestartContainer)
	g.DELETE("/containers/:id", RequireRole(auth.RoleOperator), handleInstanceRemoveContainer)
	g.PUT("/containers/:id", RequireRole(auth.RoleOperator), handleInstanceEditContainer)
	g.GET("/containers/:id/stats", RequireRole(auth.RoleViewer), handleInstanceContainerStats)
	g.GET("/containers/:id/logs", RequireRole(auth.RoleViewer), handleInstanceContainerLogs)

	// Deploy routes
	g.POST("/deploy/stream", RequireRole(auth.RoleOperator), handleInstanceDeployStream)
	g.POST("/deploy/compose", RequireRole(auth.RoleOperator), handleInstanceDeployCompose)
	g.POST("/deploy/git", RequireRole(auth.RoleOperator), handleInstanceDeployGit)
	g.POST("/templates/:id/deploy/stream", RequireRole(auth.RoleOperator), handleInstanceTemplateDeployStream)

	// Image routes
	g.GET("/images", RequireRole(auth.RoleViewer), handleInstanceListImages)
	g.POST("/images/pull", RequireRole(auth.RoleOperator), handleInstancePullImage)
	g.DELETE("/images/:id", RequireRole(auth.RoleOperator), handleInstanceRemoveImage)

	// Host routes
	g.GET("/host/info", RequireRole(auth.RoleViewer), handleInstanceHostInfo)
	g.GET("/host/stats", RequireRole(auth.RoleViewer), handleInstanceHostStats)
	g.GET("/system/info", RequireRole(auth.RoleViewer), handleInstanceSystemInfo)

	// Service routes
	g.GET("/services", RequireRole(auth.RoleViewer), handleInstanceListServices)
	g.DELETE("/services/:id", RequireRole(auth.RoleOperator), handleInstanceDeleteService)

	// Domain routes
	g.GET("/domains", RequireRole(auth.RoleViewer), handleInstanceListDomains)
	g.POST("/domains", RequireRole(auth.RoleOperator), handleInstanceCreateDomain)
	g.DELETE("/domains/:id", RequireRole(auth.RoleOperator), handleInstanceDeleteDomain)

	// Registry routes
	g.GET("/registries", RequireRole(auth.RoleViewer), handleInstanceListRegistries)
	g.POST("/registries", RequireRole(auth.RoleOperator), handleInstanceCreateRegistry)
	g.GET("/registries/:id", RequireRole(auth.RoleViewer), handleInstanceGetRegistry)
	g.PUT("/registries/:id", RequireRole(auth.RoleOperator), handleInstanceUpdateRegistry)
	g.DELETE("/registries/:id", RequireRole(auth.RoleOperator), handleInstanceDeleteRegistry)
	g.POST("/registries/:id/test", RequireRole(auth.RoleOperator), handleInstanceTestRegistry)
}

// handleInstanceListContainers lists all containers for the instance.
func handleInstanceListContainers(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

	containers, err := client.ListContainers(c.Request.Context(), true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list containers on instance %s: %v", instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, containers)
}

// handleInstanceInspectContainer returns detailed information about a container.
func handleInstanceInspectContainer(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	containerID := c.Param("id")

	detail, err := client.InspectContainer(c.Request.Context(), containerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to inspect container %s on instance %s: %v", containerID, instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, detail)
}

// handleInstanceStartContainer starts a container.
func handleInstanceStartContainer(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	containerID := c.Param("id")

	if err := client.StartContainer(c.Request.Context(), containerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start container %s on instance %s: %v", containerID, instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "started", "instance_id": instanceID, "container_id": containerID})
}

// handleInstanceStopContainer stops a container.
func handleInstanceStopContainer(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	containerID := c.Param("id")

	if err := client.StopContainer(c.Request.Context(), containerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stop container %s on instance %s: %v", containerID, instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "stopped", "instance_id": instanceID, "container_id": containerID})
}

// handleInstanceRestartContainer restarts a container.
func handleInstanceRestartContainer(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	containerID := c.Param("id")

	if err := client.RestartContainer(c.Request.Context(), containerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to restart container %s on instance %s: %v", containerID, instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "restarted", "instance_id": instanceID, "container_id": containerID})
}

// handleInstanceRemoveContainer removes a container.
func handleInstanceRemoveContainer(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	containerID := c.Param("id")
	force := c.Query("force") == "true"

	if err := client.RemoveContainer(c.Request.Context(), containerID, force); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to remove container %s on instance %s: %v", containerID, instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "removed", "instance_id": instanceID, "container_id": containerID})
}

// handleInstanceEditContainer edits a container (in-place updates or recreate).
func handleInstanceEditContainer(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	containerID := c.Param("id")

	var req docker.ContainerEditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Validate name if provided
	if req.Name != nil {
		if err := validateContainerName(*req.Name); err != nil {
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
			if err := validateEnvVarValue(env); err != nil {
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

	// Determine if recreate is needed
	needsRecreate := req.Image != nil || req.Env != nil || req.Ports != nil || req.Volumes != nil

	detail, err := client.EditContainer(c.Request.Context(), containerID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to edit container: %v", err)})
		return
	}

	response := gin.H{
		"status":    "updated",
		"instance":  instanceID,
		"container": detail,
	}
	if needsRecreate {
		response["recreated"] = true
	}

	c.JSON(http.StatusOK, response)
}

// handleInstanceContainerStats returns container statistics.
func handleInstanceContainerStats(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	containerID := c.Param("id")

	stats, err := client.GetContainerStats(c.Request.Context(), containerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get container stats for %s on instance %s: %v", containerID, instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleInstanceContainerLogs streams container logs via WebSocket.
func handleInstanceContainerLogs(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	containerID := c.Param("id")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	// Get tail parameter, default to "100"
	tail := c.DefaultQuery("tail", "100")

	reader, err := client.ContainerLogs(c.Request.Context(), containerID, tail)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Error: failed to retrieve container logs from instance %s: %v", instanceID, err)))
		conn.Close()
		return
	}

	streamContainerLogs(conn, reader)
}

// validateContainerName is a simple validation for container names.
// This duplicates validation logic from the validator package to avoid import cycles.
func validateContainerName(name string) error {
	if len(name) < 1 || len(name) > 128 {
		return fmt.Errorf("name must be between 1 and 128 characters")
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return fmt.Errorf("name can only contain letters, numbers, hyphens, underscores, and dots")
		}
	}
	if name[0] == '-' || name[0] == '_' || name[0] == '.' {
		return fmt.Errorf("name cannot start with a hyphen, underscore, or dot")
	}
	return nil
}

// validateEnvVarValue validates an environment variable value.
// This duplicates validation logic from the validator package to avoid import cycles.
func validateEnvVarValue(value string) error {
	// Check for null bytes and control characters (except newline, tab)
	for i, c := range value {
		if c == 0 {
			return fmt.Errorf("value contains null byte at position %d", i)
		}
		if c < 32 && c != 9 && c != 10 {
			return fmt.Errorf("value contains control character at position %d", i)
		}
	}
	return nil
}

// Deploy handlers for instance-scoped routes

// handleInstanceDeployStream handles POST /deploy/stream - deploy with streaming.
// It delegates to the remote agent for the given instance.
func handleInstanceDeployStream(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

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

	// Resolve registry credentials for this instance
	registryAuths := resolveRegistryAuths(c, req.Compose)
	database := getDatabase(c)
	name := req.Name
	domain := req.Domain
	compose := req.Compose

	// Use the global deploy manager so the WebSocket handler can find the session
	session := globalDeployManager.CreateSession()

	// Run deploy in background goroutine
	go func() {
		err := client.DeployComposeStreamed(context.Background(), name, compose, session, registryAuths)
		if err == nil && database != nil {
			database.SaveService(db.Service{
				ID:         generateID("svc"),
				Name:       name,
				Type:       "compose",
				Domain:     domain,
				Compose:    compose,
				InstanceID: instanceID,
				CreatedAt:  time.Now().Unix(),
			})
			if domain != "" {
				port := extractFirstPort(compose)
				traefik.GenerateConfig(domain, name, port)
			}
		}
		// Clean up session after 30 seconds
		time.AfterFunc(30*time.Second, func() {
			globalDeployManager.RemoveSession(session.ID)
		})
	}()

	c.JSON(http.StatusOK, gin.H{"deploy_id": session.ID})
}

// handleInstanceTemplateDeployStream handles POST /templates/:id/deploy/stream for instance-scoped template deploys.
func handleInstanceTemplateDeployStream(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

	templates, err := loadTemplates()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load templates"})
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

	// Resolve registry credentials for this instance
	registryAuths := resolveRegistryAuths(c, compose)
	database := getDatabase(c)
	domain := req.Domain

	session := globalDeployManager.CreateSession()

	go func() {
		err := client.DeployComposeStreamed(context.Background(), name, compose, session, registryAuths)
		if err == nil && database != nil {
			database.SaveService(db.Service{
				ID:         generateID("svc"),
				Name:       name,
				Type:       "template",
				Domain:     domain,
				Compose:    compose,
				InstanceID: instanceID,
				CreatedAt:  time.Now().Unix(),
			})
			if domain != "" {
				port := extractFirstPort(compose)
				traefik.GenerateConfig(domain, name, port)
			}
		}
		time.AfterFunc(30*time.Second, func() {
			globalDeployManager.RemoveSession(session.ID)
		})
	}()

	c.JSON(http.StatusOK, gin.H{"deploy_id": session.ID})
}

// handleInstanceDeployCompose handles POST /deploy/compose - deploy compose YAML.
func handleInstanceDeployCompose(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

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

	// Resolve registry credentials for this instance
	registryAuths := resolveRegistryAuths(c, req.Compose)

	// Deploy compose via agent client
	if err := client.DeployCompose(c.Request.Context(), req.Name, req.Compose, registryAuths); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("deploy failed: %v", err)})
		return
	}

	// Save service to database
	if database := getDatabase(c); database != nil {
		database.SaveService(db.Service{
			ID:         generateID("svc"),
			Name:       req.Name,
			Type:       "compose",
			Domain:     req.Domain,
			Compose:    req.Compose,
			InstanceID: instanceID,
			CreatedAt:  time.Now().Unix(),
		})

		// Generate Traefik config when domain is specified
		if req.Domain != "" {
			port := extractFirstPort(req.Compose)
			if err := traefik.GenerateConfig(req.Domain, req.Name, port); err != nil {
				fmt.Printf("Warning: failed to generate traefik config: %v", err)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "deployed"})
}

// handleInstanceDeployGit handles POST /deploy/git - clone git repo on server side and send compose to agent.
func handleInstanceDeployGit(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

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

	// Auto-fetch GitHub token from stored registry credentials (instance-scoped then global)
	registryMgr := getRegistryManager(c)
	var token string
	if registryMgr != nil {
		token, _ = registryMgr.GetTokenForDomain("github.com")
	}

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

	// Resolve registry credentials for this instance
	registryAuths := resolveRegistryAuths(c, string(composeData))

	// Deploy compose via agent client
	if err := client.DeployCompose(c.Request.Context(), projectName, string(composeData), registryAuths); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("deploy failed: %s", err.Error())})
		return
	}

	// Save service to database
	if database := getDatabase(c); database != nil {
		database.SaveService(db.Service{
			ID:         generateID("svc"),
			Name:       projectName,
			Type:       "git",
			Repo:       req.Repo,
			InstanceID: instanceID,
			CreatedAt:  time.Now().Unix(),
		})
	}

	c.JSON(http.StatusOK, gin.H{"status": "deployed", "info": info})
}

// resolveRegistryAuths extracts registry credentials for all image domains in a compose file.
// It resolves credentials per-instance using the database method with instance-specific then global fallback.
func resolveRegistryAuths(c *gin.Context, composeYAML string) map[string]string {
	// Extract image domains from compose YAML
	domains := extractDomainsFromCompose(composeYAML)
	if len(domains) == 0 {
		return nil
	}

	// Try to use registry manager if available (has access to decryption key)
	registryMgr := getRegistryManager(c)
	if registryMgr != nil {
		return resolveRegistryAuthsWithManager(registryMgr, composeYAML)
	}

	// Fall back to direct database lookup
	instanceID := c.MustGet("instance_id").(string)
	database := getDatabase(c)
	if database == nil {
		return nil
	}

	return resolveRegistryAuthsWithDB(database, instanceID, domains)
}

// resolveRegistryAuthsWithManager uses the registry manager to get auth headers.
func resolveRegistryAuthsWithManager(mgr *registry.Manager, composeYAML string) map[string]string {
	domains := extractDomainsFromCompose(composeYAML)
	if len(domains) == 0 {
		return nil
	}

	registryAuths := make(map[string]string)
	for _, domain := range domains {
		// Use the image ref format for the manager
		imageRef := domain + "/image:latest"
		authHeader, err := mgr.GetAuthHeader(imageRef)
		if err == nil && authHeader != "" {
			registryAuths[domain] = authHeader
		}
	}
	return registryAuths
}

// resolveRegistryAuthsWithDB uses direct database lookup for auth headers.
func resolveRegistryAuthsWithDB(database *db.DB, instanceID string, domains []string) map[string]string {
	registryAuths := make(map[string]string)

	for _, domain := range domains {
		// Try instance-specific credential first, then fall back to global
		cred, err := database.FindRegistryCredentialByDomainAndInstance(domain, instanceID)
		if err != nil || cred == nil {
			// Try alias fallback (e.g., ghcr.io → github.com)
			alias := getRegistryAlias(domain)
			if alias != "" {
				cred, err = database.FindRegistryCredentialByDomainAndInstance(alias, instanceID)
			}
		}
		if err != nil || cred == nil {
			continue // no credentials found for this domain
		}

		// Build auth header from credential (requires decryption which we can't do here)
		authHeader := buildAuthHeader(cred)
		if authHeader != "" {
			registryAuths[domain] = authHeader
		}
	}

	return registryAuths
}

// extractDomainsFromCompose extracts unique registry domains from a compose YAML.
func extractDomainsFromCompose(composeYAML string) []string {
	// Simple extraction: find all "image: " values and extract their domains
	// This is a simplified approach - a full implementation would parse the YAML
	seen := make(map[string]bool)
	var domains []string

	lines := strings.Split(composeYAML, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "image:") {
			imageRef := strings.TrimSpace(strings.TrimPrefix(line, "image:"))
			// Remove any quotes
			imageRef = strings.Trim(imageRef, "\"'")
			if imageRef == "" {
				continue
			}
			domain := extractDomain(imageRef)
			if domain != "" && !seen[domain] {
				seen[domain] = true
				domains = append(domains, domain)
			}
		}
	}
	return domains
}

// extractDomain extracts the domain from an image reference.
// Returns empty string for Docker Hub images (e.g., "nginx:latest").
func extractDomain(imageRef string) string {
	// Handle image:tag format
	parts := strings.Split(imageRef, "/")
	if len(parts) == 1 {
		// No slash - this is a Docker Hub official image
		return ""
	}
	// Check if first part looks like a domain (contains . or :)
	firstPart := parts[0]
	if strings.Contains(firstPart, ".") || strings.Contains(firstPart, ":") {
		return firstPart
	}
	// It's a Docker Hub user/image
	return ""
}

// getRegistryAlias returns the alias domain for a given registry.
func getRegistryAlias(domain string) string {
	aliases := map[string]string{
		"ghcr.io": "github.com",
	}
	return aliases[domain]
}

// buildAuthHeader builds a base64-encoded Docker auth header from a registry credential.
func buildAuthHeader(cred *db.RegistryCredential) string {
	// Note: The actual decryption would require the crypto key
	// For now, return empty - the registry manager handles this with full access
	return ""
}

// getDatabase retrieves the database from the Gin context.
func getDatabase(c *gin.Context) *db.DB {
	if dbVal, exists := c.Get("database"); exists {
		return dbVal.(*db.DB)
	}
	return nil
}

// getRegistryManager retrieves the registry manager from the Gin context.
func getRegistryManager(c *gin.Context) *registry.Manager {
	if rmVal, exists := c.Get("registry_manager"); exists {
		return rmVal.(*registry.Manager)
	}
	return nil
}

// =============================================================================
// Image handlers
// =============================================================================

// handleInstanceListImages lists all images for the instance.
func handleInstanceListImages(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

	images, err := client.ListImages(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list images on instance %s: %v", instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, images)
}

// handleInstancePullImage pulls an image to the instance.
func handleInstancePullImage(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

	var req struct {
		Image string `json:"image" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: image is required"})
		return
	}

	// Try authenticated pull if credentials are available
	registryMgr := getRegistryManager(c)
	if registryMgr != nil {
		authHeader, _ := registryMgr.GetAuthHeader(req.Image)
		if authHeader != "" {
			if err := client.PullImageWithAuth(c.Request.Context(), req.Image, authHeader); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to pull image on instance %s: %v", instanceID, err)})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "pulled"})
			return
		}
	}

	// Fallback to unauthenticated pull
	if err := client.PullImage(c.Request.Context(), req.Image); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to pull image on instance %s: %v", instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "pulled"})
}

// handleInstanceRemoveImage removes an image from the instance.
func handleInstanceRemoveImage(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)
	imageID := c.Param("id")

	if err := client.RemoveImage(c.Request.Context(), imageID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to remove image %s on instance %s: %v", imageID, instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "removed", "instance_id": instanceID, "image_id": imageID})
}

// =============================================================================
// Host handlers
// =============================================================================

// handleInstanceHostInfo returns host information for the instance.
func handleInstanceHostInfo(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

	info, err := client.GetHostInfo(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get host info from instance %s: %v", instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, info)
}

// handleInstanceHostStats returns host statistics for the instance.
func handleInstanceHostStats(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

	stats, err := client.GetHostStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get host stats from instance %s: %v", instanceID, err)})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleInstanceSystemInfo returns merged HostInfo and HostStats for the instance.
// This merges HostInfo (hostname, os, cpu_cores, docker_version) and
// HostStats (cpu_percent, used_ram, total_ram, used_disk, total_disk)
// into the SystemInfo format.
func handleInstanceSystemInfo(c *gin.Context) {
	client := c.MustGet("agent_client").(agent.AgentClient)
	instanceID := c.MustGet("instance_id").(string)

	info, err := client.GetHostInfo(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get host info from instance %s: %v", instanceID, err)})
		return
	}

	stats, err := client.GetHostStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get host stats from instance %s: %v", instanceID, err)})
		return
	}

	// Merge HostInfo and HostStats into SystemInfo format
	systemInfo := gin.H{
		"hostname":       info.Hostname,
		"os":             info.OS,
		"cpu_cores":      info.CPUCores,
		"docker_version": info.DockerVersion,
		"cpu_percent":    stats.CPUPercent,
		"used_ram":       stats.UsedRAM,
		"total_ram":      stats.TotalRAM,
		"used_disk":      stats.UsedDisk,
		"total_disk":     stats.TotalDisk,
	}

	c.JSON(http.StatusOK, systemInfo)
}

// =============================================================================
// Service handlers
// =============================================================================

// handleInstanceListServices lists services for the instance.
func handleInstanceListServices(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	database := getDatabase(c)

	if database == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	services, err := database.ListServicesByInstance(instanceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list services: %v", err)})
		return
	}

	c.JSON(http.StatusOK, services)
}

// handleInstanceDeleteService deletes a service for the instance.
func handleInstanceDeleteService(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	database := getDatabase(c)
	serviceID := c.Param("id")

	if database == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	svc, err := database.GetService(serviceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}

	// Verify the service belongs to this instance
	if svc.InstanceID != instanceID && !(instanceID == "local" && svc.InstanceID == "") {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}

	// Note: Compose removal would require the docker client directly
	// For instance-scoped services, we just delete the database record
	// The actual container cleanup would need to be added to the AgentClient interface

	// Remove Traefik config when service has an associated domain
	if svc.Domain != "" {
		if err := traefik.RemoveDomain(svc.Name); err != nil {
			fmt.Printf("Warning: failed to remove traefik config for %s: %v", svc.Name, err)
		}
	}

	if err := database.DeleteService(serviceID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete service: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// =============================================================================
// Domain handlers
// =============================================================================

// handleInstanceListDomains lists domains for the instance.
func handleInstanceListDomains(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	database := getDatabase(c)

	if database == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	domains, err := database.ListDomainsByInstance(instanceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list domains: %v", err)})
		return
	}

	c.JSON(http.StatusOK, domains)
}

// handleInstanceCreateDomain creates a domain for the instance.
func handleInstanceCreateDomain(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	database := getDatabase(c)

	if database == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	var req struct {
		Domain  string `json:"domain" binding:"required"`
		Service string `json:"service" binding:"required"`
		Port    int    `json:"port" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: domain, service, and port are required"})
		return
	}

	// Validate domain
	if err := validator.ValidateDomain(req.Domain); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid domain: %s", err.Error())})
		return
	}

	// Generate ID
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate ID"})
		return
	}
	id := fmt.Sprintf("dom-%x", b)

	domain := db.Domain{
		ID:         id,
		InstanceID: instanceID,
		Domain:     req.Domain,
		Service:    req.Service,
		Port:       req.Port,
	}

	if err := database.SaveDomain(domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save domain: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, domain)
}

// handleInstanceDeleteDomain deletes a domain for the instance.
func handleInstanceDeleteDomain(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	database := getDatabase(c)
	domainID := c.Param("id")

	if database == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Get domain to verify it belongs to this instance
	domains, err := database.ListDomainsByInstance(instanceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list domains: %v", err)})
		return
	}

	// Find the domain
	found := false
	for _, d := range domains {
		if d.ID == domainID {
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "domain not found"})
		return
	}

	if err := database.DeleteDomain(domainID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete domain: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// =============================================================================
// Registry handlers
// =============================================================================

// RegistryResponse represents a registry credential with scope indicator for API responses.
type RegistryResponse struct {
	ID              string `json:"id"`
	Registry        string `json:"registry"`
	Username        string `json:"username"`
	MaskedToken     string `json:"masked_token"`
	Status          string `json:"status"`
	Scope           string `json:"scope"` // "global" or "instance"
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	LastValidatedAt int64  `json:"last_validated_at"`
}

// handleInstanceListRegistries lists all registries for the instance.
// Returns both instance-specific and global credentials with scope indicator.
func handleInstanceListRegistries(c *gin.Context) {
	database := getDatabase(c)
	registryMgr := getRegistryManager(c)

	if database == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Get all registry credentials from database
	allCreds, err := database.ListRegistryCredentials()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list registries: %v", err)})
		return
	}

	var responses []RegistryResponse
	for _, cred := range allCreds {
		// Determine scope
		scope := "global"
		if cred.InstanceID != "" {
			scope = "instance"
		}

		// Get masked token
		maskedToken := "****"
		if registryMgr != nil {
			token, err := registryMgr.GetTokenForDomain(cred.Registry)
			if err == nil && token != "" {
				maskedToken = "****" + token[len(token)-4:]
			}
		}

		// Determine status
		status := "unknown"
		if cred.LastValidatedAt != 0 {
			if time.Now().Unix()-cred.LastValidatedAt < 7*24*60*60 {
				status = "valid"
			}
		}

		responses = append(responses, RegistryResponse{
			ID:              cred.ID,
			Registry:        cred.Registry,
			Username:        cred.Username,
			MaskedToken:     maskedToken,
			Status:          status,
			Scope:           scope,
			CreatedAt:       cred.CreatedAt,
			UpdatedAt:       cred.UpdatedAt,
			LastValidatedAt: cred.LastValidatedAt,
		})
	}

	c.JSON(http.StatusOK, responses)
}

// handleInstanceCreateRegistry creates a registry credential for the instance.
func handleInstanceCreateRegistry(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	registryMgr := getRegistryManager(c)
	database := getDatabase(c)

	if registryMgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "registry manager not available"})
		return
	}

	if database == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	var req struct {
		Registry string `json:"registry" binding:"required,max=253"`
		Username string `json:"username" binding:"required,max=100"`
		Token    string `json:"token" binding:"required,max=255"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: registry, username, and token are required"})
		return
	}

	// Use registry manager's Create method - it creates/updates credentials
	createReq := registry.CreateRequest{
		Registry: req.Registry,
		Username: req.Username,
		Token:    req.Token,
	}

	credSummary, err := registryMgr.Create(createReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If this is supposed to be instance-specific, update the credential
	if instanceID != "local" {
		// Get the stored credential and update its InstanceID
		cred, err := database.GetRegistryCredential(credSummary.ID)
		if err == nil {
			cred.InstanceID = instanceID
			database.SaveRegistryCredential(*cred)
		}
	}

	c.JSON(http.StatusCreated, credSummary)
}

// handleInstanceGetRegistry gets a registry credential by ID.
func handleInstanceGetRegistry(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	registryMgr := getRegistryManager(c)
	database := getDatabase(c)
	registryID := c.Param("id")

	if registryMgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "registry manager not available"})
		return
	}

	// Get the credential
	cred, err := database.GetRegistryCredential(registryID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	// Verify scope: either global or belongs to this instance
	if cred.InstanceID != "" && cred.InstanceID != instanceID {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	// Get full credential for token
	summary, err := registryMgr.Get(registryID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	// Add scope to response
	scope := "global"
	if cred.InstanceID != "" {
		scope = "instance"
	}

	response := RegistryResponse{
		ID:              summary.ID,
		Registry:        summary.Registry,
		Username:        summary.Username,
		MaskedToken:     summary.MaskedToken,
		Status:          summary.Status,
		Scope:           scope,
		CreatedAt:       summary.CreatedAt,
		UpdatedAt:       summary.UpdatedAt,
		LastValidatedAt: summary.LastValidatedAt,
	}

	c.JSON(http.StatusOK, response)
}

// handleInstanceUpdateRegistry updates a registry credential.
func handleInstanceUpdateRegistry(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	registryMgr := getRegistryManager(c)
	database := getDatabase(c)
	registryID := c.Param("id")

	if registryMgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "registry manager not available"})
		return
	}

	// Verify the credential exists and belongs to this instance
	cred, err := database.GetRegistryCredential(registryID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	// Verify scope
	if cred.InstanceID != "" && cred.InstanceID != instanceID {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	var req struct {
		Username string `json:"username,omitempty"`
		Token    string `json:"token,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	updateReq := registry.UpdateRequest{
		Username: req.Username,
		Token:    req.Token,
	}

	if err := registryMgr.Update(registryID, updateReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// handleInstanceDeleteRegistry deletes a registry credential.
func handleInstanceDeleteRegistry(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	registryMgr := getRegistryManager(c)
	database := getDatabase(c)
	registryID := c.Param("id")

	if registryMgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "registry manager not available"})
		return
	}

	// Verify the credential exists and belongs to this instance
	cred, err := database.GetRegistryCredential(registryID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	// Verify scope: either global or belongs to this instance
	if cred.InstanceID != "" && cred.InstanceID != instanceID {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	if err := registryMgr.Delete(registryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// handleInstanceTestRegistry tests a registry connection.
func handleInstanceTestRegistry(c *gin.Context) {
	instanceID := c.MustGet("instance_id").(string)
	registryMgr := getRegistryManager(c)
	database := getDatabase(c)
	registryID := c.Param("id")

	if registryMgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "registry manager not available"})
		return
	}

	// Verify the credential exists and belongs to this instance
	cred, err := database.GetRegistryCredential(registryID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	// Verify scope
	if cred.InstanceID != "" && cred.InstanceID != instanceID {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}

	result, err := registryMgr.TestConnection(registryID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}
