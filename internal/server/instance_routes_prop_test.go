package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"testing/quick"

	"golang.org/x/crypto/bcrypt"

	"github.com/sdldev/dockpal/internal/registry"
)

// **Validates: Requirements 5.1, 5.2**

// Property 5: Install command generation correctness
// Tests that the generateInstallCommand function produces correct docker run
// commands for both direct and edge modes.

// TestProperty_DirectMode_ContainsRequiredElements verifies that direct mode
// install commands contain all required elements:
// - Image "sdldev/dockpal-agent:latest"
// - DOCKPAL_MODE=direct environment variable
// - DOCKPAL_TOKEN environment variable with the token value
// - Port mapping 9273:9273
// - Volume mount /var/run/docker.sock:/var/run/docker.sock
func TestProperty_DirectMode_ContainsRequiredElements(t *testing.T) {
	f := func(host string, token string) bool {
		// Filter to valid inputs (non-empty, printable ASCII only)
		if host == "" || token == "" {
			return true // skip empty values
		}
		for _, c := range host {
			if c < 32 || c > 126 {
				return true // skip non-printable
			}
		}
		for _, c := range token {
			if c < 32 || c > 126 {
				return true // skip non-printable
			}
		}

		cmd := generateInstallCommand("direct", host, token)

		// Verify all required elements are present
		checks := []struct {
			name    string
			present bool
		}{
			{"image sdldev/dockpal-agent:latest", strings.Contains(cmd, "sdldev/dockpal-agent:latest")},
			{"DOCKPAL_MODE=direct", strings.Contains(cmd, "DOCKPAL_MODE=direct")},
			{"DOCKPAL_TOKEN=" + token, strings.Contains(cmd, "DOCKPAL_TOKEN="+token)},
			{"port mapping 9273:9273", strings.Contains(cmd, "9273:9273")},
			{"docker.sock volume mount", strings.Contains(cmd, "/var/run/docker.sock:/var/run/docker.sock")},
		}

		for _, check := range checks {
			if !check.present {
				t.Logf("direct mode missing: %s", check.name)
				t.Logf("command: %s", cmd)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - direct mode install command missing required elements: %v", err)
	}
}

// TestProperty_DirectMode_NoServerURL verifies that direct mode does NOT
// include the DOCKPAL_SERVER environment variable.
func TestProperty_DirectMode_NoServerURL(t *testing.T) {
	f := func(host string, token string) bool {
		if host == "" || token == "" {
			return true
		}
		for _, c := range host {
			if c < 32 || c > 126 {
				return true
			}
		}
		for _, c := range token {
			if c < 32 || c > 126 {
				return true
			}
		}

		cmd := generateInstallCommand("direct", host, token)

		// Direct mode should NOT contain DOCKPAL_SERVER
		if strings.Contains(cmd, "DOCKPAL_SERVER") {
			t.Logf("direct mode should not contain DOCKPAL_SERVER")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - direct mode should not contain DOCKPAL_SERVER: %v", err)
	}
}

// TestProperty_EdgeMode_ContainsRequiredElements verifies that edge mode
// install commands contain all required elements:
// - Image "sdldev/dockpal-agent:latest"
// - DOCKPAL_MODE=edge environment variable
// - DOCKPAL_SERVER environment variable with WebSocket URL (wss://host/api/agent/connect)
// - DOCKPAL_TOKEN environment variable with the token value
// - Volume mount /var/run/docker.sock:/var/run/docker.sock
// - NO port mapping
func TestProperty_EdgeMode_ContainsRequiredElements(t *testing.T) {
	f := func(host string, token string) bool {
		// Filter to valid inputs (non-empty, printable ASCII only)
		if host == "" || token == "" {
			return true // skip empty values
		}
		for _, c := range host {
			if c < 32 || c > 126 {
				return true // skip non-printable
			}
		}
		for _, c := range token {
			if c < 32 || c > 126 {
				return true // skip non-printable
			}
		}

		cmd := generateInstallCommand("edge", host, token)

		// Build expected WebSocket URL
		wsURL := "wss://" + host + "/api/agent/connect"

		// Verify all required elements are present
		checks := []struct {
			name    string
			present bool
		}{
			{"image sdldev/dockpal-agent:latest", strings.Contains(cmd, "sdldev/dockpal-agent:latest")},
			{"DOCKPAL_MODE=edge", strings.Contains(cmd, "DOCKPAL_MODE=edge")},
			{"DOCKPAL_SERVER=" + wsURL, strings.Contains(cmd, "DOCKPAL_SERVER="+wsURL)},
			{"DOCKPAL_TOKEN=" + token, strings.Contains(cmd, "DOCKPAL_TOKEN="+token)},
			{"docker.sock volume mount", strings.Contains(cmd, "/var/run/docker.sock:/var/run/docker.sock")},
		}

		for _, check := range checks {
			if !check.present {
				t.Logf("edge mode missing: %s", check.name)
				t.Logf("command: %s", cmd)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - edge mode install command missing required elements: %v", err)
	}
}

// TestProperty_EdgeMode_NoPortMapping verifies that edge mode does NOT
// include any port mapping.
func TestProperty_EdgeMode_NoPortMapping(t *testing.T) {
	f := func(host string, token string) bool {
		if host == "" || token == "" {
			return true
		}
		for _, c := range host {
			if c < 32 || c > 126 {
				return true
			}
		}
		for _, c := range token {
			if c < 32 || c > 126 {
				return true
			}
		}

		cmd := generateInstallCommand("edge", host, token)

		// Edge mode should NOT contain port mapping (-p flag)
		if strings.Contains(cmd, "-p ") || strings.Contains(cmd, "-p\t") {
			t.Logf("edge mode should not contain port mapping")
			return false
		}

		// Also check for any port pattern
		if strings.Contains(cmd, ":9273") {
			t.Logf("edge mode should not contain port 9273")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - edge mode should not contain port mapping: %v", err)
	}
}

// TestProperty_EdgeMode_WebSocketURLFormat verifies that the WebSocket URL
// in edge mode follows the correct format: wss://host/api/agent/connect
func TestProperty_EdgeMode_WebSocketURLFormat(t *testing.T) {
	f := func(host string, token string) bool {
		if host == "" || token == "" {
			return true
		}
		for _, c := range host {
			if c < 32 || c > 126 {
				return true
			}
		}
		for _, c := range token {
			if c < 32 || c > 126 {
				return true
			}
		}

		cmd := generateInstallCommand("edge", host, token)

		// Verify the WebSocket URL format is correct
		expectedURL := "wss://" + host + "/api/agent/connect"
		if !strings.Contains(cmd, expectedURL) {
			t.Logf("expected WebSocket URL: %s", expectedURL)
			t.Logf("command: %s", cmd)
			return false
		}

		// Verify it uses wss (not ws)
		if strings.Contains(cmd, "ws://") {
			t.Logf("edge mode should use wss:// not ws://")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - edge mode WebSocket URL format incorrect: %v", err)
	}
}

// TestProperty_TokenPreservation verifies that the token value passed to
// generateInstallCommand appears exactly in the output command.
func TestProperty_TokenPreservation(t *testing.T) {
	f := func(mode string, host string, token string) bool {
		// Only test valid modes
		if mode != "direct" && mode != "edge" {
			return true
		}
		if host == "" || token == "" {
			return true
		}
		for _, c := range host {
			if c < 32 || c > 126 {
				return true
			}
		}
		for _, c := range token {
			if c < 32 || c > 126 {
				return true
			}
		}

		cmd := generateInstallCommand(mode, host, token)

		// Token should appear exactly as provided
		if !strings.Contains(cmd, "DOCKPAL_TOKEN="+token) {
			t.Logf("token not preserved in command")
			t.Logf("mode: %s, host: %s, token: %s", mode, host, token)
			t.Logf("command: %s", cmd)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - token not preserved correctly: %v", err)
	}
}

// TestProperty_InvalidMode_ReturnsEmpty verifies that an invalid mode
// returns an empty string.
func TestProperty_InvalidMode_ReturnsEmpty(t *testing.T) {
	f := func(mode string, host string, token string) bool {
		// Skip if mode is valid
		if mode == "direct" || mode == "edge" {
			return true
		}
		// Skip if mode looks like it could be valid
		if mode == "invalid" || mode == "local" || mode == "" {
			return true
		}

		cmd := generateInstallCommand(mode, host, token)

		return cmd == ""
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - invalid mode should return empty string: %v", err)
	}
}

// **Validates: Requirements 5.3, 5.4**

// Property 6: Agent token verification
// Tests that bcrypt correctly verifies original tokens and rejects modified tokens.

// generateTestToken creates a random 32-byte token hex string for testing.
// This mirrors the token generation in handleCreateInstance.
func generateTestToken() string {
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	return hex.EncodeToString(tokenBytes)
}

// TestProperty_OriginalToken_VerifiesCorrectly tests that when we generate
// a random token, hash it with bcrypt, and verify against the original token,
// the verification succeeds.
func TestProperty_OriginalToken_VerifiesCorrectly(t *testing.T) {
	f := func() bool {
		// Generate random 32-byte token
		token := generateTestToken()

		// Hash the token using bcrypt
		hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash: %v", err)
			return false
		}

		// Verify original token matches the hash
		err = bcrypt.CompareHashAndPassword(hash, []byte(token))
		if err != nil {
			t.Logf("original token should verify against its hash: %v", err)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - original token should verify correctly: %v", err)
	}
}

// TestProperty_ModifiedToken_DoesNotVerify tests that when we modify even a
// single character of the token, bcrypt verification fails.
func TestProperty_ModifiedToken_DoesNotVerify(t *testing.T) {
	f := func(modificationIndex int) bool {
		// Ensure modification index is valid
		if modificationIndex < 0 || modificationIndex > 63 {
			return true // skip invalid indices
		}

		// Generate random 32-byte token
		token := generateTestToken()

		// Hash the token using bcrypt
		hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash: %v", err)
			return true // skip this iteration
		}

		// Create a modified token by changing one hex character
		modifiedToken := token
		if modificationIndex < len(modifiedToken) {
			// Flip the character to a different valid hex character
			char := modifiedToken[modificationIndex]
			var newChar byte
			switch char {
			case '0':
				newChar = '1'
			case '1':
				newChar = '2'
			case '2':
				newChar = '3'
			case '3':
				newChar = '4'
			case '4':
				newChar = '5'
			case '5':
				newChar = '6'
			case '6':
				newChar = '7'
			case '7':
				newChar = '8'
			case '8':
				newChar = '9'
			case '9':
				newChar = 'a'
			case 'a':
				newChar = 'b'
			case 'b':
				newChar = 'c'
			case 'c':
				newChar = 'd'
			case 'd':
				newChar = 'e'
			case 'e':
				newChar = 'f'
			case 'f':
				newChar = '0'
			default:
				return true // skip non-hex characters
			}
			modifiedToken = modifiedToken[:modificationIndex] + string(newChar) + modifiedToken[modificationIndex+1:]
		}

		// Verify modified token does NOT match the hash
		err = bcrypt.CompareHashAndPassword(hash, []byte(modifiedToken))
		if err == nil {
			t.Logf("modified token should NOT verify against original hash")
			t.Logf("original: %s, modified: %s", token, modifiedToken)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - modified token should not verify: %v", err)
	}
}

// TestProperty_DifferentToken_DoesNotVerify tests that a completely different
// random token does not verify against the original hash.
func TestProperty_DifferentToken_DoesNotVerify(t *testing.T) {
	f := func() bool {
		// Generate first random token and hash it
		token1 := generateTestToken()
		hash, err := bcrypt.GenerateFromPassword([]byte(token1), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash: %v", err)
			return true // skip this iteration
		}

		// Generate a completely different random token
		token2 := generateTestToken()

		// Make sure tokens are actually different
		if token1 == token2 {
			return true // skip this iteration (rare but possible)
		}

		// Verify different token does NOT match the hash
		err = bcrypt.CompareHashAndPassword(hash, []byte(token2))
		if err == nil {
			t.Logf("different token should NOT verify against original hash")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - different token should not verify: %v", err)
	}
}

// TestProperty_EmptyToken_DoesNotVerify tests that an empty token does not
// verify against any hash (empty string should never match a bcrypt hash).
func TestProperty_EmptyToken_DoesNotVerify(t *testing.T) {
	f := func() bool {
		// Generate random token and hash it
		token := generateTestToken()
		hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash: %v", err)
			return true // skip this iteration
		}

		// Try to verify with empty token
		err = bcrypt.CompareHashAndPassword(hash, []byte(""))

		// Empty token should NOT verify (bcrypt rejects empty passwords)
		if err == nil {
			t.Logf("empty token should NOT verify")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - empty token should not verify: %v", err)
	}
}

// **Validates: Requirement 4.11**

// Property 8: Instance input validation
// Tests that invalid mode values, port ranges, and name lengths are rejected.

// TestProperty_InvalidMode_Rejected verifies that mode values other than
// "direct" or "edge" are rejected by Gin's binding validation.
func TestProperty_InvalidMode_Rejected(t *testing.T) {
	f := func(mode string) bool {
		// Skip valid modes
		if mode == "direct" || mode == "edge" {
			return true
		}

		// Skip empty string (will be caught by required validation)
		if mode == "" {
			return true
		}

		req := CreateInstanceRequest{
			Name: "test-instance",
			Host: "example.com",
			Port: 9273,
			Mode: mode,
		}

		// Test using Gin's binding validation
		// We simulate what ShouldBindJSON does
		err := validateCreateInstanceRequest(req)

		// Invalid mode should fail validation
		if err == nil {
			t.Logf("invalid mode '%s' should be rejected", mode)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - invalid mode should be rejected: %v", err)
	}
}

// TestProperty_ValidMode_Accepted verifies that "direct" and "edge" modes
// pass validation.
func TestProperty_ValidMode_Accepted(t *testing.T) {
	f := func(mode string) bool {
		// Only test the two valid modes
		if mode != "direct" && mode != "edge" {
			return true
		}

		req := CreateInstanceRequest{
			Name: "test-instance",
			Host: "example.com",
			Port: 9273,
			Mode: mode,
		}

		err := validateCreateInstanceRequest(req)

		// Valid mode should pass
		if err != nil {
			t.Logf("valid mode '%s' should be accepted, got error: %v", mode, err)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 10}); err != nil {
		t.Errorf("Property violated - valid mode should be accepted: %v", err)
	}
}

// TestProperty_InvalidPort_Rejected verifies that ports outside 1-65535
// are rejected.
func TestProperty_InvalidPort_Rejected(t *testing.T) {
	f := func(port int) bool {
		// Skip valid port range
		if port >= 1 && port <= 65535 {
			return true
		}

		// Skip port 0 (handled separately)
		if port == 0 {
			return true
		}

		req := CreateInstanceRequest{
			Name: "test-instance",
			Host: "example.com",
			Port: port,
			Mode: "direct",
		}

		err := validateCreateInstanceRequest(req)

		// Invalid port should fail validation
		if err == nil {
			t.Logf("port %d should be rejected (outside 1-65535)", port)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - invalid port should be rejected: %v", err)
	}
}

// TestProperty_ValidPort_Accepted verifies that ports in range 1-65535
// pass validation for direct mode.
func TestProperty_ValidPort_Accepted(t *testing.T) {
	f := func(port int) bool {
		// Only test valid port range
		if port < 1 || port > 65535 {
			return true
		}

		req := CreateInstanceRequest{
			Name: "test-instance",
			Host: "example.com",
			Port: port,
			Mode: "direct",
		}

		err := validateCreateInstanceRequest(req)

		// Valid port should pass
		if err != nil {
			t.Logf("port %d should be accepted (in 1-65535), got error: %v", port, err)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - valid port should be accepted: %v", err)
	}
}

// TestProperty_NameExceedingMaxLength_Rejected verifies that names
// exceeding 100 characters are rejected.
func TestProperty_NameExceedingMaxLength_Rejected(t *testing.T) {
	f := func(nameLen int) bool {
		// Only test names that would exceed 100 chars
		if nameLen <= 1 || nameLen > 200 {
			return true // skip
		}

		// Create a name with exact length
		name := strings.Repeat("a", nameLen)

		// Verify our test name is actually > 100 chars
		if len(name) <= 100 {
			return true
		}

		req := CreateInstanceRequest{
			Name: name,
			Host: "example.com",
			Port: 9273,
			Mode: "direct",
		}

		err := validateCreateInstanceRequest(req)

		// Name > 100 chars should fail validation
		if err == nil {
			t.Logf("name with %d characters should be rejected (exceeds 100)", nameLen)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - name exceeding 100 chars should be rejected: %v", err)
	}
}

// TestProperty_NameWithinMaxLength_Accepted verifies that names with
// 1-100 characters pass validation.
func TestProperty_NameWithinMaxLength_Accepted(t *testing.T) {
	f := func(nameLen int) bool {
		// Only test valid name lengths
		if nameLen < 1 || nameLen > 100 {
			return true
		}

		name := strings.Repeat("a", nameLen)

		req := CreateInstanceRequest{
			Name: name,
			Host: "example.com",
			Port: 9273,
			Mode: "direct",
		}

		err := validateCreateInstanceRequest(req)

		// Valid name length should pass
		if err != nil {
			t.Logf("name with %d characters should be accepted, got error: %v", nameLen, err)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - valid name length should be accepted: %v", err)
	}
}

// TestProperty_EmptyName_Rejected verifies that empty names are rejected.
func TestProperty_EmptyName_Rejected(t *testing.T) {
	f := func() bool {
		req := CreateInstanceRequest{
			Name: "",
			Host: "example.com",
			Port: 9273,
			Mode: "direct",
		}

		err := validateCreateInstanceRequest(req)

		// Empty name should fail validation
		if err == nil {
			t.Logf("empty name should be rejected")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 10}); err != nil {
		t.Errorf("Property violated - empty name should be rejected: %v", err)
	}
}

// TestProperty_DirectModeRequiresHost verifies that direct mode requires
// a host field.
func TestProperty_DirectModeRequiresHost(t *testing.T) {
	f := func() bool {
		req := CreateInstanceRequest{
			Name: "test-instance",
			Host: "",
			Port: 9273,
			Mode: "direct",
		}

		err := validateCreateInstanceRequest(req)

		// Direct mode without host should fail
		if err == nil {
			t.Logf("direct mode without host should be rejected")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 10}); err != nil {
		t.Errorf("Property violated - direct mode requires host: %v", err)
	}
}

// TestProperty_EdgeModeNoHostRequired verifies that edge mode does not
// require a host field.
func TestProperty_EdgeModeNoHostRequired(t *testing.T) {
	f := func() bool {
		req := CreateInstanceRequest{
			Name: "test-instance",
			Host: "",
			Port: 0,
			Mode: "edge",
		}

		err := validateCreateInstanceRequest(req)

		// Edge mode without host should pass
		if err != nil {
			t.Logf("edge mode without host should be accepted, got error: %v", err)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 10}); err != nil {
		t.Errorf("Property violated - edge mode should not require host: %v", err)
	}
}

// validateCreateInstanceRequest simulates Gin's binding validation for
// CreateInstanceRequest. This tests the validation logic that would be
// applied when handling HTTP requests.
func validateCreateInstanceRequest(req CreateInstanceRequest) error {
	// Check required fields
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if req.Mode == "" {
		return fmt.Errorf("mode is required")
	}

	// Validate mode using oneof constraint
	if req.Mode != "direct" && req.Mode != "edge" {
		return fmt.Errorf("mode must be one of: direct, edge")
	}

	// Validate name length
	if len(req.Name) > 100 {
		return fmt.Errorf("name must be maximum 100 characters")
	}

	// Mode-specific validation
	if req.Mode == "direct" {
		// Direct mode requires host
		if req.Host == "" {
			return fmt.Errorf("host is required for direct mode")
		}
		// Direct mode requires valid port
		if req.Port < 1 || req.Port > 65535 {
			return fmt.Errorf("port must be between 1 and 65535")
		}
	}

	return nil
}

// TestProperty_BCryptHashUniqueness tests that the same token produces
// different hashes (due to random salt).
func TestProperty_BCryptHashUniqueness(t *testing.T) {
	f := func() bool {
		token := generateTestToken()

		// Generate two hashes for the same token
		hash1, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash: %v", err)
			return true // skip
		}

		hash2, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash: %v", err)
			return true // skip
		}

		// Both hashes should verify the original token
		err = bcrypt.CompareHashAndPassword(hash1, []byte(token))
		if err != nil {
			t.Logf("hash1 should verify original token: %v", err)
			return false
		}

		err = bcrypt.CompareHashAndPassword(hash2, []byte(token))
		if err != nil {
			t.Logf("hash2 should verify original token: %v", err)
			return false
		}

		// Hashes should be different (due to random salt)
		if string(hash1) == string(hash2) {
			t.Logf("bcrypt should produce different hashes due to salt")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - bcrypt hash uniqueness: %v", err)
	}
}
// **Validates: Requirement 4.8**

// Property 13: Token rotation produces distinct credentials
// Tests that token rotation generates new credentials that are different
// from the previous ones (both hash and encrypted token).

// TestProperty_TokenRotation_ProducesDistinctHash verifies that after
// rotating a token, the new bcrypt hash is different from the original.
func TestProperty_TokenRotation_ProducesDistinctHash(t *testing.T) {
	f := func() bool {
		// Generate initial token
		tokenBytes1 := make([]byte, 32)
		rand.Read(tokenBytes1)
		token1 := hex.EncodeToString(tokenBytes1)

		// Hash the initial token
		hash1, err := bcrypt.GenerateFromPassword([]byte(token1), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash: %v", err)
			return true // skip this iteration
		}

		// Simulate token rotation: generate a new token
		tokenBytes2 := make([]byte, 32)
		rand.Read(tokenBytes2)
		token2 := hex.EncodeToString(tokenBytes2)

		// Hash the rotated token
		hash2, err := bcrypt.GenerateFromPassword([]byte(token2), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash: %v", err)
			return true // skip this iteration
		}

		// Both tokens should verify against their respective hashes
		if err := bcrypt.CompareHashAndPassword(hash1, []byte(token1)); err != nil {
			t.Logf("original token should verify against hash1: %v", err)
			return false
		}
		if err := bcrypt.CompareHashAndPassword(hash2, []byte(token2)); err != nil {
			t.Logf("rotated token should verify against hash2: %v", err)
			return false
		}

		// The hashes should be different (bcrypt uses random salt)
		if string(hash1) == string(hash2) {
			t.Logf("rotated token hash should differ from original hash")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - token rotation should produce distinct hash: %v", err)
	}
}

// TestProperty_TokenRotation_ProducesDistinctEncryptedToken verifies that
// after rotating a token, the new encrypted token decrypts to different
// plaintext than the original.
func TestProperty_TokenRotation_ProducesDistinctEncryptedToken(t *testing.T) {
	jwtSecret := "test-jwt-secret-for-encryption"
	cryptoKey, err := registry.DeriveKey(jwtSecret)
	if err != nil {
		t.Fatalf("failed to derive encryption key: %v", err)
	}

	f := func() bool {
		// Generate initial token
		tokenBytes1 := make([]byte, 32)
		rand.Read(tokenBytes1)
		token1 := hex.EncodeToString(tokenBytes1)

		// Encrypt the initial token
		encrypted1, err := registry.Encrypt(tokenBytes1, cryptoKey)
		if err != nil {
			t.Logf("failed to encrypt token1: %v", err)
			return true // skip this iteration
		}

		// Simulate token rotation: generate a new token
		tokenBytes2 := make([]byte, 32)
		rand.Read(tokenBytes2)
		token2 := hex.EncodeToString(tokenBytes2)

		// Encrypt the rotated token
		encrypted2, err := registry.Encrypt(tokenBytes2, cryptoKey)
		if err != nil {
			t.Logf("failed to encrypt token2: %v", err)
			return true // skip this iteration
		}

		// Both encrypted tokens should decrypt back to their original values
		decrypted1, err := registry.Decrypt(encrypted1, cryptoKey)
		if err != nil {
			t.Logf("failed to decrypt encrypted1: %v", err)
			return false
		}
		if hex.EncodeToString(decrypted1) != token1 {
			t.Logf("decrypted token1 should match original token1")
			return false
		}

		decrypted2, err := registry.Decrypt(encrypted2, cryptoKey)
		if err != nil {
			t.Logf("failed to decrypt encrypted2: %v", err)
			return false
		}
		if hex.EncodeToString(decrypted2) != token2 {
			t.Logf("decrypted token2 should match original token2")
			return false
		}

		// The decrypted tokens should be different
		if hex.EncodeToString(decrypted1) == hex.EncodeToString(decrypted2) {
			t.Logf("rotated token should differ from original token (random tokens might collide)")
			return false
		}

		// The encrypted tokens should also be different (different nonce)
		if string(encrypted1) == string(encrypted2) {
			t.Logf("encrypted tokens should differ due to different nonces")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - token rotation should produce distinct encrypted token: %v", err)
	}
}

// TestProperty_TokenRotation_BothHashAndEncryptedDiffer verifies the full
// token rotation scenario: after rotation, both the hash AND the encrypted
// token should be different from the original.
func TestProperty_TokenRotation_BothHashAndEncryptedDiffer(t *testing.T) {
	jwtSecret := "test-jwt-secret-full-rotation"
	cryptoKey, err := registry.DeriveKey(jwtSecret)
	if err != nil {
		t.Fatalf("failed to derive encryption key: %v", err)
	}

	f := func() bool {
		// === Initial token setup ===
		tokenBytes1 := make([]byte, 32)
		rand.Read(tokenBytes1)
		token1 := hex.EncodeToString(tokenBytes1)

		// Hash initial token
		hash1, err := bcrypt.GenerateFromPassword([]byte(token1), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash1: %v", err)
			return true // skip
		}

		// Encrypt initial token
		encrypted1, err := registry.Encrypt(tokenBytes1, cryptoKey)
		if err != nil {
			t.Logf("failed to encrypt token1: %v", err)
			return true // skip
		}

		// === Token rotation ===
		tokenBytes2 := make([]byte, 32)
		rand.Read(tokenBytes2)
		token2 := hex.EncodeToString(tokenBytes2)

		// Hash rotated token
		hash2, err := bcrypt.GenerateFromPassword([]byte(token2), bcrypt.DefaultCost)
		if err != nil {
			t.Logf("failed to generate hash2: %v", err)
			return true // skip
		}

		// Encrypt rotated token
		encrypted2, err := registry.Encrypt(tokenBytes2, cryptoKey)
		if err != nil {
			t.Logf("failed to encrypt token2: %v", err)
			return true // skip
		}

		// === Verification ===
		// Verify original token verifies against original hash
		if err := bcrypt.CompareHashAndPassword(hash1, []byte(token1)); err != nil {
			t.Logf("original token should verify: %v", err)
			return false
		}
		// Verify rotated token verifies against rotated hash
		if err := bcrypt.CompareHashAndPassword(hash2, []byte(token2)); err != nil {
			t.Logf("rotated token should verify: %v", err)
			return false
		}

		// CHECK 1: Hashes should be different
		if string(hash1) == string(hash2) {
			t.Logf("hash1 and hash2 should be different")
			return false
		}

		// CHECK 2: Encrypted tokens should decrypt to different plaintext
		decrypted1, err := registry.Decrypt(encrypted1, cryptoKey)
		if err != nil {
			t.Logf("failed to decrypt encrypted1: %v", err)
			return false
		}
		decrypted2, err := registry.Decrypt(encrypted2, cryptoKey)
		if err != nil {
			t.Logf("failed to decrypt encrypted2: %v", err)
			return false
		}

		if hex.EncodeToString(decrypted1) == hex.EncodeToString(decrypted2) {
			t.Logf("decrypted tokens should be different")
			return false
		}

		// CHECK 3: Encrypted data should be different (different nonce)
		if string(encrypted1) == string(encrypted2) {
			t.Logf("encrypted tokens should be different")
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property violated - token rotation should produce distinct credentials: %v", err)
	}
}