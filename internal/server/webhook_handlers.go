package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/git"
	"github.com/sdldev/dockpal/internal/registry"
)

// generateRandomToken generates a random hex string.
func generateRandomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
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
		token, _ := regMgr.GetTokenForDomain("github.com")

		// Clone repository
		info, err := git.Clone(wh.Repo, wh.Branch, token)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("git clone failed: %s", err.Error())})
			return
		}

		selectedFile := wh.ComposeFile
		if selectedFile == "" {
			if len(info.ComposeFiles) > 0 {
				selectedFile = info.ComposeFiles[0]
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": "no docker-compose file found in repository"})
				return
			}
		}

		composePath := filepath.Join(info.Path, selectedFile)
		composeData, err := os.ReadFile(composePath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to read compose file: %s", err.Error())})
			return
		}

		// Resolve registry auths from compose file using direct DB lookup
		var domains []string
		for _, line := range strings.Split(string(composeData), "\n") {
			if strings.Contains(line, "image:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					img := parts[1]
					if domain := registry.ExtractDomain(img); domain != "" {
						domains = append(domains, domain)
					}
				}
			}
		}
		registryAuths := resolveRegistryAuthsWithDB(database, wh.InstanceID, domains)

		// Deploy
		projectName := wh.Name
		if projectName == "" {
			projectName = filepath.Base(info.Path)
		}

		if err := client.DeployCompose(c.Request.Context(), projectName, string(composeData), registryAuths, false); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("deploy failed: %s", err.Error())})
			return
		}

		// Save/update service info in DB
		database.SaveService(db.Service{
			ID:         "svc-" + generateRandomToken()[:12],
			Name:       projectName,
			Type:       "git",
			Repo:       wh.Repo,
			InstanceID: wh.InstanceID,
			CreatedAt:  time.Now().Unix(),
		})

		c.JSON(http.StatusOK, gin.H{"status": "deployed", "project": projectName})
	}
}

// HandleListWebhooks retrieves all configured webhooks.
func HandleListWebhooks(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := database.ListWebhooks()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

		wh := db.Webhook{
			ID:          "wh-" + generateRandomToken()[:16],
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
