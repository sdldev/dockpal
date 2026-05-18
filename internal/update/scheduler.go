package update

import (
	"context"
	"log"
	"time"
)

// VersionCheckScheduler periodically checks for version updates in the background
type VersionCheckScheduler struct {
	versionService *VersionService
	interval       time.Duration
	stopCh         chan struct{}
	running        bool
}

// NewVersionCheckScheduler creates a new VersionCheckScheduler
func NewVersionCheckScheduler(versionService *VersionService, interval time.Duration) *VersionCheckScheduler {
	return &VersionCheckScheduler{
		versionService: versionService,
		interval:       interval,
		stopCh:         make(chan struct{}),
		running:        false,
	}
}

// Start begins the background version check loop
// The interval parameter specifies how often to check for updates (default 6 hours)
// The scheduler runs as a goroutine and can be stopped via Stop()
func (s *VersionCheckScheduler) Start(ctx context.Context, interval time.Duration) {
	// Use default interval if not provided or invalid
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	s.interval = interval

	// Prevent starting multiple instances
	if s.running {
		log.Println("Version checker: already running")
		return
	}
	s.running = true

	go s.run(ctx)
}

// Stop gracefully stops the background version checker
// It waits for any in-progress check to complete before returning
func (s *VersionCheckScheduler) Stop() {
	if !s.running {
		log.Println("Version checker: not running")
		return
	}

	log.Println("Version checker: stopping...")
	close(s.stopCh)
	s.running = false
}

// run is the main loop that periodically checks for version updates
func (s *VersionCheckScheduler) run(ctx context.Context) {
	// Create a new ticker with the specified interval
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run an immediate check on startup
	s.checkVersion(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("Version checker: context cancelled, stopping")
			return
		case <-s.stopCh:
			log.Println("Version checker: stopped")
			return
		case <-ticker.C:
			s.checkVersion(ctx)
		}
	}
}

// checkVersion performs the version check and updates the cache
func (s *VersionCheckScheduler) checkVersion(ctx context.Context) {
	log.Println("Version checker: running background version check...")

	info, err := s.versionService.GetVersionInfo(ctx)
	if err != nil {
		log.Printf("Version checker: failed to check version: %v\n", err)
		return
	}

	if info.UpdateAvailable {
		log.Printf("Version checker: update available - current: %s, latest: %s\n",
			info.CurrentVersion, info.LatestVersion)
	} else {
		log.Printf("Version checker: no update available - version: %s\n",
			info.CurrentVersion)
	}

	log.Println("Version checker: background check completed")
}

// GetInterval returns the current check interval
func (s *VersionCheckScheduler) GetInterval() time.Duration {
	return s.interval
}

// IsRunning returns whether the scheduler is currently running
func (s *VersionCheckScheduler) IsRunning() bool {
	return s.running
}