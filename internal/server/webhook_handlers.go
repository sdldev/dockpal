package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
)

// generateRandomToken generates a random hex string.
func generateRandomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// hmacSha256 computes HMAC-SHA256 signature.
func hmacSha256(message, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

type webhookResponse struct {
	ID          string `json:"id"`
	InstanceID  string `json:"instance_id"`
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	ComposeFile string `json:"compose_file"`
	HasSecret   bool   `json:"has_secret"`
	CreatedAt   int64  `json:"created_at"`
}

func sanitizeWebhook(wh db.Webhook) webhookResponse {
	return webhookResponse{
		ID:          wh.ID,
		InstanceID:  wh.InstanceID,
		Name:        wh.Name,
		Repo:        wh.Repo,
		Branch:      wh.Branch,
		ComposeFile: wh.ComposeFile,
		HasSecret:   wh.Secret != "",
		CreatedAt:   wh.CreatedAt,
	}
}

// HandleWebhookDeploy processes incoming Git webhook triggers and deploys on remote agent.
func HandleWebhookDeploy(database *db.DB, agentMgr *agent.Manager, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("webhook_id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "webhook ID is required"})
			return
		}

		wh, err := database.GetWebhook(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
			return
		}

		// Read body for signature verification
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
			return
		}
		// Restore body
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Signature verification if secret is configured
		if wh.Secret != "" {
			sig := c.GetHeader("X-Hub-Signature-256")
			if sig == "" {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "signature required"})
				return
			}
			const prefix = "sha256="
			if !strings.HasPrefix(sig, prefix) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature prefix"})
				return
			}
			hexSig := sig[len(prefix):]
			expected := hmacSha256(bodyBytes, []byte(wh.Secret))
			if !hmac.Equal([]byte(hexSig), []byte(expected)) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
				return
			}
		}

		// Get remote agent client
		client, err := agentMgr.GetClient(wh.InstanceID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent instance not connected"})
			return
		}

		// Resolve git credentials
		regMgr := registry.NewManager(database, jwtSecret)
		token, err := regMgr.GetTokenForDomain("github.com")
		if err != nil {
			// Stored credentials exist but are unreadable; cloning without
			// them would fail later with a misleading "not found".
			internalError(c, err)
			return
		}

		// Clone repository and read the stored compose file. Webhooks redeploy
		// non-interactively: pick the first compose file when none is stored.
		prep := prepareGitDeploy(c, gitDeployOptions{
			Repo:             wh.Repo,
			Branch:           wh.Branch,
			ComposeFile:      wh.ComposeFile,
			Name:             wh.Name,
			Token:            token,
			AutoSelect:       true,
			TrustComposeFile: true,
		})
		if prep == nil {
			return
		}
		// Webhook redeploys pull a fresh compose from git; normalize so a
		// reboot-unsafe restart policy in the repo does not silently disable
		// auto-start after a host reboot.
		composeYAML := ensureAutoStart(prep.ComposeData, "", nil)

		// Resolve registry auths from compose file using direct DB lookup
		registryAuths := resolveRegistryAuthsWithDB(database, wh.InstanceID, extractDomainsFromCompose(composeYAML))

		// Deploy
		projectName := prep.ProjectName

		if err := client.DeployCompose(c.Request.Context(), projectName, composeYAML, registryAuths, false); err != nil {
			internalError(c, err)
			return
		}

		// Save/update service info in DB
		svcToken, err := generateRandomToken()
		if err != nil {
			log.Printf("webhook deploy: skip service record for %s: %v", projectName, err)
		} else {
			database.SaveService(db.Service{
				ID:         "svc-" + svcToken[:12],
				Name:       projectName,
				Type:       "git",
				Repo:       wh.Repo,
				InstanceID: wh.InstanceID,
				CreatedAt:  time.Now().Unix(),
			})
		}

		c.JSON(http.StatusOK, gin.H{"status": "deployed", "project": projectName})
	}
}

// HandleListWebhooks retrieves all configured webhooks.
func HandleListWebhooks(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := database.ListWebhooks()
		if err != nil {
			internalError(c, err)
			return
		}
		response := make([]webhookResponse, 0, len(list))
		for _, wh := range list {
			response = append(response, sanitizeWebhook(wh))
		}
		c.JSON(http.StatusOK, response)
	}
}

// HandleCreateWebhook registers a new webhook.
func HandleCreateWebhook(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			InstanceID  string `json:"instance_id" binding:"required"`
			Name        string `json:"name" binding:"required"`
			Repo        string `json:"repo" binding:"required"`
			Branch      string `json:"branch"`
			ComposeFile string `json:"compose_file"`
			Secret      string `json:"secret"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
			return
		}

		// A webhook without a secret accepts unsigned triggers — anyone who
		// sees the URL (CI configs, browser history, logs) can force a deploy
		// (audit-auth M2). Generate one server-side when the caller leaves it
		// empty so every webhook is HMAC-protected by default.
		if req.Secret == "" {
			secret, err := generateRandomToken()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate webhook secret"})
				return
			}
			req.Secret = secret
		}

		whToken, err := generateRandomToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate webhook ID"})
			return
		}
		wh := db.Webhook{
			ID:          "wh-" + whToken[:16],
			InstanceID:  req.InstanceID,
			Name:        req.Name,
			Repo:        req.Repo,
			Branch:      req.Branch,
			ComposeFile: req.ComposeFile,
			Secret:      req.Secret,
			CreatedAt:   time.Now().Unix(),
		}

		if err := database.CreateWebhook(wh); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save webhook"})
			return
		}

		c.JSON(http.StatusOK, sanitizeWebhook(wh))
	}
}

// HandleDeleteWebhook removes a webhook.
func HandleDeleteWebhook(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("webhook_id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "webhook ID is required"})
			return
		}

		if err := database.DeleteWebhook(id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}
