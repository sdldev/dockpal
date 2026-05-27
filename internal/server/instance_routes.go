package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
	"github.com/sdldev/dockpal/internal/ssh"

	"golang.org/x/crypto/bcrypt"
)

// Instance request/response types

type CreateInstanceRequest struct {
	Name string `json:"name" binding:"required,min=1,max=100"`
	Host string `json:"host"`
	Port int    `json:"port"`
	Mode string `json:"mode" binding:"required,oneof=direct edge"`
}

type InstanceResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	DockerVersion  string `json:"docker_version,omitempty"`
	OS             string `json:"os,omitempty"`
	CPUCores       int    `json:"cpu_cores,omitempty"`
	TotalMemory    int64  `json:"total_memory,omitempty"`
	LastSeen       int64  `json:"last_seen,omitempty"`
	CreatedAt      int64  `json:"created_at,omitempty"`
	InstallCommand string `json:"install_command,omitempty"`
	AgentVersion   string `json:"agent_version,omitempty"`
}

type InstanceListItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	LastSeen int64  `json:"last_seen"`
}

type UpdateInstanceRequest struct {
	Name string `json:"name" binding:"min=1,max=100"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type TestResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// RegisterInstanceRoutes adds instance CRUD and enrollment endpoints.
func RegisterInstanceRoutes(g *gin.RouterGroup, database *db.DB, agentMgr *agent.Manager, jwtSecret string, logsManager *InstallLogsManager) {
	g.POST("/instances", RequireRole(auth.RoleAdmin), handleCreateInstance(database, jwtSecret))
	g.GET("/instances", RequireRole(auth.RoleViewer), handleListInstances(database))
	g.GET("/instances/:instance_id", RequireRole(auth.RoleViewer), handleGetInstance(database, jwtSecret))
	g.PUT("/instances/:instance_id", RequireRole(auth.RoleAdmin), handleUpdateInstance(database))
	g.DELETE("/instances/:instance_id", RequireRole(auth.RoleAdmin), handleDeleteInstance(database, agentMgr))
	g.POST("/instances/:instance_id/test", RequireRole(auth.RoleOperator), handleTestInstance(agentMgr, database))
	g.POST("/instances/:instance_id/rotate-token", RequireRole(auth.RoleAdmin), handleRotateToken(database, jwtSecret))
	g.POST("/instances/:instance_id/install", RequireRole(auth.RoleAdmin), handleInstallAgent(database, jwtSecret, logsManager))
	g.GET("/instances/:instance_id/install/logs", RequireRole(auth.RoleAdmin), handleInstallAgentLogs(logsManager))
}

// handleCreateInstance creates a new instance with a generated agent token.
func handleCreateInstance(database *db.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateInstanceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
			return
		}

		// Validate mode-specific fields
		if req.Mode == "direct" {
			if req.Host == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "host is required for direct mode"})
				return
			}
			if req.Port < 1 || req.Port > 65535 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "port must be between 1 and 65535"})
				return
			}
		}

		// Generate 32-byte random token
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
			return
		}
		token := hex.EncodeToString(tokenBytes)

		// Hash token with bcrypt
		hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash token"})
			return
		}

		// Derive encryption key and encrypt token
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive encryption key"})
			return
		}
		encryptedToken, err := registry.Encrypt([]byte(token), cryptoKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt token"})
			return
		}

		// Generate instance ID
		instanceID := generateInstanceID()

		// Default port for direct mode
		port := req.Port
		if req.Mode == "direct" && port == 0 {
			port = 9273
		}

		// Create instance record
		instance := db.Instance{
			ID:                  instanceID,
			Name:                req.Name,
			Host:                req.Host,
			Port:                port,
			Mode:                req.Mode,
			AgentTokenHash:      string(hash),
			AgentTokenEncrypted: encryptedToken,
			Status:              "enrolling",
			CreatedAt:           time.Now().Unix(),
		}

		if err := database.SaveInstance(instance); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save instance"})
			return
		}

		LogAudit(c, database, "instance.create", instanceID, "success", fmt.Sprintf("Created instance '%s' in mode '%s'", req.Name, req.Mode))

		// Generate install command
		serverHost := c.Request.Host
		installCmd := generateInstallCommand(req.Mode, serverHost, token)

		c.JSON(http.StatusCreated, InstanceResponse{
			ID:             instanceID,
			Name:           req.Name,
			Host:           req.Host,
			Port:           port,
			Mode:           req.Mode,
			Status:         "enrolling",
			CreatedAt:      instance.CreatedAt,
			InstallCommand: installCmd,
		})
	}
}

// handleListInstances returns all instances with summary fields.
func handleListInstances(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		instances, err := database.ListInstances()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list instances"})
			return
		}

		result := make([]InstanceListItem, len(instances))
		for i, inst := range instances {
			result[i] = InstanceListItem{
				ID:       inst.ID,
				Name:     inst.Name,
				Host:     inst.Host,
				Port:     inst.Port,
				Mode:     inst.Mode,
				Status:   inst.Status,
				LastSeen: inst.LastSeen,
			}
		}

		c.JSON(http.StatusOK, result)
	}
}

// handleGetInstance returns full instance details or 404.
func handleGetInstance(database *db.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		inst, err := database.GetInstance(id)
		if err != nil {
			if errors.Is(err, db.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get instance"})
			return
		}

		// Generate install command if token is available
		var installCmd string
		if len(inst.AgentTokenEncrypted) > 0 {
			cryptoKey, err := registry.DeriveKey(jwtSecret)
			if err == nil {
				token, err := registry.Decrypt(inst.AgentTokenEncrypted, cryptoKey)
				if err == nil {
					serverHost := c.Request.Host
					installCmd = generateInstallCommand(inst.Mode, serverHost, string(token))
				}
			}
		}

		c.JSON(http.StatusOK, InstanceResponse{
			ID:             inst.ID,
			Name:           inst.Name,
			Host:           inst.Host,
			Port:           inst.Port,
			Mode:           inst.Mode,
			Status:         inst.Status,
			LastSeen:       inst.LastSeen,
			CreatedAt:      inst.CreatedAt,
			AgentVersion:   inst.AgentVersion,
			DockerVersion:  inst.DockerVersion,
			OS:             inst.OS,
			CPUCores:       inst.CPUCores,
			TotalMemory:    inst.TotalMemory,
			InstallCommand: installCmd,
		})
	}
}

// handleUpdateInstance updates instance fields with validation.
func handleUpdateInstance(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		// Check if instance exists
		inst, err := database.GetInstance(id)
		if err != nil {
			if errors.Is(err, db.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get instance"})
			return
		}

		var req UpdateInstanceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
			return
		}

		// Validate and update fields
		if req.Name != "" {
			if len(req.Name) < 1 || len(req.Name) > 100 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "name must be between 1 and 100 characters"})
				return
			}
			inst.Name = req.Name
		}

		if req.Host != "" {
			inst.Host = req.Host
		}

		if req.Port > 0 {
			if req.Port < 1 || req.Port > 65535 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "port must be between 1 and 65535"})
				return
			}
			inst.Port = req.Port
		}

		if err := database.SaveInstance(*inst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update instance"})
			return
		}

		LogAudit(c, database, "instance.update", id, "success", fmt.Sprintf("Updated instance: Name='%s', Host='%s', Port=%d", inst.Name, inst.Host, inst.Port))

		c.JSON(http.StatusOK, InstanceResponse{
			ID:            inst.ID,
			Name:          inst.Name,
			Host:          inst.Host,
			Port:          inst.Port,
			Mode:          inst.Mode,
			Status:        inst.Status,
			LastSeen:      inst.LastSeen,
			CreatedAt:     inst.CreatedAt,
			AgentVersion:  inst.AgentVersion,
			DockerVersion: inst.DockerVersion,
			OS:            inst.OS,
			CPUCores:      inst.CPUCores,
			TotalMemory:   inst.TotalMemory,
		})
	}
}

// handleDeleteInstance removes an instance or rejects deletion of local instance.
func handleDeleteInstance(database *db.DB, agentMgr *agent.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		// Reject deletion of local instance
		if id == "local" {
			c.JSON(http.StatusForbidden, gin.H{"error": "cannot delete the local instance"})
			return
		}

		// Get instance to check mode and disconnect agent if needed
		inst, err := database.GetInstance(id)
		if err != nil {
			if errors.Is(err, db.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get instance"})
			return
		}

		// Disconnect edge agent if connected
		if inst.Mode == "edge" {
			agentMgr.UnregisterEdgeConnection(id)
		}

		// Delete from database
		if err := database.DeleteInstance(id); err != nil {
			if errors.Is(err, db.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			if errors.Is(err, db.ErrCannotDeleteLocal) {
				c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete instance"})
			return
		}

		LogAudit(c, database, "instance.delete", id, "success", fmt.Sprintf("Deleted instance '%s'", id))

		c.JSON(http.StatusOK, gin.H{"message": "instance deleted"})
	}
}

// handleTestInstance tests connectivity to an agent with 10s timeout.
func handleTestInstance(agentMgr *agent.Manager, database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		// Get client for the instance
		client, err := agentMgr.GetClient(id)
		if err != nil {
			if errors.Is(err, agent.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			if errors.Is(err, agent.ErrInstanceOffline) {
				c.JSON(http.StatusOK, TestResult{Status: "error", Message: "instance is offline"})
				return
			}
			internalError(c, err)
			return
		}

		// Try to ping the agent
		if err := client.Ping(ctx); err != nil {
			c.JSON(http.StatusOK, TestResult{Status: "error", Message: fmt.Sprintf("connection failed: %v", err)})
			return
		}

		// Ping successful — update status to online and last_seen
		database.UpdateInstanceStatus(id, "online")
		database.UpdateInstanceLastSeen(id, time.Now().Unix())

		c.JSON(http.StatusOK, TestResult{Status: "ok", Message: "connection successful"})
	}
}

// handleRotateToken generates a new token for an instance.
func handleRotateToken(database *db.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		// Get existing instance
		inst, err := database.GetInstance(id)
		if err != nil {
			if errors.Is(err, db.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get instance"})
			return
		}

		// Generate new 32-byte random token
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
			return
		}
		token := hex.EncodeToString(tokenBytes)

		// Hash token with bcrypt
		hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash token"})
			return
		}

		// Derive encryption key and encrypt token
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive encryption key"})
			return
		}
		encryptedToken, err := registry.Encrypt([]byte(token), cryptoKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt token"})
			return
		}

		// Update instance
		inst.AgentTokenHash = string(hash)
		inst.AgentTokenEncrypted = encryptedToken

		if err := database.SaveInstance(*inst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update instance"})
			return
		}

		LogAudit(c, database, "instance.rotate_token", id, "success", fmt.Sprintf("Rotated agent enrollment token for instance '%s'", id))

		// Generate install command
		serverHost := c.Request.Host
		installCmd := generateInstallCommand(inst.Mode, serverHost, token)

		c.JSON(http.StatusOK, InstanceResponse{
			ID:             inst.ID,
			Name:           inst.Name,
			Host:           inst.Host,
			Port:           inst.Port,
			Mode:           inst.Mode,
			Status:         inst.Status,
			LastSeen:       inst.LastSeen,
			CreatedAt:      inst.CreatedAt,
			AgentVersion:   inst.AgentVersion,
			DockerVersion:  inst.DockerVersion,
			OS:             inst.OS,
			CPUCores:       inst.CPUCores,
			TotalMemory:    inst.TotalMemory,
			InstallCommand: installCmd,
		})
	}
}

// === Helper functions ===

// generateInstanceID creates a unique instance ID.
func generateInstanceID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return fmt.Sprintf("inst-%s", hex.EncodeToString(bytes)[:12])
}

// generateInstallCommand generates the Docker run command for agent installation.
func generateInstallCommand(mode, serverHost, token string) string {
	agentImg := os.Getenv("DOCKPAL_AGENT_IMAGE")
	if agentImg == "" {
		agentImg = "ghcr.io/sdldev/dockpal-agent:latest"
	}

	var runCmd string
	switch mode {
	case "direct":
		// For direct mode: include host, port mapping, token
		runCmd = fmt.Sprintf(
			"docker run -d --name dockpal-agent --restart unless-stopped \\\n  -e DOCKPAL_MODE=direct \\\n  -e DOCKPAL_TOKEN=%s \\\n  -p 9273:9273 \\\n  -v /var/run/docker.sock:/var/run/docker.sock \\\n  -v /opt/dockpal-agent:/opt/dockpal-agent \\\n  %s",
			token,
			agentImg,
		)
	case "edge":
		// For edge mode: include server WebSocket URL, token, no port mapping
		wsURL := fmt.Sprintf("wss://%s/api/agent/connect", serverHost)
		runCmd = fmt.Sprintf(
			"docker run -d --name dockpal-agent --restart unless-stopped \\\n  -e DOCKPAL_MODE=edge \\\n  -e DOCKPAL_SERVER=%s \\\n  -e DOCKPAL_TOKEN=%s \\\n  -v /var/run/docker.sock:/var/run/docker.sock \\\n  -v /opt/dockpal-agent:/opt/dockpal-agent \\\n  %s",
			wsURL,
			token,
			agentImg,
		)
	default:
		return ""
	}

	return fmt.Sprintf(
		"if ! command -v docker >/dev/null 2>&1; then\n  echo \"[Dockpal] Docker is not installed. Installing Docker...\"\n  curl -fsSL https://get.docker.com | sh\n  sudo systemctl enable --now docker || true\nfi\n\n%s",
		runCmd,
	)
}

type InstallAgentRequest struct {
	SSHHost       string `json:"ssh_host" binding:"required"`
	SSHPort       int    `json:"ssh_port"`
	SSHUser       string `json:"ssh_user"`
	SSHAuthType   string `json:"ssh_auth_type" binding:"required,oneof=password key"`
	SSHSecret     string `json:"ssh_secret" binding:"required"`
	InstallDocker bool   `json:"install_docker"`
}

type logWriter struct {
	instanceID string
	mgr        *InstallLogsManager
}

func (lw *logWriter) Write(p []byte) (n int, err error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if trimmed != "" {
			lw.mgr.WriteLog(lw.instanceID, trimmed)
		}
	}
	return len(p), nil
}

func handleInstallAgent(database *db.DB, jwtSecret string, logsManager *InstallLogsManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		var req InstallAgentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
			return
		}

		inst, err := database.GetInstance(id)
		if err != nil {
			if errors.Is(err, db.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get instance"})
			return
		}

		// Decrypt agent token
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive decryption key"})
			return
		}

		tokenBytes, err := registry.Decrypt(inst.AgentTokenEncrypted, cryptoKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt agent token"})
			return
		}
		token := hex.EncodeToString(tokenBytes)

		// Encrypt SSH Secret (password or key)
		encryptedSecret, err := registry.Encrypt([]byte(req.SSHSecret), cryptoKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt SSH secret"})
			return
		}

		// Update SSH details in database
		inst.SSHHost = req.SSHHost
		inst.SSHPort = req.SSHPort
		if inst.SSHPort == 0 {
			inst.SSHPort = 22
		}
		inst.SSHUser = req.SSHUser
		if inst.SSHUser == "" {
			inst.SSHUser = "root"
		}
		inst.SSHAuthType = req.SSHAuthType
		if req.SSHAuthType == "key" {
			inst.SSHKeyEncrypted = encryptedSecret
			inst.SSHPasswordEncrypted = nil
		} else {
			inst.SSHPasswordEncrypted = encryptedSecret
			inst.SSHKeyEncrypted = nil
		}
		inst.Status = "enrolling"

		if err := database.SaveInstance(*inst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save instance details"})
			return
		}

		LogAudit(c, database, "instance.install_start", id, "success", fmt.Sprintf("Started agent installation on remote host %s:%d", req.SSHHost, req.SSHPort))

		// Clear/Reset logs for this session
		logsManager.RemoveSession(id)

		// Capture parameters for the background goroutine
		host := c.Request.Host
		isSecureWS := c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https"

		go func() {
			lw := &logWriter{instanceID: id, mgr: logsManager}
			logsManager.WriteLogf(id, "[Dockpal Installer] Initializing installation on remote host %s:%d...\n", req.SSHHost, req.SSHPort)

			agentImg := os.Getenv("DOCKPAL_AGENT_IMAGE")
			if agentImg == "" {
				agentImg = "ghcr.io/sdldev/dockpal-agent:latest"
			}

			params := ssh.InstallParams{
				Host:          req.SSHHost,
				Port:          req.SSHPort,
				User:          req.SSHUser,
				AuthType:      req.SSHAuthType,
				AuthSecret:    req.SSHSecret,
				InstallDocker: req.InstallDocker,
				Mode:          inst.Mode,
				Token:         token,
				ServerHost:    host,
				AgentImage:    agentImg,
				IsSecureWS:    isSecureWS,
			}

			err := ssh.InstallAgent(params, lw)
			if err != nil {
				log.Printf("SSH Install on instance %s failed: %v", id, err)
				logsManager.WriteLogf(id, "[Dockpal Installer] Error: %v\n", err)

				// update status to offline if failed
				instCopy, _ := database.GetInstance(id)
				if instCopy != nil {
					instCopy.Status = "offline"
					database.SaveInstance(*instCopy)
				}
			} else {
				log.Printf("SSH Install on instance %s completed successfully", id)
				logsManager.WriteLog(id, "[Dockpal Installer] Installation completed successfully! Waiting for agent to connect...")
			}
			logsManager.CompleteSession(id)
		}()

		c.JSON(http.StatusAccepted, gin.H{"message": "installation started"})
	}
}

var installWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     checkOrigin,
}

func handleInstallAgentLogs(logsManager *InstallLogsManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		conn, err := installWebSocketUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("Failed to upgrade installation logs WebSocket: %v", err)
			return
		}
		defer conn.Close()

		ch, history, deregister := logsManager.RegisterListener(id)
		defer deregister()

		// 1. Send all existing log history
		for _, line := range history {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
				return
			}
		}

		// 2. Stream new logs as they arrive
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		go func() {
			for {
				_, _, err := conn.ReadMessage()
				if err != nil {
					return
				}
			}
		}()

		for {
			select {
			case line, ok := <-ch:
				if !ok {
					// Channel closed, session completed
					_ = conn.WriteMessage(websocket.TextMessage, []byte("[Dockpal Installer] Session disconnected."))
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
					return
				}
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}
}
