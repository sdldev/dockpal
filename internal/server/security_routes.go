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
	"strings"
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

// SecurityApplyRequest is the desired end state of all three controls (the
// UI's toggle model: set the switches, then Apply once) plus the operator's
// own public keys to keep on the server.
type SecurityApplyRequest struct {
	PasswordAuth *bool `json:"password_auth" binding:"required"`
	RootLogin    *bool `json:"root_login" binding:"required"`
	Fail2ban     *bool `json:"fail2ban" binding:"required"`
	// ExtraPublicKeys are the operator's own authorized_keys lines — without
	// at least one (or the panel key), their own machine loses shell access
	// once passwords are disabled.
	ExtraPublicKeys []string `json:"extra_public_keys"`
	// ExtraKeyIDs reference saved PUBLIC keys (Settings → Administration →
	// SSH Keys) — the picker alternative to pasting.
	ExtraKeyIDs []string `json:"extra_key_ids"`
}

// handleApplySecurity starts a background job converging the server to the
// requested state. Shares the in-flight map with hardening: two concurrent
// sshd-rewriting jobs on one instance would interleave writes and rollbacks.
func handleApplySecurity(database *db.DB, jwtSecret string, logsManager *InstallLogsManager, running *sync.Map) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")
		if id == "local" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the local instance is not managed over SSH"})
			return
		}
		var req SecurityApplyRequest
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

		// The panel key that stays on the server: prefer the key generated at
		// create time; without one (legacy instances) generate one now.
		panelPriv := ""
		panelPub := strings.TrimSpace(inst.SSHPublicKey)
		panelFP := inst.SSHKeyFingerprint
		if len(inst.SSHKeyEncrypted) > 0 {
			plain, derr := registry.Decrypt(inst.SSHKeyEncrypted, cryptoKey)
			if derr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt the panel SSH key"})
				return
			}
			panelPriv = string(plain)
			if panelPub == "" {
				pub, _, derr := ssh.PublicKeyFromPrivate(panelPriv)
				if derr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive the panel public key"})
					return
				}
				panelPub = pub
			}
		} else {
			pub, priv, fp, gerr := ssh.GenerateKeyPair("dockpal-" + id)
			if gerr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gerr.Error()})
				return
			}
			panelPub, panelPriv, panelFP = pub, priv, fp
		}

		// Disabling password auth can be verified end-to-end when the panel
		// still holds the old password.
		testPassword := ""
		if !*req.PasswordAuth && len(inst.SSHPasswordEncrypted) > 0 {
			if plain, derr := registry.Decrypt(inst.SSHPasswordEncrypted, cryptoKey); derr == nil {
				testPassword = string(plain)
			}
		}

		// Validate the operator's own public keys BEFORE taking the in-flight
		// slot: a synchronous 400 must not leak the slot (a leaked slot made
		// every later run return a false 409).
		publicKeys := []string{panelPub}
		for _, raw := range req.ExtraPublicKeys {
			line := strings.TrimSpace(raw)
			if line == "" {
				continue
			}
			if err := ssh.ValidatePublicKeyLine(line); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid public key %q: %v", truncateForLog(line), err)})
				return
			}
			publicKeys = append(publicKeys, line)
		}
		for _, keyID := range req.ExtraKeyIDs {
			line, rerr := resolveSSHPublicLine(database, cryptoKey, keyID)
			if rerr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": rerr.Error()})
				return
			}
			publicKeys = append(publicKeys, line)
		}

		if _, loaded := running.LoadOrStore(id, struct{}{}); loaded {
			c.JSON(http.StatusConflict, gin.H{"error": "a security or hardening job is already in progress for this server"})
			return
		}

		sessionKey := securitySessionKey(id)
		logsManager.RemoveSession(sessionKey)
		LogAudit(c, database, "instance.security", id, "success",
			fmt.Sprintf("Security apply: password_auth=%t root_login=%t fail2ban=%t on %s:%d", *req.PasswordAuth, *req.RootLogin, *req.Fail2ban, host, port))

		update := ssh.SecurityUpdate{
			PasswordAuth: *req.PasswordAuth,
			RootLogin:    *req.RootLogin,
			Fail2ban:     *req.Fail2ban,
			PanelKeyPEM:  panelPriv,
			PublicKeys:   publicKeys,
			TestPassword: testPassword,
		}

		go func() {
			defer running.Delete(id)
			defer logsManager.CompleteSession(sessionKey)
			lw := &logWriter{instanceID: sessionKey, mgr: logsManager}
			logsManager.WriteLogf(sessionKey, "[Dockpal Security] Applying desired state (password_auth=%t root_login=%t fail2ban=%t) on %s:%d...\n", *req.PasswordAuth, *req.RootLogin, *req.Fail2ban, host, port)

			if err := ssh.ApplySecurity(host, port, user, authType, secret, "", update, lw); err != nil {
				log.Printf("Security update on instance %s failed: %v", id, err)
				logsManager.WriteLogf(sessionKey, "[Dockpal Security] Error: %v\n", err)
				return
			}
			// Persist the outcome: panel keeps its key, the password is gone
			// once passwords are disabled, and the badge reads "hardened".
			instCopy, gerr := database.GetInstance(id)
			if gerr == nil {
				encPriv, eerr := registry.Encrypt([]byte(panelPriv), cryptoKey)
				if eerr == nil {
					instCopy.SSHKeyEncrypted = encPriv
					instCopy.SSHPublicKey = panelPub
					instCopy.SSHKeyFingerprint = panelFP
					if !*req.PasswordAuth {
						instCopy.SSHAuthType = "key"
						instCopy.SSHPasswordEncrypted = nil
					}
					if !*req.PasswordAuth {
						instCopy.SSHHardeningStatus = "hardened"
						instCopy.SSHHardenedAt = time.Now().Unix()
					}
					if serr := database.SaveInstance(*instCopy); serr != nil {
						log.Printf("Security apply on instance %s: persisting state failed: %v", id, serr)
					}
				}
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

// truncateForLog keeps validation error messages readable.
func truncateForLog(s string) string {
	if len(s) <= 40 {
		return s
	}
	return s[:37] + "..."
}

func handleSecurityLogs(logsManager *InstallLogsManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		streamLogSession(c, logsManager, securitySessionKey(c.Param("instance_id")), "[Dockpal Security]")
	}
}
