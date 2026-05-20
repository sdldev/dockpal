package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"

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
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Mode                string `json:"mode"`
	Status              string `json:"status"`
	DockerVersion       string `json:"docker_version,omitempty"`
	OS                  string `json:"os,omitempty"`
	CPUCores            int    `json:"cpu_cores,omitempty"`
	TotalMemory         int64  `json:"total_memory,omitempty"`
	LastSeen            int64  `json:"last_seen,omitempty"`
	CreatedAt           int64  `json:"created_at,omitempty"`
	InstallCommand      string `json:"install_command,omitempty"`
	AgentVersion        string `json:"agent_version,omitempty"`
}

type InstanceListItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Mode      string `json:"mode"`
	Status    string `json:"status"`
	LastSeen  int64  `json:"last_seen"`
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
func RegisterInstanceRoutes(g *gin.RouterGroup, database *db.DB, agentMgr *agent.Manager, jwtSecret string) {
	g.POST("/instances", RequireRole(auth.RoleAdmin), handleCreateInstance(database, jwtSecret))
	g.GET("/instances", RequireRole(auth.RoleViewer), handleListInstances(database))
	g.GET("/instances/:instance_id", RequireRole(auth.RoleViewer), handleGetInstance(database))
	g.PUT("/instances/:instance_id", RequireRole(auth.RoleAdmin), handleUpdateInstance(database))
	g.DELETE("/instances/:instance_id", RequireRole(auth.RoleAdmin), handleDeleteInstance(database, agentMgr))
	g.POST("/instances/:instance_id/test", RequireRole(auth.RoleOperator), handleTestInstance(agentMgr))
	g.POST("/instances/:instance_id/rotate-token", RequireRole(auth.RoleAdmin), handleRotateToken(database, jwtSecret))
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
		encryptedToken, err := registry.Encrypt(tokenBytes, cryptoKey)
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
func handleGetInstance(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		inst, err := database.GetInstance(id)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get instance"})
			return
		}

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

// handleUpdateInstance updates instance fields with validation.
func handleUpdateInstance(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		// Check if instance exists
		inst, err := database.GetInstance(id)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
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
			if strings.Contains(err.Error(), "not found") {
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
			if strings.Contains(err.Error(), "not found") {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			if strings.Contains(err.Error(), "cannot delete") {
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
func handleTestInstance(agentMgr *agent.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		// Get client for the instance
		client, err := agentMgr.GetClient(id)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			if strings.Contains(err.Error(), "offline") {
				c.JSON(http.StatusOK, TestResult{Status: "error", Message: "instance is offline"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Try to ping the agent
		if err := client.Ping(ctx); err != nil {
			c.JSON(http.StatusOK, TestResult{Status: "error", Message: fmt.Sprintf("connection failed: %v", err)})
			return
		}

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
			if strings.Contains(err.Error(), "not found") {
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
		encryptedToken, err := registry.Encrypt(tokenBytes, cryptoKey)
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
	switch mode {
	case "direct":
		// For direct mode: include host, port mapping, token
		return fmt.Sprintf(
			"docker run -d --name dockpal-agent \\\n  -e DOCKPAL_MODE=direct \\\n  -e DOCKPAL_TOKEN=%s \\\n  -p 9273:9273 \\\n  -v /var/run/docker.sock:/var/run/docker.sock \\\n  sdldev/dockpal-agent:latest",
			token,
		)
	case "edge":
		// For edge mode: include server WebSocket URL, token, no port mapping
		wsURL := fmt.Sprintf("wss://%s/api/agent/connect", serverHost)
		return fmt.Sprintf(
			"docker run -d --name dockpal-agent \\\n  -e DOCKPAL_MODE=edge \\\n  -e DOCKPAL_SERVER=%s \\\n  -e DOCKPAL_TOKEN=%s \\\n  -v /var/run/docker.sock:/var/run/docker.sock \\\n  sdldev/dockpal-agent:latest",
			wsURL,
			token,
		)
	default:
		return ""
	}
}