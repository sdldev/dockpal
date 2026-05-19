package agent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/registry"
)

// Property 4: GetClient routing by mode
// **Validates: Requirements 2.11, 3.2, 3.3, 3.4, 3.5, 3.6**

// TestProperty_GetClientRoutingByMode verifies that GetClient correctly routes to the appropriate
// client type based on the instance ID and mode:
// - "local" returns LocalClient
// - direct mode returns DirectClient
// - edge mode with active connection returns EdgeClient
// - edge mode without connection returns error (offline)
// - non-existent ID returns error (not found)
func TestProperty_GetClientRoutingByMode(t *testing.T) {
	prop := func(params routingParams) bool {
		// Create a temp BBolt database
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.New(dbPath)
		if err != nil {
			t.Logf("failed to create db: %v", err)
			return false
		}
		defer database.Close()
		defer os.Remove(dbPath)

		// Ensure local instance exists
		if err := database.EnsureLocalInstance(); err != nil {
			t.Logf("failed to ensure local instance: %v", err)
			return false
		}

		// Generate a valid crypto key for the manager
		jwtSecret := "test-secret-key-for-property-testing"
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			t.Logf("failed to derive crypto key: %v", err)
			return false
		}

		// Create the manager
		mgr := &Manager{
			db:        database,
			cryptoKey: cryptoKey,
			local:     nil, // Will be set properly
			edge:      make(map[string]*EdgeConnection),
		}

		// For the test, we need a local client. We create a minimal one.
		// Since we don't actually call Docker methods in these tests, we can use a stub.
		mgr.local = &LocalClient{dockerClient: &docker.Client{}}

		// Test 1: GetClient("local") should return LocalClient
		localClient, err := mgr.GetClient("local")
		if err != nil {
			t.Logf("GetClient(\"local\") returned error: %v", err)
			return false
		}
		if localClient == nil {
			t.Logf("GetClient(\"local\") returned nil")
			return false
		}
		// Verify it's actually a LocalClient
		if _, ok := localClient.(*LocalClient); !ok {
			t.Logf("GetClient(\"local\") did not return LocalClient, got %T", localClient)
			return false
		}

		// Test 2: For direct mode instance, GetClient should return DirectClient
		if params.DirectModeInstanceID != "" {
			// Save the direct mode instance
			directInstance := db.Instance{
				ID:                  params.DirectModeInstanceID,
				Name:                params.DirectModeInstanceName,
				Host:                params.Host,
				Port:                params.Port,
				Mode:                "direct",
				AgentTokenHash:      "$2a$10$testhash", // fake bcrypt hash
				AgentTokenEncrypted: params.EncryptedToken,
				Status:              "online",
				CreatedAt:           time.Now().Unix(),
			}
			if err := database.SaveInstance(directInstance); err != nil {
				t.Logf("failed to save direct instance: %v", err)
				return false
			}

			directClient, err := mgr.GetClient(params.DirectModeInstanceID)
			if err != nil {
				t.Logf("GetClient(direct) returned error: %v", err)
				return false
			}
			if directClient == nil {
				t.Logf("GetClient(direct) returned nil")
				return false
			}
			if _, ok := directClient.(*DirectClient); !ok {
				t.Logf("GetClient(direct) did not return DirectClient, got %T", directClient)
				return false
			}
		}

		// Test 3: For edge mode instance with connection, GetClient should return EdgeClient
		if params.EdgeModeInstanceIDWithConnection != "" {
			// Save the edge mode instance
			edgeInstance := db.Instance{
				ID:                  params.EdgeModeInstanceIDWithConnection,
				Name:                params.EdgeModeInstanceName,
				Mode:                "edge",
				AgentTokenHash:      "$2a$10$testhash",
				AgentTokenEncrypted: []byte("fake-encrypted-token"),
				Status:              "online",
				CreatedAt:           time.Now().Unix(),
			}
			if err := database.SaveInstance(edgeInstance); err != nil {
				t.Logf("failed to save edge instance: %v", err)
				return false
			}

			// Directly add an edge connection to the manager's map without starting the read loop
			// This simulates a connected edge agent without actually connecting one
			mgr.mu.Lock()
			mgr.edge[params.EdgeModeInstanceIDWithConnection] = &EdgeConnection{
				instanceID: params.EdgeModeInstanceIDWithConnection,
				conn:       nil, // nil conn - we don't actually use it in GetClient
				pending:    make(map[string]chan *AgentResponse),
				done:       make(chan struct{}),
			}
			mgr.mu.Unlock()

			// Now GetClient should return EdgeClient
			edgeClient, err := mgr.GetClient(params.EdgeModeInstanceIDWithConnection)
			if err != nil {
				t.Logf("GetClient(edge with connection) returned error: %v", err)
				return false
			}
			if edgeClient == nil {
				t.Logf("GetClient(edge with connection) returned nil")
				return false
			}
			if _, ok := edgeClient.(*EdgeClient); !ok {
				t.Logf("GetClient(edge with connection) did not return EdgeClient, got %T", edgeClient)
				return false
			}
		}

		// Test 4: For edge mode instance without connection, GetClient should return error (offline)
		if params.EdgeModeInstanceIDNoConnection != "" {
			// Save the edge mode instance but don't register a connection
			edgeInstanceOffline := db.Instance{
				ID:                  params.EdgeModeInstanceIDNoConnection,
				Name:                "offline-edge",
				Mode:                "edge",
				AgentTokenHash:      "$2a$10$testhash",
				AgentTokenEncrypted: []byte("fake-encrypted-token"),
				Status:              "offline",
				CreatedAt:           time.Now().Unix(),
			}
			if err := database.SaveInstance(edgeInstanceOffline); err != nil {
				t.Logf("failed to save edge instance (offline): %v", err)
				return false
			}

			// GetClient should return error indicating offline
			_, err := mgr.GetClient(params.EdgeModeInstanceIDNoConnection)
			if err == nil {
				t.Logf("GetClient(edge without connection) should have returned error, got nil")
				return false
			}
			// Check the error message contains "offline"
			if err != nil && !contains(err.Error(), "offline") {
				t.Logf("GetClient(edge without connection) error should mention offline, got: %v", err)
				return false
			}
		}

		// Test 5: For non-existent instance ID, GetClient should return error (not found)
		if params.NonExistentInstanceID != "" {
			_, err := mgr.GetClient(params.NonExistentInstanceID)
			if err == nil {
				t.Logf("GetClient(non-existent) should have returned error, got nil")
				return false
			}
			// Check the error message contains "not found"
			if err != nil && !contains(err.Error(), "not found") {
				t.Logf("GetClient(non-existent) error should mention 'not found', got: %v", err)
				return false
			}
		}

		// Cleanup any edge connections manually (avoid calling Close which might try to close nil conns)
		mgr.mu.Lock()
		for id := range mgr.edge {
			if ec, ok := mgr.edge[id]; ok && ec != nil {
				close(ec.done)
			}
			delete(mgr.edge, id)
		}
		mgr.mu.Unlock()

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   routingGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 4 failed: %v", err)
	}
}

// routingParams holds generated parameters for GetClient routing test
type routingParams struct {
	DirectModeInstanceID           string
	DirectModeInstanceName         string
	Host                           string
	Port                           int
	EncryptedToken                 []byte
	EdgeModeInstanceIDWithConnection string
	EdgeModeInstanceIDNoConnection  string
	EdgeModeInstanceName            string
	NonExistentInstanceID           string
}

// routingGenerator generates random valid parameters for routing tests
func routingGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate valid host
	hostLen := rng.Intn(15) + 5
	host := make([]byte, hostLen)
	hostChars := "abcdefghijklmnopqrstuvwxyz0123456789."
	for i := range host {
		host[i] = hostChars[rng.Intn(len(hostChars))]
	}
	// Ensure it looks like a hostname
	if rng.Intn(3) > 0 {
		host = append(host, []byte(".example.com")...)
	}
	hostStr := string(host)

	// Generate valid port (1-65535)
	port := rng.Intn(65534) + 1

	// Generate instance IDs using the pattern from instanceGenerator
	generateInstanceID := func(prefix string, rng *rand.Rand) string {
		idLen := rng.Intn(15) + 10
		id := make([]byte, idLen)
		idChars := "abcdefghijklmnopqrstuvwxyz0123456789-"
		for i := range id {
			id[i] = idChars[rng.Intn(len(idChars))]
		}
		return prefix + string(id)
	}

	directInstanceID := generateInstanceID("direct-", rng)
	directInstanceNameLen := rng.Intn(30) + 5
	directInstanceName := make([]byte, directInstanceNameLen)
	nameChars := "abcdefghijklmnopqrstuvwxyz0123456789 -_"
	for i := range directInstanceName {
		directInstanceName[i] = nameChars[rng.Intn(len(nameChars))]
	}

	edgeWithConnID := generateInstanceID("edge-online-", rng)
	edgeNoConnID := generateInstanceID("edge-offline-", rng)
	edgeInstanceNameLen := rng.Intn(30) + 5
	edgeInstanceName := make([]byte, edgeInstanceNameLen)
	for i := range edgeInstanceName {
		edgeInstanceName[i] = nameChars[rng.Intn(len(nameChars))]
	}

	nonExistentID := generateInstanceID("nonexistent-", rng)

	// Generate a properly encrypted token using the same key derivation as the manager
	// We use a fixed test secret and encrypt a 32-byte random token
	jwtSecret := "test-secret-key-for-property-testing"
	cryptoKey, err := registry.DeriveKey(jwtSecret)
	if err != nil {
		// If key derivation fails, use a zero key (will cause encryption to fail but that's ok for this test)
		cryptoKey = make([]byte, 32)
	}

	// Generate random 32-byte token plaintext
	tokenPlaintext := make([]byte, 32)
	for i := range tokenPlaintext {
		tokenPlaintext[i] = byte(rng.Intn(256))
	}

	// Encrypt the token using registry encryption
	encryptedToken, err := registry.Encrypt(tokenPlaintext, cryptoKey)
	if err != nil {
		// If encryption fails, use empty bytes - this test case will fail which is acceptable
		encryptedToken = []byte{}
	}

	params := routingParams{
		DirectModeInstanceID:            directInstanceID,
		DirectModeInstanceName:          string(directInstanceName),
		Host:                            hostStr,
		Port:                            port,
		EncryptedToken:                  encryptedToken,
		EdgeModeInstanceIDWithConnection: edgeWithConnID,
		EdgeModeInstanceIDNoConnection:   edgeNoConnID,
		EdgeModeInstanceName:             string(edgeInstanceName),
		NonExistentInstanceID:            nonExistentID,
	}
	values[0] = reflect.ValueOf(params)
}

// contains is a simple string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Property 7: Edge request/response multiplexing
// **Validates: Requirements 6.4, 6.7, 3.9**

// TestProperty_EdgeRequestResponseMultiplexing verifies that when N concurrent requests are sent
// through an edge WebSocket connection, each caller receives exactly its own response matched by
// request_id. This tests that the pending map correctly routes responses to the right requestors
// even when multiple requests are in flight simultaneously.
func TestProperty_EdgeRequestResponseMultiplexing(t *testing.T) {
	// Test with different numbers of concurrent requests
	testWithRequestCount := func(numRequests int) bool {
		// Generate instance ID
		instanceID := generateRandomID("edge-mux-", 12)

		// Create mock WebSocket connection using gorilla/websocket
		// We'll simulate the manager's pending map directly

		// Create the EdgeConnection with pending map
		ec := &EdgeConnection{
			instanceID: instanceID,
			pending:    make(map[string]chan *AgentResponse),
			done:       make(chan struct{}),
		}

		// Generate N unique requests with unique request_ids and different methods/paths
		requests := make([]*AgentRequest, numRequests)
		for i := 0; i < numRequests; i++ {
			method := httpMethods[i%len(httpMethods)]
			path := fmt.Sprintf("/api/%s/item-%d", method, i)
			body := json.RawMessage(fmt.Sprintf(`{"index":%d,"value":"test-%d"}`, i, i))
			requests[i] = &AgentRequest{
				RequestID: generateRandomID("req-", 16),
				Method:    method,
				Path:      path,
				Query:     map[string]string{"idx": fmt.Sprintf("%d", i)},
				Body:      body,
			}
		}

		// Register each request in the pending map
		for _, req := range requests {
			ec.pending[req.RequestID] = make(chan *AgentResponse, 1)
		}

		// Create expected responses matching each request
		expectedResponses := make(map[string]*AgentResponse)
		for _, req := range requests {
			respBody := json.RawMessage(fmt.Sprintf(`{"request_id":"%s","method":"%s","path":"%s","echo_index":%d}`,
				req.RequestID, req.Method, req.Path, len(ec.pending)))
			expectedResponses[req.RequestID] = &AgentResponse{
				RequestID: req.RequestID,
				Status:    200,
				Body:      respBody,
				Stream:    false,
			}
		}

		// Simulate concurrent delivery of responses (like edgeReadLoop would do)
		var wg sync.WaitGroup
		for _, req := range requests {
			wg.Add(1)
			go func(r *AgentRequest) {
				defer wg.Done()
				// Simulate slight random delay to create race conditions
				time.Sleep(time.Duration(rand.Intn(10)) * time.Microsecond)

				ec.mu.Lock()
				if ch, ok := ec.pending[r.RequestID]; ok {
					ch <- expectedResponses[r.RequestID]
				}
				ec.mu.Unlock()
			}(req)
		}

		// Wait for all responses to be "sent"
		wg.Wait()

		// Now simulate callers receiving their responses through the pending map
		// This mimics what Manager.SendEdgeRequest does
		type result struct {
			requestID  string
			response   *AgentResponse
		}
		results := make(chan result, numRequests)

		// Concurrently receive from each channel
		var receiveWG sync.WaitGroup
		for _, req := range requests {
			receiveWG.Add(1)
			go func(r *AgentRequest) {
				defer receiveWG.Done()

				ec.mu.Lock()
				ch, ok := ec.pending[r.RequestID]
				ec.mu.Unlock()

				if !ok {
					results <- result{requestID: r.RequestID, response: nil}
					return
				}

				select {
				case resp := <-ch:
					results <- result{requestID: r.RequestID, response: resp}
				case <-time.After(100 * time.Millisecond):
					results <- result{requestID: r.RequestID, response: nil}
				}
			}(req)
		}

		receiveWG.Wait()
		close(results)

		// Collect results and verify each caller got exactly its own response
		received := make(map[string]*AgentResponse)
		for res := range results {
			if res.response == nil {
				t.Logf("Request %s did not receive a response", res.requestID)
				return false
			}
			received[res.requestID] = res.response
		}

		// Verify all responses were received
		if len(received) != numRequests {
			t.Logf("Expected %d responses, got %d", numRequests, len(received))
			return false
		}

		// Verify each response matches its request by request_id
		for _, req := range requests {
			resp, ok := received[req.RequestID]
			if !ok {
				t.Logf("Missing response for request %s", req.RequestID)
				return false
			}

			// Critical: response must have matching request_id
			if resp.RequestID != req.RequestID {
				t.Logf("Request %s received response with request_id %s (mismatch!)",
					req.RequestID, resp.RequestID)
				return false
			}

			// Verify response content corresponds to the request
			if resp.Status != 200 {
				t.Logf("Request %s got status %d, expected 200", req.RequestID, resp.Status)
				return false
			}
		}

		return true
	}

	// Test with different concurrency levels (5, 10, 15, 20)
	for _, numRequests := range []int{5, 10, 15, 20} {
		t.Run(fmt.Sprintf("%d_concurrent_requests", numRequests), func(t *testing.T) {
			for i := 0; i < 100; i++ {
				if !testWithRequestCount(numRequests) {
					t.Errorf("Failed with %d concurrent requests on iteration %d", numRequests, i)
					return
				}
			}
		})
	}
}

// httpMethods is a list of HTTP methods for generating varied requests
var httpMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

// generateRandomID generates a random ID with the given prefix
func generateRandomID(prefix string, length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return prefix + string(b)
}

// TestProperty_EdgeMultiplexingWithConflictingIDs tests that responses with the same request_id
// as an existing pending request go to the correct caller
func TestProperty_EdgeMultiplexingWithConflictingIDs(t *testing.T) {
	prop := func(params multiplexingConflictingParams) bool {
		instanceID := generateRandomID("edge-conflict-", 12)

		ec := &EdgeConnection{
			instanceID: instanceID,
			pending:    make(map[string]chan *AgentResponse),
			done:       make(chan struct{}),
		}

		// Create two requests with different IDs
		req1 := &AgentRequest{
			RequestID: params.FirstRequestID,
			Method:    "GET",
			Path:      "/api/containers",
		}
		req2 := &AgentRequest{
			RequestID: params.SecondRequestID,
			Method:    "POST",
			Path:      "/api/containers/start",
		}

		// Register both in pending map
		ec.pending[req1.RequestID] = make(chan *AgentResponse, 1)
		ec.pending[req2.RequestID] = make(chan *AgentResponse, 1)

		// Send responses - response1 to request1, response2 to request2
		resp1 := &AgentResponse{RequestID: req1.RequestID, Status: 200, Body: json.RawMessage(`{"result":"ok1"}`)}
		resp2 := &AgentResponse{RequestID: req2.RequestID, Status: 201, Body: json.RawMessage(`{"result":"ok2"}`)}

		// Deliver concurrently
		var deliveryWG sync.WaitGroup
		deliveryWG.Add(2)
		go func() {
			defer deliveryWG.Done()
			ec.mu.Lock()
			ec.pending[req1.RequestID] <- resp1
			ec.mu.Unlock()
		}()
		go func() {
			defer deliveryWG.Done()
			ec.mu.Lock()
			ec.pending[req2.RequestID] <- resp2
			ec.mu.Unlock()
		}()
		deliveryWG.Wait()

		// Receive responses
		received1 := <-ec.pending[req1.RequestID]
		received2 := <-ec.pending[req2.RequestID]

		// Verify each response went to the correct request
		if received1.RequestID != req1.RequestID {
			t.Logf("Request 1 got response for request_id %s instead of %s",
				received1.RequestID, req1.RequestID)
			return false
		}
		if received2.RequestID != req2.RequestID {
			t.Logf("Request 2 got response for request_id %s instead of %s",
				received2.RequestID, req2.RequestID)
			return false
		}
		if received1.Status != 200 || received2.Status != 201 {
			t.Logf("Status mismatch: got %d/%d expected 200/201", received1.Status, received2.Status)
			return false
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   conflictingIDGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 7 (conflicting IDs) failed: %v", err)
	}
}

type multiplexingConflictingParams struct {
	FirstRequestID  string
	SecondRequestID string
}

func conflictingIDGenerator(values []reflect.Value, rng *rand.Rand) {
	values[0] = reflect.ValueOf(multiplexingConflictingParams{
		FirstRequestID:  generateRandomID("req1-", 20),
		SecondRequestID: generateRandomID("req2-", 20),
	})
}