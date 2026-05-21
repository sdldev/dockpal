package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
)

// HandleTriggerBackup creates a hot backup of the BBolt database.
// Only admin users may access this endpoint.
func HandleTriggerBackup(database *db.DB, dataDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Path string `json:"path,omitempty"`
		}
		if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		backupPath := req.Path
		if backupPath == "" {
			backupDir := filepath.Join(dataDir, "backups")
			timestamp := time.Now().Format("20060102-150405")
			backupPath = filepath.Join(backupDir, fmt.Sprintf("dockpal-%s.db", timestamp))
		}

		if err := database.BackupTo(backupPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("backup failed: %v", err)})
			return
		}

		info, err := os.Stat(backupPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stat backup: %v", err)})
			return
		}

		checksumPath := backupPath + ".sha256"
		checksumData, _ := os.ReadFile(checksumPath)
		checksum := string(checksumData)
		if len(checksum) > 0 && checksum[len(checksum)-1] == '\n' {
			checksum = checksum[:len(checksum)-1]
		}

		c.JSON(http.StatusOK, gin.H{
			"path":      backupPath,
			"checksum":  checksum,
			"size":      info.Size(),
			"timestamp": time.Now().Format(time.RFC3339),
		})
	}
}
