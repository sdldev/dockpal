package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"golang.org/x/crypto/bcrypt"
)

func TestAgentWebSocketConnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	agentAuthRateLimiter = NewRateLimiter()

	// Setup database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()

	// Seed edge instance
	token := "my-secret-agent-token-123"
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash token: %v", err)
	}

	inst := db.Instance{
		ID:             "edge-inst",
		Name:           "Edge Instance",
		Mode:           "edge",
		Status:         "offline",
		AgentTokenHash: string(tokenHash),
		CreatedAt:      time.Now().Unix(),
	}
	_ = database.SaveInstance(inst)

	jwtSecret := "test-secret-key-1234567890-abcdefg"
	t.Setenv("JWT_SECRET", jwtSecret)

	dockerClient, err := docker.NewClient()
	if err != nil {
		t.Fatalf("failed to create docker client: %v", err)
	}
	defer dockerClient.Close()

	agentMgr, err := agent.NewManager(database, dockerClient, jwtSecret)
	if err != nil {
		t.Fatalf("failed to create agent manager: %v", err)
	}

	router := gin.New()
	router.GET("/api/agent/connect", HandleAgentConnect(database, agentMgr))

	server := httptest.NewServer(router)
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/agent/connect"

	// Connect as client
	dialer := websocket.Dialer{}
	header := make(http.Header)
	header.Set("Origin", server.URL)
	conn, resp, err := dialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			t.Fatalf("failed to dial: %v, status: %d", err, resp.StatusCode)
		}
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// Send auth message
	authMsg := map[string]string{"token": token}
	if err := conn.WriteJSON(authMsg); err != nil {
		t.Fatalf("failed to write auth JSON: %v", err)
	}

	// Read the ping/request host-info request from server
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read from websocket: %v", err)
	}

	var req agent.AgentRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	// Send host info response
	hostInfo := hostInfoUpdate{
		DockerVersion: "20.10.7",
		OS:            "linux",
		CPUCores:      4,
		TotalMemory:   8589934592,
		AgentVersion:  "v0.8.0",
	}
	hostInfoBytes, _ := json.Marshal(hostInfo)
	respMsg := agent.AgentResponse{
		RequestID: req.RequestID,
		Status:    200,
		Body:      hostInfoBytes,
	}

	if err := conn.WriteJSON(respMsg); err != nil {
		t.Fatalf("failed to write response JSON: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		updatedInst, err := database.GetInstance("edge-inst")
		if err != nil {
			t.Fatalf("failed to get instance: %v", err)
		}
		if updatedInst.Status == "online" && updatedInst.DockerVersion == hostInfo.DockerVersion {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected status online with host info, got status=%s docker_version=%s", updatedInst.Status, updatedInst.DockerVersion)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
