package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
		os.Remove(dbPath)
	})
	return database
}

func TestSchedulerCreatesBackup(t *testing.T) {
	database := setupTestDB(t)
	dataDir := t.TempDir()

	scheduler := NewScheduler(database, dataDir, 100*time.Millisecond, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx)
	defer scheduler.Stop()

	// Wait for the initial backup plus one tick
	time.Sleep(250 * time.Millisecond)

	backupDir := filepath.Join(dataDir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("failed to read backup dir: %v", err)
	}

	var dbFiles int
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".db" {
			dbFiles++
		}
	}
	if dbFiles == 0 {
		t.Fatal("expected at least one backup file, got none")
	}
}

func TestSchedulerCleanupOldBackups(t *testing.T) {
	database := setupTestDB(t)
	dataDir := t.TempDir()
	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0750); err != nil {
		t.Fatalf("failed to create backup dir: %v", err)
	}

	// Create an old backup file (backdated 10 days)
	oldFile := filepath.Join(backupDir, "dockpal-old.db")
	if err := os.WriteFile(oldFile, []byte("old"), 0600); err != nil {
		t.Fatalf("failed to create old backup: %v", err)
	}
	oldTime := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatalf("failed to backdate old backup: %v", err)
	}
	oldChecksum := oldFile + ".sha256"
	_ = os.WriteFile(oldChecksum, []byte("abc\n"), 0600)
	_ = os.Chtimes(oldChecksum, oldTime, oldTime)

	// Create a recent backup file
	recentFile := filepath.Join(backupDir, "dockpal-recent.db")
	if err := os.WriteFile(recentFile, []byte("recent"), 0600); err != nil {
		t.Fatalf("failed to create recent backup: %v", err)
	}
	recentChecksum := recentFile + ".sha256"
	_ = os.WriteFile(recentChecksum, []byte("def\n"), 0600)

	// Retention = 7 days, so old file should be deleted
	scheduler := NewScheduler(database, dataDir, 100*time.Millisecond, 7*24*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx)
	defer scheduler.Stop()

	// Wait for initial backup + cleanup
	time.Sleep(250 * time.Millisecond)

	if _, err := os.Stat(oldFile); err == nil {
		t.Fatal("expected old backup file to be removed")
	}
	if _, err := os.Stat(oldChecksum); err == nil {
		t.Fatal("expected old checksum file to be removed")
	}
	if _, err := os.Stat(recentFile); err != nil {
		t.Fatal("expected recent backup file to be kept")
	}
	if _, err := os.Stat(recentChecksum); err != nil {
		t.Fatal("expected recent checksum file to be kept")
	}
}

func TestSchedulerDoesNotStartWhenIntervalZero(t *testing.T) {
	database := setupTestDB(t)
	dataDir := t.TempDir()

	scheduler := NewScheduler(database, dataDir, 0, 7*24*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx)
	defer scheduler.Stop()

	time.Sleep(150 * time.Millisecond)

	if scheduler.IsRunning() {
		t.Fatal("expected scheduler not to be running when interval is zero")
	}
}
