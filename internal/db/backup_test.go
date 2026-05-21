package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupToCreatesValidBackup(t *testing.T) {
	db := setupTestDB(t)

	// Seed some data
	user := User{
		ID:           "user-1",
		Username:     "alice",
		PasswordHash: "hash123",
		Role:         "viewer",
		CreatedAt:    time.Now().Unix(),
	}
	if err := db.CreateUser(user); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	backupDir := filepath.Join(t.TempDir(), "backups")
	backupPath := filepath.Join(backupDir, "dockpal-test.db")

	if err := db.BackupTo(backupPath); err != nil {
		t.Fatalf("BackupTo failed: %v", err)
	}

	// Verify backup file exists
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file does not exist: %v", err)
	}

	// Verify checksum file exists
	checksumPath := backupPath + ".sha256"
	if _, err := os.Stat(checksumPath); err != nil {
		t.Fatalf("checksum file does not exist: %v", err)
	}

	// Validate backup
	if err := ValidateBackup(backupPath); err != nil {
		t.Fatalf("ValidateBackup failed: %v", err)
	}

	// Verify data integrity by opening backup as a new DB
	restored, err := New(backupPath)
	if err != nil {
		t.Fatalf("failed to open restored db: %v", err)
	}
	defer restored.Close()

	got, err := restored.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser from restored db failed: %v", err)
	}
	if got.Username != "alice" || got.Role != "viewer" {
		t.Errorf("restored user mismatch: %+v", got)
	}
}

func TestValidateBackupDetectsCorruption(t *testing.T) {
	db := setupTestDB(t)

	backupDir := filepath.Join(t.TempDir(), "backups")
	backupPath := filepath.Join(backupDir, "dockpal-test.db")

	if err := db.BackupTo(backupPath); err != nil {
		t.Fatalf("BackupTo failed: %v", err)
	}

	// Corrupt the backup file
	f, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("failed to open backup for corruption: %v", err)
	}
	if _, err := f.WriteString("corruption"); err != nil {
		t.Fatalf("failed to write corruption: %v", err)
	}
	_ = f.Close()

	// Validation should fail because checksum no longer matches
	if err := ValidateBackup(backupPath); err == nil {
		t.Fatal("expected ValidateBackup to fail on corrupted file, got nil")
	}
}

func TestValidateBackupWithoutChecksum(t *testing.T) {
	db := setupTestDB(t)

	backupDir := filepath.Join(t.TempDir(), "backups")
	backupPath := filepath.Join(backupDir, "dockpal-test.db")

	if err := db.BackupTo(backupPath); err != nil {
		t.Fatalf("BackupTo failed: %v", err)
	}

	// Remove checksum file; validation should still pass (bbolt validity only)
	checksumPath := backupPath + ".sha256"
	if err := os.Remove(checksumPath); err != nil {
		t.Fatalf("failed to remove checksum: %v", err)
	}

	if err := ValidateBackup(backupPath); err != nil {
		t.Fatalf("ValidateBackup failed without checksum: %v", err)
	}
}
