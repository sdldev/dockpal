package auth

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"testing/quick"
)

func TestLoadOrGenerateSecret_EnvVar(t *testing.T) {
	// Priority 1: env var takes precedence
	t.Setenv("JWT_SECRET", "my-env-secret")

	secret, err := loadOrGenerateSecret("/nonexistent/path/.secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "my-env-secret" {
		t.Fatalf("expected env secret, got %q", secret)
	}
}

func TestLoadOrGenerateSecret_File(t *testing.T) {
	// Priority 2: file takes precedence when no env var
	t.Setenv("JWT_SECRET", "")

	tmpDir := t.TempDir()
	secretFile := filepath.Join(tmpDir, ".secret")
	if err := os.WriteFile(secretFile, []byte("file-secret"), 0600); err != nil {
		t.Fatal(err)
	}

	secret, err := loadOrGenerateSecret(secretFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "file-secret" {
		t.Fatalf("expected file secret, got %q", secret)
	}
}

func TestLoadOrGenerateSecret_Generate(t *testing.T) {
	// Priority 3: generate when no env var and no file
	t.Setenv("JWT_SECRET", "")

	tmpDir := t.TempDir()
	secretFile := filepath.Join(tmpDir, "subdir", ".secret")

	secret, err := loadOrGenerateSecret(secretFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be 64 hex characters (32 bytes)
	if len(secret) != 64 {
		t.Fatalf("expected 64 char hex string, got length %d", len(secret))
	}
	if _, err := hex.DecodeString(secret); err != nil {
		t.Fatalf("secret is not valid hex: %v", err)
	}

	// File should be persisted
	data, err := os.ReadFile(secretFile)
	if err != nil {
		t.Fatalf("secret file not created: %v", err)
	}
	if string(data) != secret {
		t.Fatalf("persisted secret doesn't match returned secret")
	}

	// File permissions should be 0600
	info, err := os.Stat(secretFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestLoadOrGenerateSecret_GenerateIsDeterministicOnSubsequentCalls(t *testing.T) {
	// Once generated, subsequent calls should return the same secret from file
	t.Setenv("JWT_SECRET", "")

	tmpDir := t.TempDir()
	secretFile := filepath.Join(tmpDir, ".secret")

	secret1, err := loadOrGenerateSecret(secretFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secret2, err := loadOrGenerateSecret(secretFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if secret1 != secret2 {
		t.Fatalf("expected same secret on subsequent calls, got %q and %q", secret1, secret2)
	}
}

// Property 2: Secret Generation Format
// Validates: Requirements 3.4
// generateNewSecret() must always produce a valid 64-character hexadecimal string.
func TestProperty_SecretGenerationFormat(t *testing.T) {
	// The function takes no input but is non-deterministic (uses crypto/rand).
	// We use testing/quick to invoke it many times and verify the invariant holds.
	f := func(_ byte) bool {
		secret, err := generateNewSecret()
		if err != nil {
			t.Logf("generateNewSecret returned error: %v", err)
			return false
		}

		// Must be exactly 64 characters (32 bytes hex-encoded)
		if len(secret) != 64 {
			t.Logf("expected length 64, got %d", len(secret))
			return false
		}

		// Must be valid hexadecimal
		_, err = hex.DecodeString(secret)
		if err != nil {
			t.Logf("not valid hex: %v", err)
			return false
		}

		return true
	}

	cfg := &quick.Config{MaxCount: 100}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("property failed: %v", err)
	}
}
