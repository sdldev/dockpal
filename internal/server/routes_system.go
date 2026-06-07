package server

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
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

