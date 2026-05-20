package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CachedVersion represents the cached version information stored in the cache file
type CachedVersion struct {
	LastChecked   time.Time `json:"lastChecked"`
	LatestVersion string    `json:"latestVersion"`
	ReleaseNotes  string    `json:"releaseNotes"`
	DownloadURL   string    `json:"downloadUrl"`
}

// IsCacheExpired checks if the cached version has expired based on the default TTL
func (c *CachedVersion) IsCacheExpired() bool {
	if c.LastChecked.IsZero() {
		return true
	}
	return time.Since(c.LastChecked) >= DefaultCacheTTL
}

// GetCachePath returns the full path to the version cache file
func GetCachePath(dataDir string) string {
	return filepath.Join(dataDir, "version-cache.json")
}

// ReadCache reads the cached version information from the cache file
func ReadCache(cachePath string) (*CachedVersion, error) {
	// Check if file exists
	if _, err := os.Stat(cachePath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cache file does not exist: %w", err)
	}

	// Read file content
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	// Parse JSON
	var cached CachedVersion
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, fmt.Errorf("failed to parse cache file: %w", err)
	}

	return &cached, nil
}

// WriteCache writes the cached version information to the cache file atomically
func WriteCache(cachePath string, cached *CachedVersion) error {
	// Ensure parent directory exists
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Update lastChecked timestamp
	cached.LastChecked = time.Now().UTC()

	// Marshal to JSON with indentation for readability
	data, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	// Atomic write: write to temp file then rename
	tmpPath := cachePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp cache file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, cachePath); err != nil {
		// Clean up temp file on failure
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename cache file: %w", err)
	}

	return nil
}