package backup

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// Scheduler periodically creates database backups and cleans up old ones.
type Scheduler struct {
	database  *db.DB
	dataDir   string
	interval  time.Duration
	retention time.Duration
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
		stopCh:    make(chan struct{}),
		running:   false,
	}
}

// Start begins the background backup loop.
// Does nothing if interval is zero or the scheduler is already running.
func (s *Scheduler) Start(ctx context.Context) {
	if s.interval <= 0 {
		return
	}
	if s.running {
		return
	}
	s.running = true
	go s.run(ctx)
}

// Stop gracefully stops the background backup loop.
func (s *Scheduler) Stop() {
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
		log.Printf("[ERROR] scheduled backup failed: %v", err)
		return
	}

	checksumPath := path + ".sha256"
	checksum, _ := os.ReadFile(checksumPath)
	log.Printf("[INFO] scheduled backup created: %s (checksum: %s)", path, strings.TrimSpace(string(checksum)))

	if s.retention > 0 {
		s.cleanupOldBackups()
	}
}

func (s *Scheduler) cleanupOldBackups() {
	backupDir := filepath.Join(s.dataDir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		log.Printf("[WARN] failed to read backup directory for cleanup: %v", err)
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
				log.Printf("[WARN] failed to remove old backup %s: %v", path, err)
				continue
			}
			_ = os.Remove(path + ".sha256")
			log.Printf("[INFO] removed old backup: %s", path)
		}
	}
}

// IsRunning returns whether the scheduler is currently running.
func (s *Scheduler) IsRunning() bool {
	return s.running
}
