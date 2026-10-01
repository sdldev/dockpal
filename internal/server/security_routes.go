package server

// Per-server security controls (Vito parity): detect the effective sshd
// state (sshd -T), toggle password authentication / root login, manage
// fail2ban. Detection is a read over the panel's stored SSH credential;
// changes run as background jobs streaming to /security/logs like harden.

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
	"github.com/sdldev/dockpal/internal/ssh"
)

// securitySessionKey namespaces the security job's log session.
func securitySessionKey(instanceID string) string {
	return "security-" + instanceID
}

// resolveInstanceSSHCreds decrypts the credential the panel holds for this
// instance ("key" or "password") — shared by harden and security flows.
func resolveInstanceSSHCreds(cryptoKey []byte, inst *db.Instance) (string, string, error) {
	if len(inst.SSHKeyEncrypted) > 0 {
		plain, err := registry.Decrypt(inst.SSHKeyEncrypted, cryptoKey)
		if err != nil {
			return "", "", fmt.Errorf("failed to decrypt stored SSH key")
		}
		return "key", string(plain), nil
	}
	if len(inst.SSHPasswordEncrypted) > 0 {
		plain, err := registry.Decrypt(inst.SSHPasswordEncrypted, cryptoKey)
		if err != nil {
			return "", "", fmt.Errorf("failed to decrypt stored SSH password")
		}
		return "password", string(plain), nil
	}
	return "", "", errors.New("no SSH credentials stored for this server — install the agent over SSH first")
}

// resolveInstanceSSHTarget returns where to connect (SSH endpoint recorded
// during install, falling back to the agent host for direct instances).
func resolveInstanceSSHTarget(inst *db.Instance) (string, int, string, error) {
	host := inst.SSHHost
	if host == "" && inst.Mode == "direct" {
		host = inst.Host
	}
	if host == "" {
		return "", 0, "", errors.New("no SSH host recorded for this server")
	}
	port := inst.SSHPort
	if port == 0 {
		port = 22
	}
	user := inst.SSHUser
	if user == "" {
		user = "root"
	}
	return host, port, user, nil
}

// persistSecurityState caches the detected state on the instance record so
// the Servers-table badge reflects reality between checks.
func persistSecurityState(database *db.DB, id string, state ssh.SecurityState) {
	inst, err := database.GetInstance(id)
	if err != nil {
		return
	}
	inst.SecPasswordAuth = state.PasswordAuth
	inst.SecRootLogin = state.RootLogin
	inst.SecFail2ban = state.Fail2ban
	inst.SecCheckedAt = time.Now().Unix()
	_ = database.SaveInstance(*inst)
}

// handleDetectSecurity runs a live detection pass (synchronous — one SSH
// connection, a couple of commands) and returns the effective state.
func handleDetectSecurity(database *db.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")
		if id == "local" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the local instance is not managed over SSH"})
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
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive decryption key"})
			return
		}
		authType, secret, err := resolveInstanceSSHCreds(cryptoKey, inst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		host, port, user, err := resolveInstanceSSHTarget(inst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		state, derr := ssh.DetectSecurity(host, port, user, authType, secret, "", io.Discard)
		if derr != nil {
			// Connection-level failure: nothing detected, say so.
			persistSecurityState(database, id, ssh.SecurityState{
				PasswordAuth: "unknown", RootLogin: "unknown", Fail2ban: "unknown",
			})
			c.JSON(http.StatusOK, gin.H{"security": state, "error": derr.Error()})
			return
		}
		persistSecurityState(database, id, state)
		c.JSON(http.StatusOK, gin.H{"security": state})
	}
}

// SecurityControlRequest asks for one control change.
type SecurityControlRequest struct {
	Control string `json:"control" binding:"required,oneof=password_auth root_login fail2ban"`
	Enabled *bool  `json:"enabled" binding:"required"`
}

// handleApplySecurity starts a background job applying one control change.
// Shares the in-flight map with hardening: two concurrent sshd-rewriting
// jobs on one instance would interleave writes and rollbacks.
func handleApplySecurity(database *db.DB, jwtSecret string, logsManager *InstallLogsManager, running *sync.Map) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")
		if id == "local" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the local instance is not managed over SSH"})
			return
		}
		var req SecurityControlRequest
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
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive decryption key"})
			return
		}
		authType, secret, err := resolveInstanceSSHCreds(cryptoKey, inst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		host, port, user, err := resolveInstanceSSHTarget(inst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Disabling password auth can be verified end-to-end when the panel
		// still holds the old password.
		testPassword := ""
		if req.Control == "password_auth" && !*req.Enabled && len(inst.SSHPasswordEncrypted) > 0 {
			if plain, derr := registry.Decrypt(inst.SSHPasswordEncrypted, cryptoKey); derr == nil {
				testPassword = string(plain)
			}
		}

		if _, loaded := running.LoadOrStore(id, struct{}{}); loaded {
			c.JSON(http.StatusConflict, gin.H{"error": "a security or hardening job is already in progress for this server"})
			return
		}

		sessionKey := securitySessionKey(id)
		logsManager.RemoveSession(sessionKey)
		LogAudit(c, database, "instance.security", id, "success",
			fmt.Sprintf("Security change: control=%s enabled=%t on %s:%d", req.Control, *req.Enabled, host, port))

		update := ssh.SecurityUpdate{
			Control:      req.Control,
			Enabled:      *req.Enabled,
			TestPassword: testPassword,
		}

		go func() {
			defer running.Delete(id)
			defer logsManager.CompleteSession(sessionKey)
			lw := &logWriter{instanceID: sessionKey, mgr: logsManager}
			logsManager.WriteLogf(sessionKey, "[Dockpal Security] Applying %s (enabled=%t) on %s:%d...\n", req.Control, *req.Enabled, host, port)

			if err := ssh.ApplySecurity(host, port, user, authType, secret, "", update, lw); err != nil {
				log.Printf("Security update on instance %s failed: %v", id, err)
				logsManager.WriteLogf(sessionKey, "[Dockpal Security] Error: %v\n", err)
				return
			}
			// Refresh the cached state from the server after the change.
			if state, derr := ssh.DetectSecurity(host, port, user, authType, secret, "", lw); derr == nil {
				persistSecurityState(database, id, state)
			}
			log.Printf("Security update on instance %s completed", id)
			logsManager.WriteLog(sessionKey, "[Dockpal Security] Update completed successfully.")
		}()

		c.JSON(http.StatusAccepted, gin.H{"message": "security update started", "session": sessionKey})
	}
}

func handleSecurityLogs(logsManager *InstallLogsManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		streamLogSession(c, logsManager, securitySessionKey(c.Param("instance_id")), "[Dockpal Security]")
	}
}
