package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
)

type createAPIKeyRequest struct {
	Name string `json:"name" binding:"required"`
	Role string `json:"role" binding:"required"`
}

func handleListAPIKeys(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		keys, err := database.ListAPIKeys()
		if err != nil {
			internalError(c, err)
			return
		}
		type response struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Role      string `json:"role"`
			CreatedAt int64  `json:"created_at"`
		}
		out := make([]response, 0, len(keys))
		for _, key := range keys {
			out = append(out, response{ID: key.ID, Name: key.Name, Role: key.Role, CreatedAt: key.CreatedAt})
		}
		c.JSON(http.StatusOK, out)
	}
}

func handleCreateAPIKey(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createAPIKeyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if !auth.HasRole(req.Role, auth.RoleViewer) || req.Role == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
			return
		}
		secret, err := randomHex(32)
		if err != nil {
			internalError(c, err)
			return
		}
		id := generateID("api-key")
		key := db.APIKey{
			ID:        id,
			Name:      req.Name,
			KeyHash:   hashAPIKey(secret),
			Role:      req.Role,
			CreatedAt: time.Now().Unix(),
		}
		if err := database.SaveAPIKey(key); err != nil {
			internalError(c, err)
			return
		}
		LogAudit(c, database, "api_key.create", id, "success", "Created API key")
		c.JSON(http.StatusCreated, gin.H{"id": id, "name": req.Name, "role": req.Role, "key": secret, "created_at": key.CreatedAt})
	}
}

func handleDeleteAPIKey(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing api key id"})
			return
		}
		if err := database.DeleteAPIKey(id); err != nil {
			internalError(c, err)
			return
		}
		LogAudit(c, database, "api_key.delete", id, "success", "Deleted API key")
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

func randomHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
