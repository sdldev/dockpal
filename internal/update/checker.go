// Package update implements Dockpal's system self-update support: a background
// release checker that polls GitHub for the latest published release, and a
// manager that turns an admin's "update now" request into a trigger file the
// privileged systemd updater unit consumes.
//
// The design keeps the running process unprivileged. The panel (user `dockpal`,
// systemd-hardened with ProtectSystem=strict and NoNewPrivileges=yes) cannot
// replace /usr/local/bin/dockpal itself, so it only writes a trigger file under
// its one writable tree (/opt/dockpal). A root-owned .path unit watches for the
// file and runs update.sh, which performs the verified swap, health check and
// rollback. See update.sh and scripts/dockpal-update-helper.sh.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// defaultCheckInterval is how often the checker polls GitHub for a newer
// release when DOCKPAL_UPDATE_CHECK_INTERVAL is unset.
const defaultCheckInterval = 6 * time.Hour

// settingsKeyLatestRelease is the settings-bucket key holding the cached
// ReleaseInfo JSON so the UI stays fast across restarts.
const settingsKeyLatestRelease = "system_update_latest_release"

// ReleaseInfo describes the latest published upstream release.
type ReleaseInfo struct {
	// Version is the normalized (no leading "v") release tag, e.g. "2.1.0".
	Version string `json:"version"`
	// Tag is the raw GitHub tag, e.g. "v2.1.0".
	Tag string `json:"tag"`
	// Changelog is the release body (markdown), trimmed for the UI.
	Changelog string `json:"changelog,omitempty"`
	// ChangelogURL links to the release page on GitHub.
	ChangelogURL string `json:"changelog_url,omitempty"`
	// PublishedAt is the release publish timestamp (Unix seconds).
	PublishedAt int64 `json:"published_at,omitempty"`
	// CheckedAt is when the checker last successfully fetched (Unix seconds).
	CheckedAt int64 `json:"checked_at"`
}

// Checker periodically queries the GitHub Releases API for the newest release
// and caches the result in memory and in the settings bucket. It follows the
// Start/Stop lifecycle used by the backup scheduler and image update monitor.
type Checker struct {
	database *db.DB
	repo     string
	client   *http.Client
	interval time.Duration
	// apiBase is overridable for tests; defaults to https://api.github.com.
	apiBase string

	mu      sync.RWMutex
	latest  *ReleaseInfo
	running bool
	stopCh  chan struct{}
}

// NewChecker builds a Checker. interval <= 0 disables the background ticker
// (CheckNow remains usable). repo defaults via envRepo. apiBase is normally
// empty (production); tests pass an httptest URL.
func NewChecker(database *db.DB, repo, apiBase string, interval time.Duration) *Checker {
	if repo == "" {
		repo = envRepo()
	}
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	return &Checker{
		database: database,
		repo:     repo,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
			},
		},
		interval: interval,
		apiBase:  apiBase,
	}
}

// envRepo returns the GitHub repo to poll, honoring DOCKPAL_REPO.
func envRepo() string {
	if v := strings.TrimSpace(os.Getenv("DOCKPAL_REPO")); v != "" {
		return v
	}
	return "sdldev/dockpal"
}

// EnvCheckInterval reads DOCKPAL_UPDATE_CHECK_INTERVAL (Go duration or plain
// hours). "0" disables the background checker. Defaults to 6h.
func EnvCheckInterval() time.Duration {
	v := strings.TrimSpace(os.Getenv("DOCKPAL_UPDATE_CHECK_INTERVAL"))
	if v == "" {
		return defaultCheckInterval
	}
	if v == "0" {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	// Accept a bare number as hours for operator convenience.
	if h, err := strconv.Atoi(v); err == nil && h >= 0 {
		return time.Duration(h) * time.Hour
	}
	slog.Warn("invalid DOCKPAL_UPDATE_CHECK_INTERVAL, using default", "value", v, "default", defaultCheckInterval)
	return defaultCheckInterval
}

// EnvEnabled reports whether the UI-driven update feature is on
// (DOCKPAL_UPDATE_ENABLED, default true).
func EnvEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("DOCKPAL_UPDATE_ENABLED")))
	return v != "0" && v != "false" && v != "no"
}

// Latest returns the most recently cached release, or nil if never fetched.
func (c *Checker) Latest() *ReleaseInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.latest == nil {
		return nil
	}
	cp := *c.latest
	return &cp
}

// Start loads any persisted cache, performs an immediate check, then ticks at
// the configured interval. A non-positive interval means no background work.
func (c *Checker) Start(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.stopCh = make(chan struct{})
	c.mu.Unlock()

	c.loadPersisted()
	if c.interval <= 0 {
		slog.Info("release checker disabled", "component", "update")
		return
	}
	go c.run(ctx)
	slog.Info("release checker started", "component", "update", "interval", c.interval, "repo", c.repo)
}

// Stop halts the background ticker.
func (c *Checker) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	c.running = false
	if c.stopCh != nil {
		close(c.stopCh)
		c.stopCh = nil
	}
}

func (c *Checker) run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	// Immediate first check so the UI has data soon after boot.
	c.CheckNow(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.CheckNow(ctx)
		}
	}
}

// CheckNow performs a single fetch, updating the cache on success. It is safe
// to call concurrently and is exposed for the "Check now" admin action. A
// failure never clears a previously cached good result.
func (c *Checker) CheckNow(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	info, err := c.fetchLatest(ctx)
	if err != nil {
		slog.Warn("release check failed", "component", "update", "error", err)
		return
	}
	c.mu.Lock()
	c.latest = info
	c.mu.Unlock()
	c.persist(info)
	slog.Info("release check completed", "component", "update", "latest", info.Version)
}

// ghRelease is the subset of the GitHub release payload we consume.
type ghRelease struct {
	TagName     string `json:"tag_name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

func (c *Checker) fetchLatest(ctx context.Context) (*ReleaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.apiBase, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "dockpal-update-checker")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("github rate limited (status %d, remaining %s)", resp.StatusCode, resp.Header.Get("X-RateLimit-Remaining"))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases returned status %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	if rel.Draft {
		return nil, fmt.Errorf("latest release is a draft")
	}

	info := &ReleaseInfo{
		Version:      normalizeVersion(rel.TagName),
		Tag:          rel.TagName,
		Changelog:    truncate(rel.Body, 4000),
		ChangelogURL: rel.HTMLURL,
		PublishedAt:  parseGitHubTime(rel.PublishedAt),
		CheckedAt:    time.Now().Unix(),
	}
	if info.Version == "" {
		return nil, fmt.Errorf("release has empty tag_name")
	}
	return info, nil
}

// persist writes the cache to the settings bucket (best effort).
func (c *Checker) persist(info *ReleaseInfo) {
	if c.database == nil {
		return
	}
	data, err := json.Marshal(info)
	if err != nil {
		return
	}
	if err := c.database.SetSetting(settingsKeyLatestRelease, data); err != nil {
		slog.Warn("failed to persist release cache", "component", "update", "error", err)
	}
}

// loadPersisted restores the cache from the settings bucket on boot so the UI
// has data before the first successful network check.
func (c *Checker) loadPersisted() {
	if c.database == nil {
		return
	}
	data, err := c.database.GetSetting(settingsKeyLatestRelease)
	if err != nil {
		return
	}
	var info ReleaseInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return
	}
	c.mu.Lock()
	c.latest = &info
	c.mu.Unlock()
}

// normalizeVersion strips a leading "Dockpal " prefix and "v".
func normalizeVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "Dockpal ")
	raw = strings.TrimPrefix(raw, "v")
	return raw
}

// CompareVersions compares two dotted-numeric versions (after normalization).
// It returns -1, 0 or 1. Non-numeric segments compare as 0, which is adequate
// for Dockpal's own release tags (all MAJOR.MINOR.PATCH).
func CompareVersions(a, b string) int {
	pa := strings.Split(normalizeVersion(a), ".")
	pb := strings.Split(normalizeVersion(b), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(numericPrefix(pa[i]))
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(numericPrefix(pb[i]))
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// numericPrefix returns the leading digits of a version segment, dropping any
// pre-release/build suffix (e.g. "2-rc1" -> "2").
func numericPrefix(s string) string {
	for i, r := range s {
		if r < '0' || r > '9' {
			return s[:i]
		}
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// parseGitHubTime parses RFC3339 timestamps from the API; zero on failure.
func parseGitHubTime(s string) int64 {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix()
	}
	return 0
}
