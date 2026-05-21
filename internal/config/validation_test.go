package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidator_ValidateConfig(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "dockpal-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test configuration
	cfg := &Config{
		DataDir:    tempDir,
		DBPath:     filepath.Join(tempDir, "test.db"),
		LogPath:    filepath.Join(tempDir, "test.log"),
		SecretPath: filepath.Join(tempDir, ".secret"),
		Port:       "3013", // Use different port to avoid conflicts
		TLS:        false,
	}

	validator := NewValidator(cfg)
	result := validator.ValidateConfig()

	if !result.IsValid {
		t.Errorf("Expected validation to pass, but got errors: %v", result.Errors)
	}

	// Check that no critical errors occurred
	for _, err := range result.Errors {
		t.Errorf("Unexpected validation error: %s", err)
	}

	// Warnings are acceptable for test environment
	t.Logf("Validation warnings (acceptable): %v", result.Warnings)
}

func TestValidator_InvalidPort(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dockpal-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := &Config{
		DataDir:    tempDir,
		DBPath:     filepath.Join(tempDir, "test.db"),
		LogPath:    filepath.Join(tempDir, "test.log"),
		SecretPath: filepath.Join(tempDir, ".secret"),
		Port:       "99999", // Invalid port
		TLS:        false,
	}

	validator := NewValidator(cfg)
	result := validator.ValidateConfig()

	if result.IsValid {
		t.Error("Expected validation to fail with invalid port")
	}

	foundPortError := false
	for _, err := range result.Errors {
		if err == "Port number 99999 is out of valid range (1-65535)" {
			foundPortError = true
			break
		}
	}

	if !foundPortError {
		t.Errorf("Expected port validation error, got: %v", result.Errors)
	}
}

func TestValidator_NonExistentPaths(t *testing.T) {
	// Use non-existent parent directory
	cfg := &Config{
		DataDir:    "/non/existent/path/data",
		DBPath:     "/non/existent/path/data/test.db",
		LogPath:    "/non/existent/path/data/test.log",
		SecretPath: "/non/existent/path/data/.secret",
		Port:       "3013",
		TLS:        false,
	}

	validator := NewValidator(cfg)
	result := validator.ValidateConfig()

	if result.IsValid {
		t.Error("Expected validation to fail with non-existent paths")
	}

	// Should have errors about directory creation
	hasDirError := false
	for _, err := range result.Errors {
		if contains(err, "Failed to create") {
			hasDirError = true
			break
		}
	}

	if !hasDirError {
		t.Errorf("Expected directory creation errors, got: %v", result.Errors)
	}
}

func TestValidator_TLSValidation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dockpal-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test TLS with non-existent certificate files
	cfg := &Config{
		DataDir:    tempDir,
		DBPath:     filepath.Join(tempDir, "test.db"),
		LogPath:    filepath.Join(tempDir, "test.log"),
		SecretPath: filepath.Join(tempDir, ".secret"),
		Port:       "3443",
		TLS:        true,
		TLSCert:    filepath.Join(tempDir, "nonexistent.crt"),
		TLSKey:     filepath.Join(tempDir, "nonexistent.key"),
	}

	validator := NewValidator(cfg)
	result := validator.ValidateConfig()

	if result.IsValid {
		t.Error("Expected validation to fail with missing TLS files")
	}

	hasCertError := false
	hasKeyError := false
	for _, err := range result.Errors {
		if contains(err, "TLS certificate file not found") {
			hasCertError = true
		}
		if contains(err, "TLS key file not found") {
			hasKeyError = true
		}
	}

	if !hasCertError || !hasKeyError {
		t.Errorf("Expected TLS certificate and key errors, got: %v", result.Errors)
	}
}

func TestValidator_TLSWithDomain(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dockpal-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test TLS with domain (Let's Encrypt mode) - should pass without cert files
	cfg := &Config{
		DataDir:    tempDir,
		DBPath:     filepath.Join(tempDir, "test.db"),
		LogPath:    filepath.Join(tempDir, "test.log"),
		SecretPath: filepath.Join(tempDir, ".secret"),
		Port:       "3443",
		TLS:        true,
		TLSDomain:  "example.com",
	}

	validator := NewValidator(cfg)
	result := validator.ValidateConfig()

	// Should pass validation (Let's Encrypt mode doesn't require existing cert files)
	if !result.IsValid {
		t.Errorf("Expected TLS with domain to validate, got errors: %v", result.Errors)
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())))
}