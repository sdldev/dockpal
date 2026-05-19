package agent

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/registry"
)

// Manager maintains connections to all registered agents.
// It provides a uniform GetClient interface for route handlers.
type Manager struct {
	db        *db.DB
	cryptoKey []byte // For decrypting agent tokens (same key as registry encryption)
	mu        sync.RWMutex
	local     *LocalClient
	edge      map[string]*EdgeConnection // instance_id → active WebSocket
}

// EdgeConnection represents an active WebSocket connection from an edge-mode agent.
type EdgeConnection struct {
	instanceID string
	conn       *websocket.Conn
	pending    map[string]chan *AgentResponse // request_id → response channel
	mu         sync.Mutex
	done       chan struct{}
}

// NewManager creates a new Manager that maintains connections to all registered agents.
// The cryptoKey is derived from the JWT secret using the same derivation as registry encryption.
func NewManager(database *db.DB, localDocker *docker.Client, jwtSecret string) (*Manager, error) {
	cryptoKey, err := registry.DeriveKey(jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to derive crypto key: %w", err)
	}

	return &Manager{
		db:        database,
		cryptoKey: cryptoKey,
		local:     NewLocalClient(localDocker),
		edge:      make(map[string]*EdgeConnection),
	}, nil
}

// GetClient returns the appropriate AgentClient for an instance.
// Returns error if instance not found or offline (for edge mode without connection).
func (m *Manager) GetClient(instanceID string) (AgentClient, error) {
	// Special case: "local" always returns the LocalClient
	if instanceID == "local" {
		return m.local, nil
	}

	// Look up instance in database
	inst, err := m.db.GetInstance(instanceID)
	if err != nil {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}

	switch inst.Mode {
	case "direct":
		// Decrypt agent token from AES-256-GCM encrypted storage
		if len(inst.AgentTokenEncrypted) == 0 {
			return nil, fmt.Errorf("instance has no agent token")
		}
		token, err := registry.Decrypt(inst.AgentTokenEncrypted, m.cryptoKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt agent token: %w", err)
		}
		return NewDirectClient(instanceID, inst.Host, inst.Port, string(token)), nil

	case "edge":
		m.mu.RLock()
		_, connected := m.edge[instanceID]
		m.mu.RUnlock()
		if !connected {
			return nil, fmt.Errorf("instance offline: %s", instanceID)
		}
		return NewEdgeClient(instanceID, m), nil

	default:
		return nil, fmt.Errorf("unknown instance mode: %s", inst.Mode)
	}
}

// RegisterEdgeConnection stores a WebSocket connection for an edge-mode agent.
// Replaces any existing connection for the same instance.
func (m *Manager) RegisterEdgeConnection(instanceID string, conn *websocket.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Close existing connection if any
	if existing, ok := m.edge[instanceID]; ok {
		close(existing.done)
		existing.conn.Close()
	}

	ec := &EdgeConnection{
		instanceID: instanceID,
		conn:       conn,
		pending:    make(map[string]chan *AgentResponse),
		done:       make(chan struct{}),
	}
	m.edge[instanceID] = ec

	// Start read loop for this connection
	go m.edgeReadLoop(ec)
}

// UnregisterEdgeConnection removes an edge connection and marks instance offline in the database.
func (m *Manager) UnregisterEdgeConnection(instanceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ec, ok := m.edge[instanceID]; ok {
		close(ec.done)
		ec.conn.Close()
		delete(m.edge, instanceID)
	}

	// Update instance status to offline in database
	if m.db != nil {
		m.db.UpdateInstanceStatus(instanceID, "offline")
	}
}

// SendEdgeRequest sends a JSON request through an edge WebSocket and waits for response.
// Returns error if no response within 60 seconds.
func (m *Manager) SendEdgeRequest(instanceID string, req *AgentRequest) (*AgentResponse, error) {
	m.mu.RLock()
	ec, ok := m.edge[instanceID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no edge connection for instance: %s", instanceID)
	}

	// Create response channel
	respCh := make(chan *AgentResponse, 1)
	ec.mu.Lock()
	ec.pending[req.RequestID] = respCh
	ec.mu.Unlock()

	// Ensure cleanup of pending map on return
	defer func() {
		ec.mu.Lock()
		delete(ec.pending, req.RequestID)
		ec.mu.Unlock()
	}()

	// Send request as JSON
	ec.mu.Lock()
	err := ec.conn.WriteJSON(req)
	ec.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("failed to send edge request: %w", err)
	}

	// Wait for response with timeout
	select {
	case resp := <-respCh:
		return resp, nil
	case <-time.After(60 * time.Second):
		return nil, fmt.Errorf("edge request timeout for instance: %s", instanceID)
	case <-ec.done:
		return nil, fmt.Errorf("edge connection closed for instance: %s", instanceID)
	}
}

// edgeReadLoop reads messages from an edge WebSocket and routes them to pending requests.
// It handles disconnection by calling UnregisterEdgeConnection.
func (m *Manager) edgeReadLoop(ec *EdgeConnection) {
	defer m.UnregisterEdgeConnection(ec.instanceID)

	for {
		// Read JSON message from WebSocket
		var resp AgentResponse
		err := ec.conn.ReadJSON(&resp)
		if err != nil {
			// Connection closed or read error
			return
		}

		// Route response to pending request by request_id
		ec.mu.Lock()
		if ch, ok := ec.pending[resp.RequestID]; ok {
			select {
			case ch <- &resp:
			default:
				// Channel already received or closed, drop response
			}
		}
		ec.mu.Unlock()
	}
}

// Close shuts down all active edge connections.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, ec := range m.edge {
		close(ec.done)
		ec.conn.Close()
		delete(m.edge, id)
	}
}

// === Helper functions for decoding ===

// decodeBody is a helper to decode JSON body into a type.
func decodeBody[T any](body json.RawMessage) (*T, error) {
	if len(body) == 0 {
		var zero T
		return &zero, nil
	}
	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}