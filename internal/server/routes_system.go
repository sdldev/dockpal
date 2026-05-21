package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/update"
)

// handleStatsStream streams real-time container resource stats over WebSocket.
// It sends JSON stats every 2 seconds and stops on client disconnect or error.
func handleStatsStream(c *gin.Context, agentMgr *agent.Manager) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	client, err := agentMgr.GetClient("local")
	if err != nil {
		conn.WriteJSON(gin.H{"error": "failed to get client"})
		return
	}

	containerID := c.Param("id")
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	detail, err := client.InspectContainer(ctx, containerID)
	if err != nil {
		conn.WriteJSON(gin.H{"error": "container not found"})
		return
	}
	if detail.State != "running" {
		conn.WriteJSON(gin.H{"error": "container is not running"})
		return
	}

	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	stats, err := client.GetContainerStats(ctx, containerID)
	if err != nil {
		conn.WriteJSON(gin.H{"error": "failed to get container stats"})
		return
	}
	if err := conn.WriteJSON(stats); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats, err := client.GetContainerStats(ctx, containerID)
			if err != nil {
				conn.WriteJSON(gin.H{"error": "failed to get container stats"})
				return
			}
			if err := conn.WriteJSON(stats); err != nil {
				return
			}
		}
	}
}

// HandleGetVersion handles the GET /api/system/version endpoint
func HandleGetVersion(c *gin.Context, versionService *update.VersionService) {
	if versionService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "version service not configured"})
		return
	}

	versionInfo, err := versionService.GetVersionInfo(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}

	c.JSON(http.StatusOK, versionInfo)
}

// HandleUpdate handles the POST /api/system/update endpoint
// It performs a streaming update with progress notifications
func HandleUpdate(c *gin.Context, updateService *update.UpdateService, database *db.DB) {
	if updateService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update service not configured"})
		return
	}

	var req struct {
		DownloadURL string `json:"downloadUrl" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "downloadUrl is required"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	emit := func(progress update.UpdateProgress) {
		data, _ := json.Marshal(progress)
		c.Writer.Write([]byte("data: "))
		c.Writer.Write(data)
		c.Writer.Write([]byte("\n\n"))
		c.Writer.Flush()
	}

	_ = updateService.RunUpdate(c.Request.Context(), req.DownloadURL, emit)
}
