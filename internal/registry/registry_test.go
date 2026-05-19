package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sdldev/dockpal/internal/db"
)

func setupTestManager(t *testing.T) *Manager {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mgr := NewManager(database, "test-jwt-secret")
	if mgr.cryptoKey == nil {
		t.Fatal("expected crypto key to be derived")
	}
	return mgr
}

func TestNewManager(t *testing.T) {
	t.Run("creates manager with valid secret", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.New(dbPath)
		if err != nil {
			t.Fatalf("failed to create test db: %v", err)
		}
		defer database.Close()

		mgr := NewManager(database, "my-secret")
		if mgr == nil {
			t.Fatal("expected non-nil manager")
		}
		if mgr.cryptoKey == nil {
			t.Fatal("expected crypto key to be set")
		}
		if len(mgr.cryptoKey) != 32 {
			t.Fatalf("expected 32-byte key, got %d", len(mgr.cryptoKey))
		}
	})

	t.Run("creates manager with nil key on empty secret", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.New(dbPath)
		if err != nil {
			t.Fatalf("failed to create test db: %v", err)
		}
		defer database.Close()

		mgr := NewManager(database, "")
		if mgr == nil {
			t.Fatal("expected non-nil manager")
		}
		if mgr.cryptoKey != nil {
			t.Fatal("expected nil crypto key for empty secret")
		}
	})
}

func TestValidatePAT(t *testing.T) {
	t.Run("valid classic PAT (ghp_)", func(t *testing.T) {
		// ghp_ + 36 alphanumeric = 40 total
		token := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		if err := ValidatePAT(token); err != nil {
			t.Errorf("expected valid, got error: %v", err)
		}
	})

	t.Run("valid fine-grained PAT (github_pat_)", func(t *testing.T) {
		token := "github_pat_11ABCDEFGH0123456789abcdefghijklmnopqrstuvwxyz"
		if err := ValidatePAT(token); err != nil {
			t.Errorf("expected valid, got error: %v", err)
		}
	})

	t.Run("invalid classic PAT wrong length", func(t *testing.T) {
		token := "ghp_short"
		err := ValidatePAT(token)
		if err == nil {
			t.Error("expected error for short classic PAT")
		}
	})

	t.Run("invalid classic PAT non-alphanumeric", func(t *testing.T) {
		token := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ!@#$%^&*()"
		err := ValidatePAT(token)
		if err == nil {
			t.Error("expected error for non-alphanumeric PAT")
		}
	})

	t.Run("invalid fine-grained PAT too short", func(t *testing.T) {
		token := "github_pat_short"
		err := ValidatePAT(token)
		if err == nil {
			t.Error("expected error for short fine-grained PAT")
		}
	})

	t.Run("invalid prefix", func(t *testing.T) {
		token := "gho_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefgh"
		err := ValidatePAT(token)
		if err == nil {
			t.Error("expected error for invalid prefix")
		}
	})

	t.Run("empty token", func(t *testing.T) {
		err := ValidatePAT("")
		if err == nil {
			t.Error("expected error for empty token")
		}
	})
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		name     string
		imageRef string
		expected string
	}{
		{"ghcr.io image", "ghcr.io/owner/app:v1", "ghcr.io"},
		{"ghcr.io uppercase", "GHCR.IO/owner/app:v1", "ghcr.io"},
		{"docker hub short", "nginx:latest", ""},
		{"docker hub with owner", "library/nginx:latest", ""},
		{"custom registry", "registry.example.com/myapp:latest", "registry.example.com"},
		{"no tag", "ghcr.io/owner/app", "ghcr.io"},
		{"bare image", "ubuntu", ""},
		{"port in registry", "registry.io:5000/image:tag", "registry.io:5000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractDomain(tt.imageRef)
			if result != tt.expected {
				t.Errorf("ExtractDomain(%q) = %q, want %q", tt.imageRef, result, tt.expected)
			}
		})
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{"normal token", "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "****6789"},
		{"short token", "abc", "****"},
		{"exactly 4 chars", "abcd", "****"},
		{"5 chars", "abcde", "****bcde"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskToken(tt.token)
			if result != tt.expected {
				t.Errorf("MaskToken(%q) = %q, want %q", tt.token, result, tt.expected)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	t.Run("creates credential successfully", func(t *testing.T) {
		mgr := setupTestManager(t)
		req := CreateRequest{
			Registry: "ghcr.io",
			Username: "testuser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		}

		summary, err := mgr.Create(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary.Registry != "ghcr.io" {
			t.Errorf("expected registry ghcr.io, got %s", summary.Registry)
		}
		if summary.Username != "testuser" {
			t.Errorf("expected username testuser, got %s", summary.Username)
		}
		if summary.MaskedToken != "****6789" {
			t.Errorf("expected masked token ****6789, got %s", summary.MaskedToken)
		}
		if summary.ID == "" {
			t.Error("expected non-empty ID")
		}
	})

	t.Run("rejects empty registry", func(t *testing.T) {
		mgr := setupTestManager(t)
		req := CreateRequest{
			Registry: "",
			Username: "testuser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		}
		_, err := mgr.Create(req)
		if err == nil {
			t.Error("expected error for empty registry")
		}
	})

	t.Run("rejects empty username", func(t *testing.T) {
		mgr := setupTestManager(t)
		req := CreateRequest{
			Registry: "ghcr.io",
			Username: "",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		}
		_, err := mgr.Create(req)
		if err == nil {
			t.Error("expected error for empty username")
		}
	})

	t.Run("rejects invalid token format", func(t *testing.T) {
		mgr := setupTestManager(t)
		req := CreateRequest{
			Registry: "ghcr.io",
			Username: "testuser",
			Token:    "invalid-token",
		}
		_, err := mgr.Create(req)
		if err == nil {
			t.Error("expected error for invalid token")
		}
	})

	t.Run("updates existing credential on duplicate registry", func(t *testing.T) {
		mgr := setupTestManager(t)
		req1 := CreateRequest{
			Registry: "ghcr.io",
			Username: "user1",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		}
		summary1, err := mgr.Create(req1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		req2 := CreateRequest{
			Registry: "GHCR.IO", // case-insensitive duplicate
			Username: "user2",
			Token:    "ghp_12345678901234567890123456789012ABCD",
		}
		summary2, err := mgr.Create(req2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have same ID (updated, not new)
		if summary2.ID != summary1.ID {
			t.Errorf("expected same ID on update, got %s vs %s", summary2.ID, summary1.ID)
		}
		if summary2.Username != "user2" {
			t.Errorf("expected updated username user2, got %s", summary2.Username)
		}
	})

	t.Run("rejects when crypto key is nil", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.New(dbPath)
		if err != nil {
			t.Fatalf("failed to create test db: %v", err)
		}
		defer database.Close()

		mgr := NewManager(database, "")
		req := CreateRequest{
			Registry: "ghcr.io",
			Username: "testuser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		}
		_, err = mgr.Create(req)
		if err == nil {
			t.Error("expected error when crypto key is nil")
		}
		if err.Error() != "encryption configuration error" {
			t.Errorf("expected generic error, got: %v", err)
		}
	})
}

func TestList(t *testing.T) {
	t.Run("lists credentials", func(t *testing.T) {
		mgr := setupTestManager(t)

		// Create two credentials
		mgr.Create(CreateRequest{
			Registry: "ghcr.io",
			Username: "user1",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		})
		mgr.Create(CreateRequest{
			Registry: "registry.example.com",
			Username: "user2",
			Token:    "ghp_12345678901234567890123456789012ABCD",
		})

		list, err := mgr.List()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("expected 2 credentials, got %d", len(list))
		}
	})

	t.Run("returns empty list when no credentials", func(t *testing.T) {
		mgr := setupTestManager(t)
		list, err := mgr.List()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("expected 0 credentials, got %d", len(list))
		}
	})
}

func TestGet(t *testing.T) {
	t.Run("gets credential by ID", func(t *testing.T) {
		mgr := setupTestManager(t)
		created, _ := mgr.Create(CreateRequest{
			Registry: "ghcr.io",
			Username: "testuser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		})

		got, err := mgr.Get(created.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("expected ID %s, got %s", created.ID, got.ID)
		}
		if got.Registry != "ghcr.io" {
			t.Errorf("expected registry ghcr.io, got %s", got.Registry)
		}
	})

	t.Run("returns error for non-existent ID", func(t *testing.T) {
		mgr := setupTestManager(t)
		_, err := mgr.Get("non-existent-id")
		if err == nil {
			t.Error("expected error for non-existent ID")
		}
	})
}

func TestUpdate(t *testing.T) {
	t.Run("updates token", func(t *testing.T) {
		mgr := setupTestManager(t)
		created, _ := mgr.Create(CreateRequest{
			Registry: "ghcr.io",
			Username: "testuser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		})

		err := mgr.Update(created.ID, UpdateRequest{
			Token: "ghp_12345678901234567890123456789012WXYZ",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the token was updated by checking masked value
		got, _ := mgr.Get(created.ID)
		if got.MaskedToken != "****WXYZ" {
			t.Errorf("expected masked token ****WXYZ, got %s", got.MaskedToken)
		}
	})

	t.Run("updates username", func(t *testing.T) {
		mgr := setupTestManager(t)
		created, _ := mgr.Create(CreateRequest{
			Registry: "ghcr.io",
			Username: "olduser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		})

		err := mgr.Update(created.ID, UpdateRequest{
			Username: "newuser",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, _ := mgr.Get(created.ID)
		if got.Username != "newuser" {
			t.Errorf("expected username newuser, got %s", got.Username)
		}
	})

	t.Run("rejects invalid token format on update", func(t *testing.T) {
		mgr := setupTestManager(t)
		created, _ := mgr.Create(CreateRequest{
			Registry: "ghcr.io",
			Username: "testuser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		})

		err := mgr.Update(created.ID, UpdateRequest{
			Token: "invalid-token",
		})
		if err == nil {
			t.Error("expected error for invalid token format")
		}
	})

	t.Run("returns error for non-existent ID", func(t *testing.T) {
		mgr := setupTestManager(t)
		err := mgr.Update("non-existent", UpdateRequest{Username: "x"})
		if err == nil {
			t.Error("expected error for non-existent ID")
		}
	})
}

func TestDelete(t *testing.T) {
	t.Run("deletes credential", func(t *testing.T) {
		mgr := setupTestManager(t)
		created, _ := mgr.Create(CreateRequest{
			Registry: "ghcr.io",
			Username: "testuser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		})

		err := mgr.Delete(created.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify it's gone
		_, err = mgr.Get(created.ID)
		if err == nil {
			t.Error("expected error after deletion")
		}
	})

	t.Run("returns error for non-existent ID", func(t *testing.T) {
		mgr := setupTestManager(t)
		err := mgr.Delete("non-existent")
		if err == nil {
			t.Error("expected error for non-existent ID")
		}
	})
}

func TestGetAuthHeader(t *testing.T) {
	t.Run("returns auth header for matching domain", func(t *testing.T) {
		mgr := setupTestManager(t)
		mgr.Create(CreateRequest{
			Registry: "ghcr.io",
			Username: "testuser",
			Token:    "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		})

		header, err := mgr.GetAuthHeader("ghcr.io/owner/app:v1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if header == "" {
			t.Error("expected non-empty auth header")
		}
	})

	t.Run("returns empty for Docker Hub image", func(t *testing.T) {
		mgr := setupTestManager(t)
		header, err := mgr.GetAuthHeader("nginx:latest")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if header != "" {
			t.Errorf("expected empty header for Docker Hub, got %s", header)
		}
	})

	t.Run("returns empty for unmatched domain", func(t *testing.T) {
		mgr := setupTestManager(t)
		header, err := mgr.GetAuthHeader("registry.example.com/app:v1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if header != "" {
			t.Errorf("expected empty header for unmatched domain, got %s", header)
		}
	})
}

func TestIsAlphanumeric(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"abc123", true},
		{"ABCdef", true},
		{"", true},
		{"abc!def", false},
		{"abc def", false},
		{"abc_def", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isAlphanumeric(tt.input); got != tt.expected {
				t.Errorf("isAlphanumeric(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// Ensure unused import doesn't cause issues
var _ = os.TempDir
