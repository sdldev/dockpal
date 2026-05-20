package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultSecretFilePath = "/opt/dockpal/data/.secret"

// LoadOrGenerateSecret resolves the JWT signing secret using a priority chain:
// 1. JWT_SECRET environment variable
// 2. Existing secret file at /opt/dockpal/data/.secret
// 3. Generate a new 32-byte cryptographic random secret, hex-encode, and persist to file
func LoadOrGenerateSecret() (string, error) {
	return loadOrGenerateSecret(defaultSecretFilePath)
}

// LoadOrGenerateSecretAt resolves the JWT signing secret using the same priority
// chain as LoadOrGenerateSecret, but allows specifying a custom secret file path.
func LoadOrGenerateSecretAt(secretFilePath string) (string, error) {
	return loadOrGenerateSecret(secretFilePath)
}

// loadOrGenerateSecret is the internal implementation that accepts a configurable path
// for testability.
func loadOrGenerateSecret(secretFilePath string) (string, error) {
	// Priority 1: Environment variable
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		return secret, nil
	}

	// Priority 2: Existing secret file
	if data, err := os.ReadFile(secretFilePath); err == nil {
		secret := strings.TrimSpace(string(data))
		if secret != "" {
			return secret, nil
		}
	}

	// Priority 3: Generate and persist
	secret, err := generateNewSecret()
	if err != nil {
		return "", fmt.Errorf("failed to generate secret: %w", err)
	}

	// Ensure the directory exists
	dir := filepath.Dir(secretFilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}

	// Write secret file with restricted permissions (owner read/write only)
	if err := os.WriteFile(secretFilePath, []byte(secret), 0600); err != nil {
		return "", fmt.Errorf("failed to persist secret: %w", err)
	}

	return secret, nil
}

// generateNewSecret creates a cryptographically random 32-byte secret encoded as hex.
func generateNewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
