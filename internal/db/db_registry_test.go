package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
		os.Remove(dbPath)
	})
	return database
}

func TestSaveAndGetRegistryCredential(t *testing.T) {
	db := setupTestDB(t)

	cred := RegistryCredential{
		ID:             "reg-123",
		Registry:       "ghcr.io",
		Username:       "testuser",
		EncryptedToken: []byte("encrypted-data"),
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}

	if err := db.SaveRegistryCredential(cred); err != nil {
		t.Fatalf("SaveRegistryCredential failed: %v", err)
	}

	got, err := db.GetRegistryCredential("reg-123")
	if err != nil {
		t.Fatalf("GetRegistryCredential failed: %v", err)
	}

	if got.ID != cred.ID {
		t.Errorf("ID = %q, want %q", got.ID, cred.ID)
	}
	if got.Registry != cred.Registry {
		t.Errorf("Registry = %q, want %q", got.Registry, cred.Registry)
	}
	if got.Username != cred.Username {
		t.Errorf("Username = %q, want %q", got.Username, cred.Username)
	}
	if string(got.EncryptedToken) != string(cred.EncryptedToken) {
		t.Errorf("EncryptedToken mismatch")
	}
}

func TestGetRegistryCredential_NotFound(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.GetRegistryCredential("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent credential, got nil")
	}
}

func TestListRegistryCredentials(t *testing.T) {
	db := setupTestDB(t)

	creds := []RegistryCredential{
		{ID: "reg-1", Registry: "ghcr.io", Username: "user1", EncryptedToken: []byte("tok1"), CreatedAt: 1000, UpdatedAt: 1000},
		{ID: "reg-2", Registry: "docker.io", Username: "user2", EncryptedToken: []byte("tok2"), CreatedAt: 2000, UpdatedAt: 2000},
	}

	for _, c := range creds {
		if err := db.SaveRegistryCredential(c); err != nil {
			t.Fatalf("SaveRegistryCredential failed: %v", err)
		}
	}

	list, err := db.ListRegistryCredentials()
	if err != nil {
		t.Fatalf("ListRegistryCredentials failed: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 credentials, got %d", len(list))
	}
}

func TestListRegistryCredentials_Empty(t *testing.T) {
	db := setupTestDB(t)

	list, err := db.ListRegistryCredentials()
	if err != nil {
		t.Fatalf("ListRegistryCredentials failed: %v", err)
	}

	if list != nil {
		t.Fatalf("expected nil for empty list, got %v", list)
	}
}

func TestDeleteRegistryCredential(t *testing.T) {
	db := setupTestDB(t)

	cred := RegistryCredential{
		ID:             "reg-del",
		Registry:       "ghcr.io",
		Username:       "user",
		EncryptedToken: []byte("tok"),
		CreatedAt:      1000,
		UpdatedAt:      1000,
	}

	if err := db.SaveRegistryCredential(cred); err != nil {
		t.Fatalf("SaveRegistryCredential failed: %v", err)
	}

	if err := db.DeleteRegistryCredential("reg-del"); err != nil {
		t.Fatalf("DeleteRegistryCredential failed: %v", err)
	}

	_, err := db.GetRegistryCredential("reg-del")
	if err == nil {
		t.Fatal("expected error after deletion, got nil")
	}
}

func TestFindRegistryCredentialByDomain(t *testing.T) {
	db := setupTestDB(t)

	creds := []RegistryCredential{
		{ID: "reg-1", Registry: "ghcr.io", Username: "user1", EncryptedToken: []byte("tok1"), CreatedAt: 1000, UpdatedAt: 1000},
		{ID: "reg-2", Registry: "ghcr.io", Username: "user2", EncryptedToken: []byte("tok2"), CreatedAt: 2000, UpdatedAt: 3000},
		{ID: "reg-3", Registry: "docker.io", Username: "user3", EncryptedToken: []byte("tok3"), CreatedAt: 1000, UpdatedAt: 1000},
	}

	for _, c := range creds {
		if err := db.SaveRegistryCredential(c); err != nil {
			t.Fatalf("SaveRegistryCredential failed: %v", err)
		}
	}

	// Should find the most recently updated ghcr.io credential
	got, err := db.FindRegistryCredentialByDomain("ghcr.io")
	if err != nil {
		t.Fatalf("FindRegistryCredentialByDomain failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected credential, got nil")
	}
	if got.ID != "reg-2" {
		t.Errorf("expected reg-2 (most recently updated), got %s", got.ID)
	}
}

func TestFindRegistryCredentialByDomain_CaseInsensitive(t *testing.T) {
	db := setupTestDB(t)

	cred := RegistryCredential{
		ID:             "reg-1",
		Registry:       "ghcr.io",
		Username:       "user1",
		EncryptedToken: []byte("tok1"),
		CreatedAt:      1000,
		UpdatedAt:      1000,
	}
	if err := db.SaveRegistryCredential(cred); err != nil {
		t.Fatalf("SaveRegistryCredential failed: %v", err)
	}

	// Search with different case
	got, err := db.FindRegistryCredentialByDomain("GHCR.IO")
	if err != nil {
		t.Fatalf("FindRegistryCredentialByDomain failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected credential with case-insensitive match, got nil")
	}
	if got.ID != "reg-1" {
		t.Errorf("expected reg-1, got %s", got.ID)
	}
}

func TestFindRegistryCredentialByDomain_NotFound(t *testing.T) {
	db := setupTestDB(t)

	got, err := db.FindRegistryCredentialByDomain("nonexistent.io")
	if err != nil {
		t.Fatalf("FindRegistryCredentialByDomain failed: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for no match, got %+v", got)
	}
}
