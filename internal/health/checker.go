package health

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/client"
)

// Status represents the overall health status
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusUnhealthy Status = "unhealthy"
)

// CheckStatus represents the status of an individual health check
type CheckStatus string

const (
	CheckPass CheckStatus = "pass"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

// CheckResult represents the result of an individual health check
type CheckResult struct {
	Status      CheckStatus `json:"status"`
	Description string      `json:"description,omitempty"`
	Duration    string      `json:"duration,omitempty"`
	Details     interface{} `json:"details,omitempty"`
}

// HealthResponse represents the complete health check response
type HealthResponse struct {
	Status        Status                 `json:"status"`
	Timestamp     string                 `json:"timestamp"`
	Uptime        string                 `json:"uptime"`
	UptimeSeconds float64                `json:"uptime_seconds"`
	UptimeSource string                 `json:"uptime_source"`
	Version       string                 `json:"version"`
	Checks        map[string]CheckResult `json:"checks"`
	summary       map[CheckStatus]int    // internal summary
}

// DBPinger is the interface used by the health checker to verify database connectivity.
type DBPinger interface {
	Ping() error
}

// Checker performs health checks on various system components
type Checker struct {
	db           DBPinger
	dbPath       string
	dataDir      string
	dockerClient DockerClient
	startTime    time.Time
	version      string
}

// DockerClient interface to allow for dependency injection
type DockerClient interface {
	Ping(ctx context.Context, options client.PingOptions) (client.PingResult, error)
	Close() error
}

// NewChecker creates a new health checker.
// db is the already-open database instance; dbPath may be empty when db is provided.
func NewChecker(db DBPinger, dbPath, dataDir string, dockerClient DockerClient, version string) *Checker {
	if dataDir == "" {
		dataDir = "/"
	}
	return &Checker{
		db:           db,
		dbPath:       dbPath,
		dataDir:      dataDir,
		dockerClient: dockerClient,
		startTime:    time.Now(),
		version:      version,
	}
}

// newHealthResponse builds a fresh response with the current uptime filled in.
// "Uptime" means how long the *machine* has been up — a "System Status"
// panel that reported the Go process' own age would read "8 minutes" right
// after every server restart. We fall back to process uptime only if the
// host value cannot be read, and record which one we used.
func (c *Checker) newHealthResponse() *HealthResponse {
	elapsed := time.Since(c.startTime)

	uptime := readHostUptimeSeconds()
	uptimeSource := "host"
	if uptime < 0 {
		uptime = elapsed.Seconds()
		uptimeSource = "process"
	}

	return &HealthResponse{
		Status:        StatusHealthy,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Uptime:        time.Duration(uptime * float64(time.Second)).Round(time.Second).String(),
		UptimeSeconds: uptime,
		UptimeSource:  uptimeSource,
		Version:       c.version,
		Checks:        make(map[string]CheckResult),
		summary:       make(map[CheckStatus]int),
	}
}

// readHostUptimeSeconds returns seconds since the machine booted, read from
// /proc/uptime (Linux). Returns -1 when the figure is unavailable.
func readHostUptimeSeconds() float64 {
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			if secs, err := strconv.ParseFloat(fields[0], 64); err == nil {
				return secs
			}
		}
	}
	return -1
}

// CheckHealth performs all health checks and returns the overall health status
func (c *Checker) CheckHealth(ctx context.Context) *HealthResponse {
	response := c.newHealthResponse()

	// Perform all health checks
	checks := map[string]func(context.Context) CheckResult{
		"database":  c.checkDatabase,
		"docker":    c.checkDocker,
		"disk_root": c.checkRootDiskSpace,
		"disk_data": c.checkDataDiskSpace,
		"memory":    c.checkMemory,
	}

	for name, checkFunc := range checks {
		result := checkFunc(ctx)
		response.Checks[name] = result
		response.summary[result.Status]++
	}

	// Determine overall status based on check results
	if response.summary[CheckFail] > 0 {
		response.Status = StatusUnhealthy
	} else if response.summary[CheckWarn] > 0 {
		response.Status = StatusDegraded
	}

	return response
}

// CheckLiveness performs a basic liveness check
func (c *Checker) CheckLiveness(ctx context.Context) *HealthResponse {
	response := c.newHealthResponse()

	// For liveness, we just check if the application can respond
	// This is a simple check to ensure the process is running
	response.Checks["process"] = CheckResult{
		Status:      CheckPass,
		Description: "Process is running",
		Duration:    "0ms",
	}
	response.summary[CheckPass] = 1

	return response
}

// CheckReadiness performs a readiness check to ensure the service can accept traffic
func (c *Checker) CheckReadiness(ctx context.Context) *HealthResponse {
	response := c.newHealthResponse()

	// For readiness, check critical dependencies
	checks := map[string]func(context.Context) CheckResult{
		"database": c.checkDatabase,
		"docker":   c.checkDocker,
	}

	for name, checkFunc := range checks {
		result := checkFunc(ctx)
		response.Checks[name] = result
		response.summary[result.Status]++
	}

	// Determine overall status
	if response.summary[CheckFail] > 0 {
		response.Status = StatusUnhealthy
	} else if response.summary[CheckWarn] > 0 {
		response.Status = StatusDegraded
	}

	return response
}

// checkDatabase tests database connectivity and basic operations
func (c *Checker) checkDatabase(ctx context.Context) CheckResult {
	start := time.Now()

	if c.db == nil {
		return CheckResult{
			Status:      CheckFail,
			Description: "Database not configured",
			Duration:    time.Since(start).String(),
		}
	}

	if err := c.db.Ping(); err != nil {
		return CheckResult{
			Status:      CheckFail,
			Description: fmt.Sprintf("Database ping failed: %v", err),
			Duration:    time.Since(start).String(),
		}
	}

	return CheckResult{
		Status:      CheckPass,
		Description: "Database connectivity and operations OK",
		Duration:    time.Since(start).String(),
	}
}

// checkDocker tests Docker daemon connectivity
func (c *Checker) checkDocker(ctx context.Context) CheckResult {
	start := time.Now()

	if c.dockerClient == nil {
		return CheckResult{
			Status:      CheckFail,
			Description: "Docker client not initialized",
			Duration:    time.Since(start).String(),
		}
	}

	// Test Docker daemon connectivity with timeout
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := c.dockerClient.Ping(ctx, client.PingOptions{}); err != nil {
		return CheckResult{
			Status:      CheckFail,
			Description: fmt.Sprintf("Docker daemon unreachable: %v", err),
			Duration:    time.Since(start).String(),
		}
	}

	return CheckResult{
		Status:      CheckPass,
		Description: "Docker daemon connectivity OK",
		Duration:    time.Since(start).String(),
	}
}

func (c *Checker) checkRootDiskSpace(ctx context.Context) CheckResult {
	return c.checkDiskSpace(ctx, "/")
}

func (c *Checker) checkDataDiskSpace(ctx context.Context) CheckResult {
	return c.checkDiskSpace(ctx, c.dataDir)
}

// checkDiskSpace checks available disk space for path.
func (c *Checker) checkDiskSpace(ctx context.Context, path string) CheckResult {
	start := time.Now()
	if path == "" {
		path = "/"
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return CheckResult{
			Status:      CheckWarn,
			Description: fmt.Sprintf("Cannot check disk space: %v", err),
			Duration:    time.Since(start).String(),
			Details:     map[string]interface{}{"path": path},
		}
	}

	availableBytes := stat.Bavail * uint64(stat.Bsize)
	availableMB := availableBytes / (1024 * 1024)
	totalBytes := stat.Blocks * uint64(stat.Bsize)
	totalMB := totalBytes / (1024 * 1024)
	usedPercent := float64(totalBytes-availableBytes) / float64(totalBytes) * 100

	details := map[string]interface{}{
		"path":         path,
		"available_mb": availableMB,
		"total_mb":     totalMB,
		"used_percent": usedPercent,
	}

	status := CheckPass
	description := "Disk space OK"

	if availableMB < 100 {
		status = CheckFail
		description = "Critically low disk space"
	} else if availableMB < 500 {
		status = CheckWarn
		description = "Low disk space"
	}

	return CheckResult{
		Status:      status,
		Description: description,
		Duration:    time.Since(start).String(),
		Details:     details,
	}
}

// readAvailableMemoryMB reports available system memory in MB and where the
// figure came from. A container memory ceiling wins over host meminfo, since
// the process cannot use more than its cgroup allows.
func readAvailableMemoryMB() (availableMB int64, source string) {
	// cgroup v2: respect the container's memory ceiling when one is set.
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		limit := strings.TrimSpace(string(raw))
		if limit != "" && limit != "max" {
			if n, err := strconv.ParseInt(limit, 10, 64); err == nil && n > 0 {
				if curRaw, err := os.ReadFile("/sys/fs/cgroup/memory.current"); err == nil {
					if cur, err := strconv.ParseInt(strings.TrimSpace(string(curRaw)), 10, 64); err == nil {
						return (n - cur) / (1024 * 1024), "cgroup-v2"
					}
				}
			}
		}
	}

	// Host memory (also visible inside most containers).
	if meminfo, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(meminfo), "\n") {
			if strings.HasPrefix(line, "MemAvailable:") {
				fields := strings.Fields(line)
				if len(fields) == 3 && fields[2] == "kB" {
					if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						return kb / 1024, "meminfo"
					}
				}
			}
		}
	}

	return 0, "unavailable"
}

// checkMemory checks available system memory.
//
// The health of the process is not the health of the host: runtime.MemStats.Sys
// is virtual memory the Go runtime reserved from the OS, not the RAM the machine
// has left. Judging host memory by (Sys - Alloc) made /health report "critically
// low memory" on a 16 GB host with 6 GB free, because a lightly loaded process
// reserves only a few dozen MB. Read the real figures instead, and only warn
// when they genuinely cannot be read.
func (c *Checker) checkMemory(ctx context.Context) CheckResult {
	start := time.Now()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	allocMB := m.Alloc / (1024 * 1024)

	availableMB, source := readAvailableMemoryMB()

	details := map[string]interface{}{
		"available_mb":  availableMB,
		"allocated_mb":  allocMB,
		"runtime_sys_mb": m.Sys / (1024 * 1024),
		"source":        source,
		"gc_cycles":     m.NumGC,
	}

	if source == "unavailable" {
		return CheckResult{
			Status:      CheckWarn,
			Description: "Host memory unavailable; only process allocation is reported",
			Duration:    time.Since(start).String(),
			Details:     details,
		}
	}

	status := CheckPass
	description := "Memory usage OK"

	if availableMB < 50 { // Less than 50MB available
		status = CheckFail
		description = "Critically low memory"
	} else if availableMB < 100 { // Less than 100MB available
		status = CheckWarn
		description = "Low memory"
	}

	return CheckResult{
		Status:      status,
		Description: description,
		Duration:    time.Since(start).String(),
		Details:     details,
	}
}

// ToJSON converts the health response to JSON
func (h *HealthResponse) ToJSON() ([]byte, error) {
	return json.MarshalIndent(h, "", "  ")
}

// GetHTTPStatus returns appropriate HTTP status code based on health status
func (h *HealthResponse) GetHTTPStatus() int {
	switch h.Status {
	case StatusHealthy:
		return 200
	case StatusDegraded:
		return 200 // Still OK, but with warnings
	case StatusUnhealthy:
		return 503 // Service Unavailable
	default:
		return 500
	}
}
