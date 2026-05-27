package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// Scheduler periodically creates database backups and cleans up old ones.
type Scheduler struct {
	database  *db.DB
	dataDir   string
	interval  time.Duration
	retention time.Duration
	mu        sync.Mutex
	stopCh    chan struct{}
	running   bool
}

// NewScheduler creates a new backup scheduler.
func NewScheduler(database *db.DB, dataDir string, interval, retention time.Duration) *Scheduler {
	return &Scheduler{
		database:  database,
		dataDir:   dataDir,
		interval:  interval,
		retention: retention,
	}
}

// Start begins the background backup loop.
// Does nothing if interval is zero or the scheduler is already running.
func (s *Scheduler) Start(ctx context.Context) {
	if s.interval <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.stopCh = make(chan struct{})
	s.running = true
	go s.run(ctx)
}

// Stop gracefully stops the background backup loop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	close(s.stopCh)
	s.running = false
}

func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.performBackup()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.performBackup()
		}
	}
}

func (s *Scheduler) performBackup() {
	backupDir := filepath.Join(s.dataDir, "backups")
	timestamp := time.Now().Format("20060102-150405")
	path := filepath.Join(backupDir, fmt.Sprintf("dockpal-%s.db", timestamp))

	if err := s.database.BackupTo(path); err != nil {
		slog.Error("scheduled backup failed", "component", "backup", "error", err)
		return
	}
	checksumVerified, err := db.ValidateBackup(path)
	if err != nil {
		slog.Error("scheduled backup verification failed", "component", "backup", "path", path, "error", err)
		return
	}

	checksumPath := path + ".sha256"
	checksum, _ := os.ReadFile(checksumPath)
	if checksumVerified {
		slog.Info("scheduled backup created and verified", "component", "backup", "path", path, "checksum", strings.TrimSpace(string(checksum)))
	} else {
		slog.Warn("scheduled backup created without checksum sidecar", "component", "backup", "path", path)
	}

	if s.retention > 0 {
		s.cleanupOldBackups()
	}
}

func (s *Scheduler) cleanupOldBackups() {
	backupDir := filepath.Join(s.dataDir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		slog.Warn("failed to read backup directory for cleanup", "component", "backup", "error", err)
		return
	}

	cutoff := time.Now().Add(-s.retention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "dockpal-") || !strings.HasSuffix(name, ".db") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(backupDir, name)
			if err := os.Remove(path); err != nil {
				slog.Warn("failed to remove old backup", "component", "backup", "path", path, "error", err)
				continue
			}
			_ = os.Remove(path + ".sha256")
			slog.Info("removed old backup", "component", "backup", "path", path)
		}
	}
}

// IsRunning returns whether the scheduler is currently running.
func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
