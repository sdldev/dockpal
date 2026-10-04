package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
// Unlike browser-facing WS endpoints, the agent connect endpoint must accept
// origin-less upgrades: non-browser clients (the dockpal-agent itself) do not
// send an Origin header, and browser-origin checks are meaningless here —
// the agent authenticates with its token in the first WebSocket message,
// enforced by HandleAgentConnect after the upgrade.
var agentWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

var agentAuthRateLimiter = NewRateLimiterWithPolicy(AgentRateLimit)

// HandleAgentConnect handles the WebSocket upgrade for edge-mode agents.
// It authenticates the agent via token and maintains the connection.
func HandleAgentConnect(database *db.DB, agentMgr *agent.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, retryAfter := agentAuthRateLimiter.Allow(c.ClientIP())
		if !allowed {
			c.Header("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
			c.JSON(429, gin.H{"error": "rate limit exceeded"})
			return
		}

		// Upgrade to WebSocket
		conn, err := agentWebSocketUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("Failed to upgrade WebSocket: %v", err)
			return
		}
		defer conn.Close()

		// Set deadline for receiving authentication message (10 seconds)
		deadline := time.Now().Add(10 * time.Second)
		if err := conn.SetReadDeadline(deadline); err != nil {
			log.Printf("Agent WebSocket: set read deadline: %v", err)
			return
		}
		if err := conn.SetWriteDeadline(deadline); err != nil {
			log.Printf("Agent WebSocket: set write deadline: %v", err)
			return
		}

		// Auth token: current agent images send it as ?token= on the WS URL;
		// older images send it as a {token} first message. Accept both. The
		// close-message writes below are best-effort notifications on a
		// handshake that is already failing (see handleDeployStreamWS).
		token := c.Query("token")
		if token == "" {
			var msg agentMessage
			_, rawMsg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Agent WebSocket: failed to read auth message: %v", err)
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "authentication timeout"))
				return
			}

			if err := json.Unmarshal(rawMsg, &msg); err != nil {
				log.Printf("Agent WebSocket: invalid auth message format: %v", err)
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "authentication failed"))
				return
			}
			token = msg.Token
		}

		// Verify token against stored hashes
		instance, err := verifyAgentToken(database, token)
		if err != nil {
			log.Printf("Agent WebSocket: authentication failed for token: %v", err)
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "authentication failed"))
			return
		}

		// Clearing the deadlines must succeed or the connection dies at the
		// auth deadline even though authentication passed.
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			log.Printf("Agent WebSocket: clear read deadline: %v", err)
			return
		}
		if err := conn.SetWriteDeadline(time.Time{}); err != nil {
			log.Printf("Agent WebSocket: clear write deadline: %v", err)
			return
		}

		// Authentication successful - register the connection
		agentMgr.RegisterEdgeConnection(instance.ID, conn)

		// Update instance status to "online"
		database.UpdateInstanceStatus(instance.ID, "online")

		// Update LastSeen timestamp
		database.UpdateInstanceLastSeen(instance.ID, time.Now().Unix())

		log.Printf("Agent WebSocket: agent %s (%s) connected successfully", instance.ID, instance.Name)

		// Request host info from the agent. Best-effort: the agent is already
		// authenticated and online, so a failed refresh only leaves stale
		// metadata that the next connect overwrites.
		reqID := generateRequestID()
		if resp, err := agentMgr.SendEdgeRequest(instance.ID, &agent.AgentRequest{
			RequestID: reqID,
			Method:    "GET",
			Path:      "/agent/host-info",
		}); err == nil && len(resp.Body) > 0 {
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

		agentMgr.WaitForDisconnect(instance.ID)
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
