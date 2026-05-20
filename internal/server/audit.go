package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
)

// LogAudit writes a new audit log entry to the database.
// It automatically extracts username, role, and IP address from the gin.Context if available.
func LogAudit(c *gin.Context, database *db.DB, action, resource, status, details string) {
	// Generate random 8-byte ID (hex encoded, 16 chars)
	randBytes := make([]byte, 8)
	var id string
	if _, err := rand.Read(randBytes); err != nil {
		log.Printf("Error generating ID for audit log: %v", err)
		id = fmt.Sprintf("audit-%d", time.Now().UnixNano())
	} else {
		id = fmt.Sprintf("audit-%s", hex.EncodeToString(randBytes))
	}

	username, _ := c.Get("username")
	usernameStr, ok := username.(string)
	if !ok {
		usernameStr = "system"
	}

	role, _ := c.Get("role")
	roleStr, ok := role.(string)
	if !ok {
		roleStr = "unknown"
	}

	ipAddress := c.ClientIP()

	logEntry := db.AuditLog{
		ID:        id,
		Timestamp: time.Now().Unix(),
		Username:  usernameStr,
		UserRole:  roleStr,
		Action:    action,
		Resource:  resource,
		Status:    status,
		Details:   details,
		IPAddress: ipAddress,
	}

	if err := database.SaveAuditLog(logEntry); err != nil {
		log.Printf("Error saving audit log: %v", err)
	}
}

// handleListAuditLogs returns a paginated list of audit logs.
func handleListAuditLogs(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var query struct {
			Limit  int `form:"limit,default=50"`
			Offset int `form:"offset,default=0"`
		}
		if err := c.ShouldBindQuery(&query); err != nil {
			c.JSON(400, gin.H{"error": "invalid query parameters"})
			return
		}
		if query.Limit <= 0 || query.Limit > 100 {
			query.Limit = 50
		}
		if query.Offset < 0 {
			query.Offset = 0
		}

		logs, total, err := database.ListAuditLogs(query.Limit, query.Offset)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list audit logs"})
			return
		}

		// Ensure we return empty slice instead of null if logs is nil
		if logs == nil {
			logs = []db.AuditLog{}
		}

		c.JSON(200, gin.H{
			"logs":   logs,
			"total":  total,
			"limit":  query.Limit,
			"offset": query.Offset,
		})
	}
}
