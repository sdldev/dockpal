package agent

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/registry"
)

var (
	ErrInstanceNotFound = errors.New("instance not found")
	ErrInstanceOffline  = errors.New("instance offline")
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
		return nil, fmt.Errorf("%w: %s", ErrInstanceNotFound, instanceID)
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
			return nil, fmt.Errorf("%w: %s", ErrInstanceOffline, instanceID)
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
		select {
		case <-existing.done:
		default:
			close(existing.done)
		}
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

func (m *Manager) WaitForDisconnect(instanceID string) {
	m.mu.RLock()
	ec := m.edge[instanceID]
	m.mu.RUnlock()
	if ec == nil {
		return
	}
	<-ec.done
}

// UnregisterEdgeConnection removes an instance's edge connection and marks the
// instance offline. Used by the admin delete-instance path.
func (m *Manager) UnregisterEdgeConnection(instanceID string) {
	// Look up and tear down under a single m.mu hold so a reconnect landing in
	// the gap between the lookup and the teardown cannot leave the new
	// connection registered for an instance that no longer exists.
	m.mu.Lock()
	ec, ok := m.edge[instanceID]
	if ok && ec != nil {
		m.teardownEdgeConnectionLocked(ec)
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	if m.db != nil {
		m.db.UpdateInstanceStatus(instanceID, "offline")
	}
}

// unregisterEdgeConnection tears down a specific edge connection, but only when
// it is still the registered connection for its instance. This keeps a
// reconnect (which stores a fresh connection under the same instance ID) from
// being destroyed by the superseded connection's cleanup.
func (m *Manager) unregisterEdgeConnection(ec *EdgeConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.teardownEdgeConnectionLocked(ec)
}

// teardownEdgeConnectionLocked closes ec and removes it from the edge map when
// it is still the registered connection for its instance, and reflects the
// offline status in the database. Callers must hold m.mu.
func (m *Manager) teardownEdgeConnectionLocked(ec *EdgeConnection) {
	if current, ok := m.edge[ec.instanceID]; !ok || current != ec {
		// Not the live connection (e.g. it was superseded by a reconnect); its
		// successor must not be torn down or marked offline.
		return
	}

	select {
	case <-ec.done:
	default:
		close(ec.done)
	}
	ec.conn.Close()
	delete(m.edge, ec.instanceID)

	if m.db != nil {
		m.db.UpdateInstanceStatus(ec.instanceID, "offline")
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
// It handles disconnection by unregistering its own connection.
func (m *Manager) edgeReadLoop(ec *EdgeConnection) {
	// Key the cleanup on this connection, not on the instance ID: when the agent
	// reconnects, RegisterEdgeConnection supersedes this connection and stores a
	// new one under the same instance ID. Unregistering by ID would then tear
	// down the healthy new connection and flip the instance offline.
	defer m.unregisterEdgeConnection(ec)

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

// ListInstances returns all instances from the database
func (m *Manager) ListInstances() ([]db.Instance, error) {
	return m.db.ListInstances()
}

// WireLocalAppOps injects the auto-image-update dependencies into the
// manager's LocalClient. It is a thin pass-through to (*LocalClient).WireAppOps
// so callers (routes.go) do not need to reach into the manager's internals.
// Calling this with a zero-value LocalAppOps un-wires the app methods.
func (m *Manager) WireLocalAppOps(ops LocalAppOps) {
	if m == nil || m.local == nil {
		return
	}
	m.local.WireAppOps(ops)
}

// Close shuts down all active edge connections.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, ec := range m.edge {
		select {
		case <-ec.done:
		default:
			close(ec.done)
		}
		ec.conn.Close()
		delete(m.edge, id)
	}
}

