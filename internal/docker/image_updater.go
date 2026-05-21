package docker

import (
	"context"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

// ImageUpdateStatus holds the cached update check result for a single image.
type ImageUpdateStatus struct {
	Result    *ImageUpdateResult `json:"result"`
	ImageRef  string             `json:"image_ref"`
	CheckedAt int64              `json:"checked_at"`
}

// AuthHeaderFunc is reused from compose.go (same signature).
// Defined here as well to avoid circular dependency issues if compose.go changes.

// ImageUpdateMonitor periodically checks all locally cached Docker images against
// their respective registries to detect available updates.
type ImageUpdateMonitor struct {
	client       *Client
	getAuth      func(imageRef string) (string, error)
	ticker       *time.Ticker
	stop         chan struct{}
	cache        map[string]*ImageUpdateStatus // key: imageRef
	cacheMu      sync.RWMutex
	checkInterval time.Duration
}

// defaultCheckInterval is the default time between background image update checks.
const defaultCheckInterval = 30 * time.Minute

// envCheckInterval reads the DOCKPAL_IMAGE_CHECK_INTERVAL env var (minutes).
func envCheckInterval() time.Duration {
	v := os.Getenv("DOCKPAL_IMAGE_CHECK_INTERVAL")
	if v == "" {
		return defaultCheckInterval
	}
	m, err := strconv.Atoi(v)
	if err != nil || m < 1 {
		return defaultCheckInterval
	}
	return time.Duration(m) * time.Minute
}

// NewImageUpdateMonitor creates a monitor that checks local images for updates.
// getAuth is called per-image to obtain the registry auth header (may return empty string).
func NewImageUpdateMonitor(client *Client, getAuth func(imageRef string) (string, error)) *ImageUpdateMonitor {
	return &ImageUpdateMonitor{
		client:        client,
		getAuth:       getAuth,
		checkInterval: envCheckInterval(),
		cache:         make(map[string]*ImageUpdateStatus),
		stop:          make(chan struct{}),
	}
}

// Start begins the background monitoring goroutine.
func (m *ImageUpdateMonitor) Start() {
	m.ticker = time.NewTicker(m.checkInterval)
	go m.run()
}

// Stop terminates the background monitoring goroutine.
func (m *ImageUpdateMonitor) Stop() {
	close(m.stop)
	if m.ticker != nil {
		m.ticker.Stop()
	}
}

func (m *ImageUpdateMonitor) run() {
	// Perform an initial check immediately
	m.checkAll()

	for {
		select {
		case <-m.stop:
			return
		case <-m.ticker.C:
			m.checkAll()
		}
	}
}

// checkAll lists all local images and checks each one for updates.
func (m *ImageUpdateMonitor) checkAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	images, err := m.client.ListImages(ctx)
	if err != nil {
		log.Printf("[image-updater] failed to list images: %v", err)
		return
	}

	for _, img := range images {
		imageRef := img.Repo + ":" + img.Tag
		if img.Repo == "<none>" || img.Tag == "<none>" {
			continue
		}

		authHeader := ""
		if m.getAuth != nil {
			a, _ := m.getAuth(imageRef)
			authHeader = a
		}

		result, err := m.client.CheckImageUpdate(ctx, imageRef, authHeader)
		if err != nil {
			log.Printf("[image-updater] check failed for %s: %v", imageRef, err)
			continue
		}

		m.cacheMu.Lock()
		m.cache[imageRef] = &ImageUpdateStatus{
			Result:    result,
			ImageRef:  imageRef,
			CheckedAt: result.CheckedAt,
		}
		m.cacheMu.Unlock()
	}
}

// GetStatus returns the cached update status for an image, or nil if not checked.
func (m *ImageUpdateMonitor) GetStatus(imageRef string) *ImageUpdateStatus {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()
	return m.cache[imageRef]
}

// GetAllStatuses returns a snapshot of all cached update statuses.
func (m *ImageUpdateMonitor) GetAllStatuses() []ImageUpdateStatus {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()

	results := make([]ImageUpdateStatus, 0, len(m.cache))
	for _, status := range m.cache {
		results = append(results, *status)
	}
	return results
}

// CheckNow triggers an immediate check for a specific image and returns the result.
func (m *ImageUpdateMonitor) CheckNow(ctx context.Context, imageRef string) (*ImageUpdateResult, error) {
	authHeader := ""
	if m.getAuth != nil {
		a, _ := m.getAuth(imageRef)
		authHeader = a
	}

	result, err := m.client.CheckImageUpdate(ctx, imageRef, authHeader)
	if err != nil {
		return nil, err
	}

	m.cacheMu.Lock()
	m.cache[imageRef] = &ImageUpdateStatus{
		Result:    result,
		ImageRef:  imageRef,
		CheckedAt: result.CheckedAt,
	}
	m.cacheMu.Unlock()

	return result, nil
}

// ForcePull triggers an immediate force-pull of an image.
func (m *ImageUpdateMonitor) ForcePull(ctx context.Context, imageRef string) error {
	authHeader := ""
	if m.getAuth != nil {
		a, _ := m.getAuth(imageRef)
		authHeader = a
	}
	return m.client.ForcePullImage(ctx, imageRef, authHeader)
}
