package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrGenerateSecretAt resolves the JWT signing secret using a priority chain:
// 1. JWT_SECRET environment variable
// 2. Existing secret file at secretFilePath
// 3. Generate a new 32-byte cryptographic random secret, hex-encode, and persist to file
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

	// Priority 3: Generate and persist.
	secret, err := generateNewSecret()
	if err != nil {
		return "", fmt.Errorf("failed to generate secret: %w", err)
	}

	// Ensure the directory exists
	dir := filepath.Dir(secretFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}

	// Create the file exclusively (O_EXCL): two Dockpal processes starting at
	// the same time on a fresh install must not each generate a different
	// secret and overwrite each other's — the loser keeps signing JWTs with a
	// key that no longer matches disk (audit-auth M5). On EEXIST, re-read and
	// use whatever the winner wrote.
	f, err := os.OpenFile(secretFilePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			data, rerr := os.ReadFile(secretFilePath)
			if rerr == nil {
				if existing := strings.TrimSpace(string(data)); existing != "" {
					return existing, nil
				}
			}
			return "", fmt.Errorf("secret file appeared but could not be read: %w", err)
		}
		return "", fmt.Errorf("failed to persist secret: %w", err)
	}
	if _, err := f.WriteString(secret); err != nil {
		f.Close()
		return "", fmt.Errorf("failed to persist secret: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", fmt.Errorf("failed to persist secret: %w", err)
	}
	if err := f.Close(); err != nil {
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
