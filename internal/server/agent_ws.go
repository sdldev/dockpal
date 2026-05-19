package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/db"

	"golang.org/x/crypto/bcrypt"
)

// agentMessage represents the initial authentication message from an edge agent.
type agentMessage struct {
	Token string `json:"token"`
}

// hostInfoUpdate represents host information sent by the agent after authentication.
type hostInfoUpdate struct {
	DockerVersion string `json:"docker_version"`
	OS            string `json:"os"`
	CPUCores      int    `json:"cpu_cores"`
	TotalMemory   int64  `json:"total_memory"`
	AgentVersion  string `json:"agent_version"`
}

// agentWebSocketUpgrader is configured with specific buffer sizes as per requirements.
var agentWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     checkOrigin,
}

// RegisterAgentRoutes adds the edge agent WebSocket endpoint.
func RegisterAgentRoutes(g *gin.RouterGroup, database *db.DB, agentMgr *agent.Manager) {
	g.GET("/agent/connect", HandleAgentConnect(database, agentMgr))
}

// HandleAgentConnect handles the WebSocket upgrade for edge-mode agents.
// It authenticates the agent via token and maintains the connection.
func HandleAgentConnect(database *db.DB, agentMgr *agent.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Upgrade to WebSocket
		conn, err := agentWebSocketUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("Failed to upgrade WebSocket: %v", err)
			return
		}
		defer conn.Close()

		// Set deadline for receiving authentication message (10 seconds)
		deadline := time.Now().Add(10 * time.Second)
		conn.SetReadDeadline(deadline)
		conn.SetWriteDeadline(deadline)

		// Read initial auth message
		var msg agentMessage
		_, rawMsg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Agent WebSocket: failed to read auth message: %v", err)
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "authentication timeout"))
			return
		}

		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			log.Printf("Agent WebSocket: invalid auth message format: %v", err)
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "authentication failed"))
			return
		}

		// Verify token against stored hashes
		instance, err := verifyAgentToken(database, msg.Token)
		if err != nil {
			log.Printf("Agent WebSocket: authentication failed for token: %v", err)
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "authentication failed"))
			return
		}

		// Authentication successful - register the connection
		agentMgr.RegisterEdgeConnection(instance.ID, conn)

		// Update instance status to "online"
		database.UpdateInstanceStatus(instance.ID, "online")

		// Update LastSeen timestamp
		database.UpdateInstanceLastSeen(instance.ID, time.Now().Unix())

		log.Printf("Agent WebSocket: agent %s (%s) connected successfully", instance.ID, instance.Name)

		// Request host info from the agent
		reqID := generateRequestID()
		agentMgr.SendEdgeRequest(instance.ID, &agent.AgentRequest{
			RequestID: reqID,
			Method:    "GET",
			Path:      "/agent/host-info",
		})

		// Handle incoming messages and keep connection alive
		// The connection stays open until the agent disconnects
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				// Connection closed or error - handle disconnect
				if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("Agent WebSocket: agent %s disconnected", instance.ID)
				} else {
					log.Printf("Agent WebSocket: error reading from agent %s: %v", instance.ID, err)
				}
				break
			}

			// Handle ping or other messages from agent
			var resp agent.AgentResponse
			if err := json.Unmarshal(message, &resp); err != nil {
				// Invalid message format - discard and continue per requirement 6.9
				continue
			}

			// If this is a host info response, update the instance record
			if resp.RequestID == reqID && len(resp.Body) > 0 {
				var hostInfo hostInfoUpdate
				if err := json.Unmarshal(resp.Body, &hostInfo); err == nil {
					database.UpdateInstanceInfo(instance.ID, db.Instance{
						DockerVersion: hostInfo.DockerVersion,
						OS:            hostInfo.OS,
						CPUCores:      hostInfo.CPUCores,
						TotalMemory:   hostInfo.TotalMemory,
					})
					if hostInfo.AgentVersion != "" {
						instance, _ := database.GetInstance(instance.ID)
						if instance != nil {
							instance.AgentVersion = hostInfo.AgentVersion
							database.SaveInstance(*instance)
						}
					}
					log.Printf("Agent WebSocket: updated host info for agent %s", instance.ID)
				}
			}

			// Handle any other messages as needed
			// Per requirement 6.5, we expect ping frames at least every 30 seconds
			// The connection handling continues here - if the agent sends a ping, we respond
		}

		// Agent disconnected - mark as offline and unregister
		database.UpdateInstanceStatus(instance.ID, "offline")
		agentMgr.UnregisterEdgeConnection(instance.ID)
		log.Printf("Agent WebSocket: agent %s marked as offline", instance.ID)
	}
}

// verifyAgentToken scans all instances for a matching bcrypt hash.
// It checks instances with mode "edge" or "direct" and status in "enrolling", "offline", "online".
func verifyAgentToken(database *db.DB, token string) (*db.Instance, error) {
	// Get all instances
	instances, err := database.ListInstances()
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	// Try to find a matching token hash
	for _, inst := range instances {
		// Skip instances without a token hash
		if inst.AgentTokenHash == "" {
			continue
		}

		// Skip non-edge/direct modes
		if inst.Mode != "edge" && inst.Mode != "direct" {
			continue
		}

		// Check status - allow enrolling, offline, or online
		if inst.Status != "enrolling" && inst.Status != "offline" && inst.Status != "online" {
			continue
		}

		// Compare token against bcrypt hash
		if err := bcrypt.CompareHashAndPassword([]byte(inst.AgentTokenHash), []byte(token)); err == nil {
			// Token matches
			return &inst, nil
		}
	}

	return nil, fmt.Errorf("no matching token found")
}

// generateRequestID creates a UUID v4-like request ID.
func generateRequestID() string {
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	if err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("req-%x", bytes)
}