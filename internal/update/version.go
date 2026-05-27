package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/blang/semver"
)

const (
	// GitHubAPIURL is the endpoint for fetching the latest release
	GitHubAPIURL = "https://api.github.com/repos/sdldev/dockpal/releases/latest"
	// DefaultDataDir is the default data directory
	DefaultDataDir = "/opt/dockpal/data"
	// DefaultCacheTTL is the default cache TTL (1 hour)
	DefaultCacheTTL = time.Hour
)

var gitDescribeVersionPattern = regexp.MustCompile(`^(\d+\.\d+\.\d+)-\d+-g[0-9a-f]+(?:-dirty)?$`)

// Version represents a semantic version
type Version struct {
	Major int
	Minor int
	Patch int
}

// VersionInfo represents the version information returned by the API
type VersionInfo struct {
	CurrentVersion  string `json:"currentVersion"`  // e.g., "v0.2.0"
	LatestVersion   string `json:"latestVersion"`   // e.g., "v0.2.1"
	UpdateAvailable bool   `json:"updateAvailable"` // true if latest > current
	ReleaseNotes    string `json:"releaseNotes"`    // Markdown from GitHub
	DownloadURL     string `json:"downloadUrl"`     // Direct binary URL
}

// VersionService handles version checking and caching
type VersionService struct {
	dataDir        string
	currentVersion string
	httpClient     *http.Client
	cacheTTL       time.Duration
}

// NewVersionService creates a new VersionService
func NewVersionService(dataDir, currentVersion string) *VersionService {
	if dataDir == "" {
		dataDir = DefaultDataDir
	}
	return &VersionService{
		dataDir:        dataDir,
		currentVersion: currentVersion,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cacheTTL: DefaultCacheTTL,
	}
}

// NewVersionServiceWithTTL creates a new VersionService with custom cache TTL
func NewVersionServiceWithTTL(dataDir, currentVersion string, cacheTTL time.Duration) *VersionService {
	if dataDir == "" {
		dataDir = DefaultDataDir
	}
	return &VersionService{
		dataDir:        dataDir,
		currentVersion: currentVersion,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cacheTTL: cacheTTL,
	}
}

// GetVersionInfo fetches the latest version from GitHub API and returns version info
func (s *VersionService) GetVersionInfo(ctx context.Context) (*VersionInfo, error) {
	cachePath := GetCachePath(s.dataDir)

	// First try to get cached version
	cached, err := ReadCache(cachePath)
	if err == nil && cached != nil && !cached.IsCacheExpired() {
		// Return cached data, compare versions
		updateAvailable, err := CompareVersions(s.currentVersion, cached.LatestVersion)
		latestVersion := cached.LatestVersion
		if err != nil {
			updateAvailable = false
			latestVersion = s.currentVersion
		}
		return &VersionInfo{
			CurrentVersion:  s.currentVersion,
			LatestVersion:   latestVersion,
			UpdateAvailable: updateAvailable,
			ReleaseNotes:    cached.ReleaseNotes,
			DownloadURL:     cached.DownloadURL,
		}, nil
	}

	// Fetch from GitHub
	release, err := s.fetchFromGitHub(ctx)
	if err != nil {
		// If GitHub API fails and we have cached data, return it
		if cached != nil {
			latestVersion := cached.LatestVersion
			if _, compareErr := CompareVersions(s.currentVersion, cached.LatestVersion); compareErr != nil {
				latestVersion = s.currentVersion
			}
			return &VersionInfo{
				CurrentVersion:  s.currentVersion,
				LatestVersion:   latestVersion,
				UpdateAvailable: false, // Unknown due to error
				ReleaseNotes:    cached.ReleaseNotes,
				DownloadURL:     cached.DownloadURL,
			}, nil
		}
		return nil, fmt.Errorf("failed to fetch version info: %w", err)
	}

	// Compare versions
	updateAvailable, err := CompareVersions(s.currentVersion, release.TagName)
	if err != nil {
		updateAvailable = false
		release.TagName = s.currentVersion
	}

	// Update cache
	cacheData := &CachedVersion{
		LastChecked:   time.Now().UTC(),
		LatestVersion: release.TagName,
		ReleaseNotes:  release.Body,
		DownloadURL:   release.GetBrowserDownloadURL(),
	}
	if err := WriteCache(cachePath, cacheData); err != nil {
		// Log but don't fail
		fmt.Printf("Warning: failed to write version cache: %v\n", err)
	}

	return &VersionInfo{
		CurrentVersion:  s.currentVersion,
		LatestVersion:   release.TagName,
		UpdateAvailable: updateAvailable,
		ReleaseNotes:    release.Body,
		DownloadURL:     release.GetBrowserDownloadURL(),
	}, nil
}

// GetCachedVersion reads and returns the cached version info
func (s *VersionService) GetCachedVersion() (*CachedVersion, error) {
	cachePath := GetCachePath(s.dataDir)
	return ReadCache(cachePath)
}

// GetCachePath returns the full path to the cache file
func (s *VersionService) GetCachePath() string {
	return GetCachePath(s.dataDir)
}

// GitHubRelease represents the GitHub API release response
type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Body    string        `json:"body"`
	Assets  []GitHubAsset `json:"assets"`
}

// GitHubAsset represents a release asset in the GitHub API response
type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// GetAssetForPlatform returns the asset URL matching the given GOOS and GOARCH.
// It does NOT fall back to Assets[0]. Returns the matching asset URL or an error.
func (r *GitHubRelease) GetAssetForPlatform(goos, goarch string) (string, error) {
	if len(r.Assets) == 0 {
		return "", fmt.Errorf("%s: no assets in release", ErrAssetNotFoundForOSArch)
	}
	suffix := platformSuffix(goos, goarch)
	for _, a := range r.Assets {
		if strings.Contains(a.Name, suffix) {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("%s: no asset found for %s/%s", ErrAssetNotFoundForOSArch, goos, goarch)
}

// GetBrowserDownloadURL returns the browser download URL for the asset
// matching the current OS and architecture. Returns empty string on error
// (preserving backward compatibility for VersionService callers).
func (r *GitHubRelease) GetBrowserDownloadURL() string {
	url, err := r.GetAssetForPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return ""
	}
	return url
}

// assetSuffix returns the expected filename suffix for the current GOOS/GOARCH.
func assetSuffix() string {
	return platformSuffix(runtime.GOOS, runtime.GOARCH)
}

// platformSuffix returns the expected filename suffix for the given GOOS/GOARCH.
func platformSuffix(goos, goarch string) string {
	arch := goarch
	switch arch {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "arm64"
	case "arm":
		arch = "armv7"
	}
	return "-" + goos + "-" + arch
}

// fetchFromGitHub fetches the latest release from GitHub API
func (s *VersionService) fetchFromGitHub(ctx context.Context) (*GitHubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", GitHubAPIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "dockpal")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub response: %w", err)
	}

	return &release, nil
}

// CompareVersions compares two semver version strings and returns true if latest > current
func CompareVersions(current, latest string) (bool, error) {
	// Normalize version strings (remove 'v' prefix if present)
	current = normalizeVersion(current)
	latest = normalizeVersion(latest)

	currentVer, err := semver.Parse(current)
	if err != nil {
		return false, fmt.Errorf("invalid current version %q: %w", current, err)
	}

	latestVer, err := semver.Parse(latest)
	if err != nil {
		return false, fmt.Errorf("invalid latest version %q: %w", latest, err)
	}

	return latestVer.GT(currentVer), nil
}

// parseVersion parses a semantic version string into a Version struct
func parseVersion(v string) (*Version, error) {
	// Normalize - remove 'v' prefix if present
	v = normalizeVersion(v)

	ver, err := semver.Parse(v)
	if err != nil {
		return nil, fmt.Errorf("invalid version %q: %w", v, err)
	}

	return &Version{
		Major: int(ver.Major),
		Minor: int(ver.Minor),
		Patch: int(ver.Patch),
	}, nil
}

// normalizeVersion removes 'v' prefix if present
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if match := gitDescribeVersionPattern.FindStringSubmatch(v); len(match) == 2 {
		return match[1]
	}
	return v
}
