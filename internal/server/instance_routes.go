package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
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
	// SSH hardening state (see db.Instance).
	SSHAuthType        string `json:"ssh_auth_type,omitempty"`
	SSHHardeningStatus string `json:"ssh_hardening_status,omitempty"`
	SSHHardenedAt      int64  `json:"ssh_hardened_at,omitempty"`
	SSHKeyFingerprint  string `json:"ssh_key_fingerprint,omitempty"`
	// Panel-generated keypair (Phase 1 bootstrap): the public half is not a
	// secret and is shown so the operator can authorize it before install.
	SSHPublicKey       string `json:"ssh_public_key,omitempty"`
	SSHKeySetupCommand string `json:"ssh_key_setup_command,omitempty"`
}

type InstanceListItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	LastSeen int64  `json:"last_seen"`
	// Enough for the Servers-table security badge without secrets.
	SSHAuthType        string `json:"ssh_auth_type,omitempty"`
	SSHHardeningStatus string `json:"ssh_hardening_status,omitempty"`
	SSHHardenedAt      int64  `json:"ssh_hardened_at,omitempty"`
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
	hardenRunning := &sync.Map{} // instanceID -> struct{}{} while a harden job runs
	g.POST("/instances", RequireRole(auth.RoleAdmin), handleCreateInstance(database, jwtSecret))
	g.GET("/instances", RequireRole(auth.RoleViewer), handleListInstances(database))
	g.GET("/instances/:instance_id", RequireRole(auth.RoleViewer), handleGetInstance(database, jwtSecret))
	g.PUT("/instances/:instance_id", RequireRole(auth.RoleAdmin), handleUpdateInstance(database))
	g.DELETE("/instances/:instance_id", RequireRole(auth.RoleAdmin), handleDeleteInstance(database, agentMgr))
	g.POST("/instances/:instance_id/test", RequireRole(auth.RoleOperator), handleTestInstance(agentMgr, database))
	g.POST("/instances/:instance_id/rotate-token", RequireRole(auth.RoleAdmin), handleRotateToken(database, jwtSecret))
	g.POST("/instances/:instance_id/install", RequireRole(auth.RoleAdmin), handleInstallAgent(database, jwtSecret, logsManager))
	g.GET("/instances/:instance_id/install/logs", RequireRole(auth.RoleAdmin), handleInstallAgentLogs(logsManager))
	g.POST("/instances/:instance_id/harden", RequireRole(auth.RoleAdmin), handleHardenSSH(database, jwtSecret, logsManager, hardenRunning))
	g.GET("/instances/:instance_id/harden/logs", RequireRole(auth.RoleAdmin), handleHardenSSHLogs(logsManager))
}

// handleCreateInstance creates a new instance with a generated agent token.
func handleCreateInstance(database *db.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateInstanceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
			return
		}

		// Validate mode-specific fields. A port of 0 means "use the default
		// (9273)" and is normalized below, so it is allowed here.
		if req.Mode == "direct" {
			if req.Host == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "host is required for direct mode"})
				return
			}
			if req.Port < 0 || req.Port > 65535 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "port must be between 0 (default 9273) and 65535"})
				return
			}
		}

		// Uniqueness checks. Registering the same direct host:port twice creates
		// two records fighting over a single agent container (they desync each
		// other's tokens — see the vps-schoolhub/vps-media duplicate); a name
		// collision is almost as confusing in the UI. Reject both up front.
		if existing, err := database.ListInstances(); err == nil {
			normalizedHost := strings.ToLower(strings.TrimSpace(req.Host))
			targetPort := req.Port
			if req.Mode == "direct" && targetPort == 0 {
				targetPort = 9273
			}
			for _, inst := range existing {
				if strings.EqualFold(inst.Name, strings.TrimSpace(req.Name)) {
					c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("an instance named %q already exists", req.Name)})
					return
				}
				if req.Mode == "direct" && inst.Mode == "direct" &&
					strings.ToLower(strings.TrimSpace(inst.Host)) == normalizedHost && inst.Port == targetPort {
					c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("an instance for %s:%d already exists (%q)", req.Host, targetPort, inst.Name)})
					return
				}
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

		// Generate the panel's dedicated management keypair NOW, at create
		// time: the public half is shown so the operator can authorize it on
		// the server (ssh-copy-id style) BEFORE the install — then the panel
		// connects with its own key and no operator password or private key
		// is ever needed.
		panelPub, panelPriv, panelFP, err := ssh.GenerateKeyPair("dockpal-" + instanceID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate panel SSH key"})
			return
		}
		encPanelKey, err := registry.Encrypt([]byte(panelPriv), cryptoKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt panel SSH key"})
			return
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
			SSHKeyEncrypted:     encPanelKey,
			SSHPublicKey:        panelPub,
			SSHKeyFingerprint:   panelFP,
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
			ID:                 instanceID,
			Name:               req.Name,
			Host:               req.Host,
			Port:               port,
			Mode:               req.Mode,
			Status:             "enrolling",
			CreatedAt:          instance.CreatedAt,
			InstallCommand:     installCmd,
			SSHPublicKey:       panelPub,
			SSHKeyFingerprint:  panelFP,
			SSHKeySetupCommand: generatePanelKeySetupCommand(panelPub),
		})
	}
}

// generatePanelKeySetupCommand builds the one-liner the operator runs ON the
// new server (directly, via provider console, or through cloud-init) to
// authorize the panel's public key — the ssh-copy-id equivalent.
func generatePanelKeySetupCommand(pubLine string) string {
	return fmt.Sprintf(`ssh user@your-server "mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '%s' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"`, pubLine)
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
				ID:                 inst.ID,
				Name:               inst.Name,
				Host:               inst.Host,
				Port:               inst.Port,
				Mode:               inst.Mode,
				Status:             inst.Status,
				LastSeen:           inst.LastSeen,
				SSHAuthType:        inst.SSHAuthType,
				SSHHardeningStatus: inst.SSHHardeningStatus,
				SSHHardenedAt:      inst.SSHHardenedAt,
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

		// The install command embeds the plaintext agent token, which is a bearer
		// credential for that instance's Docker API. Handing it to a read-only
		// viewer would let them escalate to full control of the remote host, so
		// only admins receive it. (Nothing in the SPA reads this field today.)
		var installCmd string
		if role, exists := c.Get("role"); exists && role == auth.RoleAdmin && len(inst.AgentTokenEncrypted) > 0 {
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
			ID:                 inst.ID,
			Name:               inst.Name,
			Host:               inst.Host,
			Port:               inst.Port,
			Mode:               inst.Mode,
			Status:             inst.Status,
			LastSeen:           inst.LastSeen,
			CreatedAt:          inst.CreatedAt,
			AgentVersion:       inst.AgentVersion,
			DockerVersion:      inst.DockerVersion,
			OS:                 inst.OS,
			CPUCores:           inst.CPUCores,
			TotalMemory:        inst.TotalMemory,
			InstallCommand:     installCmd,
			SSHAuthType:        inst.SSHAuthType,
			SSHHardeningStatus: inst.SSHHardeningStatus,
			SSHHardenedAt:      inst.SSHHardenedAt,
			SSHKeyFingerprint:  inst.SSHKeyFingerprint,
			SSHPublicKey:       inst.SSHPublicKey,
			SSHKeySetupCommand: generatePanelKeySetupCommand(inst.SSHPublicKey),
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
			ID:                 inst.ID,
			Name:               inst.Name,
			Host:               inst.Host,
			Port:               inst.Port,
			Mode:               inst.Mode,
			Status:             inst.Status,
			LastSeen:           inst.LastSeen,
			CreatedAt:          inst.CreatedAt,
			AgentVersion:       inst.AgentVersion,
			DockerVersion:      inst.DockerVersion,
			OS:                 inst.OS,
			CPUCores:           inst.CPUCores,
			TotalMemory:        inst.TotalMemory,
			InstallCommand:     installCmd,
			SSHAuthType:        inst.SSHAuthType,
			SSHHardeningStatus: inst.SSHHardeningStatus,
			SSHHardenedAt:      inst.SSHHardenedAt,
			SSHKeyFingerprint:  inst.SSHKeyFingerprint,
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
			"docker rm -f dockpal-agent 2>/dev/null || true\n"+
				"docker run -d --name dockpal-agent --restart unless-stopped \\\n  -e DOCKPAL_MODE=direct \\\n  -e DOCKPAL_TOKEN=%s \\\n  -p 9273:9273 \\\n  -v /var/run/docker.sock:/var/run/docker.sock \\\n  -v /opt/dockpal-agent:/opt/dockpal-agent \\\n  %s",
			token,
			agentImg,
		)
	case "edge":
		// For edge mode: agent builds the full WS URL from the scheme://host
		// base itself (appends /api/agent/connect + token). Passing the path
		// here double-appended it, making the handshake always 404.
		wsURL := fmt.Sprintf("wss://%s", serverHost)
		runCmd = fmt.Sprintf(
			"docker rm -f dockpal-agent 2>/dev/null || true\n"+
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
	SSHAuthType   string `json:"ssh_auth_type" binding:"required,oneof=password key panel_key"`
	// SSHSecret is the ad-hoc credential (password or pasted private key).
	// Mutually exclusive with SSHKeyID — exactly one must be set.
	SSHSecret     string `json:"ssh_secret"`
	// SSHKeyID references a saved SSH key (Settings → Administration → SSH
	// Keys); the handler resolves and decrypts it. Requires key auth.
	SSHKeyID      string `json:"ssh_key_id"`
	InstallDocker bool   `json:"install_docker"`
	// SSHHostKey pins the server's SSH host key as a SHA-256 fingerprint
	// ("SHA256:...") for strict verification (audit-auth L6). Empty = TOFU:
	// the presented fingerprint is printed in the install log for the admin
	// to verify and pin on a later install.
	SSHHostKey string `json:"ssh_host_key"`
	// PanelAddress overrides the address embedded in the agent's edge-mode
	// server URL. Defaults to the HTTP request's Host, which is wrong when
	// the panel is accessed via localhost/IP aliases that don't resolve from
	// the remote host ("localhost" on the VPS is the VPS itself).
	PanelAddress string `json:"panel_address"`
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
		// The stored plaintext is already the hex string that handleCreateInstance
		// hashed with bcrypt — hex-encoding it again would hand the agent a
		// 128-char token that can never match, breaking every edge install.
		token := string(tokenBytes)

		// Credential resolution by auth type:
		//   panel_key — the keypair the panel generated at create time; the
		//     operator already authorized its public half on the server. No
		//     secret travels in the request at all.
		//   key — an ad-hoc pasted private key or a legacy saved private key.
		//   password — bootstrap password.
		sshSecret := req.SSHSecret
		if req.SSHAuthType == "panel_key" {
			if req.SSHKeyID != "" || strings.TrimSpace(sshSecret) != "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "panel_key auth takes no ssh_secret or ssh_key_id"})
				return
			}
			if len(inst.SSHKeyEncrypted) == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "this instance has no panel key — create a new server or use password auth"})
				return
			}
			plain, derr := registry.Decrypt(inst.SSHKeyEncrypted, cryptoKey)
			if derr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt the panel SSH key"})
				return
			}
			sshSecret = string(plain)
		} else if req.SSHKeyID != "" {
			if req.SSHAuthType != "key" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "ssh_key_id requires key auth"})
				return
			}
			if strings.TrimSpace(sshSecret) != "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "provide either ssh_key_id or ssh_secret, not both"})
				return
			}
			resolved, rerr := resolveSSHKeySecret(database, cryptoKey, req.SSHKeyID)
			if rerr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": rerr.Error()})
				return
			}
			sshSecret = resolved
		}
		if strings.TrimSpace(sshSecret) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ssh_secret or ssh_key_id is required"})
			return
		}

		// Encrypt SSH Secret (password or key)
		encryptedSecret, err := registry.Encrypt([]byte(sshSecret), cryptoKey)
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
		switch req.SSHAuthType {
		case "panel_key":
			// The stored key IS the credential — keep it as-is and never
			// keep a password alongside it.
			inst.SSHAuthType = "key"
			inst.SSHPasswordEncrypted = nil
		case "key":
			inst.SSHAuthType = "key"
			inst.SSHKeyEncrypted = encryptedSecret
			inst.SSHPasswordEncrypted = nil
		default:
			inst.SSHAuthType = "password"
			inst.SSHPasswordEncrypted = encryptedSecret
			// The panel key generated at create time stays stored: hardening
			// installs exactly that key when disabling passwords, so the
			// instance keeps a single panel key for its whole life.
		}
		inst.Status = "enrolling"

		if err := database.SaveInstance(*inst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save instance details"})
			return
		}

		LogAudit(c, database, "instance.install_start", id, "success", fmt.Sprintf("Started agent installation on remote host %s:%d", req.SSHHost, req.SSHPort))

		// Clear/Reset logs for this session
		logsManager.RemoveSession(id)

		// Capture parameters for the background goroutine. The agent's edge
		// URL must use an address reachable FROM the remote host; the request
		// Host is only a default (wrong for localhost access — the browser's
		// host is not the panel's host from the VPS's perspective).
		host := req.PanelAddress
		if host == "" {
			host = c.Request.Host
		}
		isSecureWS := c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https"

		go func() {
			lw := &logWriter{instanceID: id, mgr: logsManager}
			logsManager.WriteLogf(id, "[Dockpal Installer] Initializing installation on remote host %s:%d...\n", req.SSHHost, req.SSHPort)

			agentImg := os.Getenv("DOCKPAL_AGENT_IMAGE")
			if agentImg == "" {
				agentImg = "ghcr.io/sdldev/dockpal-agent:latest"
			}

			// The installer only knows "password" and "key" — panel_key IS key
			// auth (the panel's own generated keypair resolved above).
			installAuthType := req.SSHAuthType
			if installAuthType == "panel_key" {
				installAuthType = "key"
			}
			params := ssh.InstallParams{
				Host:            req.SSHHost,
				Port:            req.SSHPort,
				User:            req.SSHUser,
				AuthType:        installAuthType,
				AuthSecret:      sshSecret,
				InstallDocker:   req.InstallDocker,
				Mode:            inst.Mode,
				Token:           token,
				ServerHost:      host,
				AgentImage:      agentImg,
				IsSecureWS:      isSecureWS,
				ExpectedHostKey: req.SSHHostKey,
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
		streamLogSession(c, logsManager, c.Param("instance_id"), "[Dockpal Installer]")
	}
}

// streamLogSession upgrades the request to a WebSocket and streams a named
// log session: full history first, then live lines with periodic pings until
// the client goes away or the session ends.
func streamLogSession(c *gin.Context, logsManager *InstallLogsManager, sessionKey, closePrefix string) {
	conn, err := installWebSocketUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade log stream WebSocket (session %s): %v", sessionKey, err)
		return
	}
	defer conn.Close()

	ch, history, deregister := logsManager.RegisterListener(sessionKey)
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
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				// Channel closed, session completed
				_ = conn.WriteMessage(websocket.TextMessage, []byte(closePrefix+" Session disconnected."))
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

// hardenSessionKey namespaces the hardening job's log session so it can never
// collide with an install session for the same instance.
func hardenSessionKey(instanceID string) string {
	return "harden-" + instanceID
}

// HardenSSHRequest carries optional hardening options.
type HardenSSHRequest struct {
	// SSHKeyID optionally selects a Saved SSH Key to install instead of the
	// default fresh keypair generated for this instance.
	SSHKeyID string `json:"ssh_key_id"`
	// ExtraPublicKeys are the operator's own authorized_keys lines (e.g. the
	// content of ~/.ssh/id_ed25519.pub on their PC). Without one, the
	// operator's own machine loses shell access once passwords are disabled —
	// only the panel's key remains. Each line must parse as a public key.
	ExtraPublicKeys []string `json:"extra_public_keys"`
	// ExtraKeyIDs references saved PUBLIC keys (Settings → Administration →
	// SSH Keys) to install alongside — the picker alternative to pasting.
	ExtraKeyIDs []string `json:"extra_key_ids"`
}

// handleHardenSSH starts the SSH hardening job: install a key, verify key
// login from a fresh connection, then disable password auth in sshd. Runs in
// the background; progress streams over /harden/logs like the installer.
func handleHardenSSH(database *db.DB, jwtSecret string, logsManager *InstallLogsManager, running *sync.Map) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")

		if id == "local" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the local instance is not managed over SSH"})
			return
		}

		var req HardenSSHRequest
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
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

		// The credential the panel currently holds for this instance — the
		// installer stored it encrypted when the server was added.
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive decryption key"})
			return
		}

		authType := "password"
		var authSecret string
		if len(inst.SSHKeyEncrypted) > 0 {
			plain, derr := registry.Decrypt(inst.SSHKeyEncrypted, cryptoKey)
			if derr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt stored SSH key"})
				return
			}
			authType = "key"
			authSecret = string(plain)
		} else if len(inst.SSHPasswordEncrypted) > 0 {
			plain, derr := registry.Decrypt(inst.SSHPasswordEncrypted, cryptoKey)
			if derr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt stored SSH password"})
				return
			}
			authSecret = string(plain)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no SSH credentials stored for this server — install the agent over SSH first"})
			return
		}

		// Where to connect: the SSH endpoint recorded during install, falling
		// back to the agent host for direct-mode instances.
		host := inst.SSHHost
		if host == "" && inst.Mode == "direct" {
			host = inst.Host
		}
		if host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no SSH host recorded for this server"})
			return
		}
		port := inst.SSHPort
		if port == 0 {
			port = 22
		}
		user := inst.SSHUser
		if user == "" {
			user = "root"
		}

		// The key that will remain on the server, in priority order:
		//   1. a chosen saved key,
		//   2. the key the panel already holds for this instance — either
		//      the credential it connects with (key auth) or the panel key
		//      generated at create time (password bootstrap). One key per
		//      instance for its whole life, no orphans,
		//   3. a fresh keypair (instances created before panel keys existed).
		privKeyPEM := authSecret
		var pubKey, fingerprint string
		if req.SSHKeyID != "" {
			resolved, rerr := resolveSSHKeySecret(database, cryptoKey, req.SSHKeyID)
			if rerr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": rerr.Error()})
				return
			}
			privKeyPEM = resolved
			pubKey, fingerprint, rerr = ssh.PublicKeyFromPrivate(privKeyPEM)
			if rerr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": rerr.Error()})
				return
			}
		} else if authType == "key" {
			// The credential we just connected with — already installed.
			pubKey, fingerprint, err = ssh.PublicKeyFromPrivate(privKeyPEM)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		} else if len(inst.SSHKeyEncrypted) > 0 && strings.TrimSpace(inst.SSHPublicKey) != "" {
			// Password bootstrap, but the panel key already exists: install it.
			plain, derr := registry.Decrypt(inst.SSHKeyEncrypted, cryptoKey)
			if derr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt the panel SSH key"})
				return
			}
			privKeyPEM = string(plain)
			pubKey = strings.TrimSpace(inst.SSHPublicKey)
			fingerprint = inst.SSHKeyFingerprint
		} else {
			pub, priv, fp, gerr := ssh.GenerateKeyPair("dockpal-" + id)
			if gerr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gerr.Error()})
				return
			}
			pubKey, privKeyPEM, fingerprint = pub, priv, fp
		}

		// Validate the operator's own public keys BEFORE taking the in-flight
		// slot: this returns synchronously, so a 400 here would otherwise leak
		// the slot and every later run would get a false 409.
		var extraKeys []string
		for _, raw := range req.ExtraPublicKeys {
			line := strings.TrimSpace(raw)
			if line == "" {
				continue
			}
			if err := ssh.ValidatePublicKeyLine(line); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid public key %q: %v", truncateForLog(line), err)})
				return
			}
			extraKeys = append(extraKeys, line)
		}
		for _, keyID := range req.ExtraKeyIDs {
			line, rerr := resolveSSHPublicLine(database, cryptoKey, keyID)
			if rerr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": rerr.Error()})
				return
			}
			extraKeys = append(extraKeys, line)
		}

		// One harden job per instance at a time — two concurrent runs would
		// interleave sshd rewrites and rollbacks.
		if _, loaded := running.LoadOrStore(id, struct{}{}); loaded {
			c.JSON(http.StatusConflict, gin.H{"error": "hardening is already in progress for this server"})
			return
		}

		sessionKey := hardenSessionKey(id)
		logsManager.RemoveSession(sessionKey)
		LogAudit(c, database, "instance.harden_ssh", id, "success", fmt.Sprintf("Started SSH hardening on %s:%d (user %s)", host, port, user))

		params := ssh.HardenParams{
			Host:            host,
			Port:            port,
			User:            user,
			AuthType:        authType,
			AuthSecret:      authSecret,
			PublicKey:       pubKey,
			PrivateKeyPEM:   privKeyPEM,
			ExtraPublicKeys: extraKeys,
			// The password that used to work — after the reload the panel
			// dials with it again and REQUIRES rejection.
			TestPassword: func() string {
				if authType == "password" {
					return authSecret
				}
				return ""
			}(),
		}

		go func() {
			defer running.Delete(id)
			defer logsManager.CompleteSession(sessionKey)
			lw := &logWriter{instanceID: sessionKey, mgr: logsManager}
			logsManager.WriteLogf(sessionKey, "[Dockpal Hardening] Initializing hardening on remote host %s:%d...\n", host, port)

			if err := ssh.HardenSSH(params, lw); err != nil {
				log.Printf("SSH hardening on instance %s failed: %v", id, err)
				logsManager.WriteLogf(sessionKey, "[Dockpal Hardening] Error: %v\n", err)
				return
			}

			// Persist the outcome: the panel now authenticates with the
			// installed key; the password no longer works, so it is dropped.
			instCopy, gerr := database.GetInstance(id)
			if gerr != nil {
				log.Printf("SSH hardening on instance %s succeeded but reloading the record failed: %v", id, gerr)
				return
			}
			encPriv, eerr := registry.Encrypt([]byte(privKeyPEM), cryptoKey)
			if eerr != nil {
				log.Printf("SSH hardening on instance %s: failed to encrypt the new key: %v", id, eerr)
				logsManager.WriteLogf(sessionKey, "[Dockpal Hardening] Warning: could not persist the new key (%v) — re-run hardening to repair the record.", eerr)
				return
			}
			instCopy.SSHAuthType = "key"
			instCopy.SSHKeyEncrypted = encPriv
			instCopy.SSHPasswordEncrypted = nil
			instCopy.SSHHardeningStatus = "hardened"
			instCopy.SSHHardenedAt = time.Now().Unix()
			instCopy.SSHKeyFingerprint = fingerprint
			if serr := database.SaveInstance(*instCopy); serr != nil {
				log.Printf("SSH hardening on instance %s: failed to save hardened state: %v", id, serr)
				logsManager.WriteLogf(sessionKey, "[Dockpal Hardening] Warning: could not save the hardened state (%v).", serr)
				return
			}
			log.Printf("SSH hardening on instance %s completed successfully", id)
			logsManager.WriteLog(sessionKey, "[Dockpal Hardening] Hardening completed successfully — password authentication disabled.")
		}()

		c.JSON(http.StatusAccepted, gin.H{"message": "hardening started", "session": sessionKey})
	}
}

func handleHardenSSHLogs(logsManager *InstallLogsManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		streamLogSession(c, logsManager, hardenSessionKey(c.Param("instance_id")), "[Dockpal Hardening]")
	}
}

// truncateForLog keeps validation error messages readable.
func truncateForLog(s string) string {
	if len(s) <= 40 {
		return s
	}
	return s[:37] + "..."
}
