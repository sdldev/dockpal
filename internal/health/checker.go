package health

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"syscall"
	"time"

	"github.com/moby/moby/client"
	"go.etcd.io/bbolt"
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
	Status    Status                   `json:"status"`
	Timestamp string                   `json:"timestamp"`
	Uptime    string                   `json:"uptime"`
	Version   string                   `json:"version"`
	Checks    map[string]CheckResult   `json:"checks"`
	summary   map[CheckStatus]int      // internal summary
}

// Checker performs health checks on various system components
type Checker struct {
	dbPath       string
	dockerClient DockerClient
	startTime    time.Time
	version      string
}

// DockerClient interface to allow for dependency injection
type DockerClient interface {
	Ping(ctx context.Context, options client.PingOptions) (client.PingResult, error)
	Close() error
}

// NewChecker creates a new health checker
func NewChecker(dbPath string, dockerClient DockerClient, version string) *Checker {
	return &Checker{
		dbPath:       dbPath,
		dockerClient: dockerClient,
		startTime:    time.Now(),
		version:      version,
	}
}

// CheckHealth performs all health checks and returns the overall health status
func (c *Checker) CheckHealth(ctx context.Context) *HealthResponse {
	response := &HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(c.startTime).String(),
		Version:   c.version,
		Checks:    make(map[string]CheckResult),
		summary:   make(map[CheckStatus]int),
	}

	// Perform all health checks
	checks := map[string]func(context.Context) CheckResult{
		"database": c.checkDatabase,
		"docker":   c.checkDocker,
		"disk":     c.checkDiskSpace,
		"memory":   c.checkMemory,
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
	response := &HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(c.startTime).String(),
		Version:   c.version,
		Checks:    make(map[string]CheckResult),
		summary:   make(map[CheckStatus]int),
	}

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
	response := &HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(c.startTime).String(),
		Version:   c.version,
		Checks:    make(map[string]CheckResult),
		summary:   make(map[CheckStatus]int),
	}

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
	
	// Test database connection and basic operations
	db, err := bbolt.Open(c.dbPath, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return CheckResult{
			Status:      CheckFail,
			Description: fmt.Sprintf("Cannot open database: %v", err),
			Duration:    time.Since(start).String(),
		}
	}
	defer db.Close()

	// Test basic read/write operation
	if err := db.Update(func(tx *bbolt.Tx) error {
		bucket := []byte("health_check")
		if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
			return err
		}
		return tx.Bucket(bucket).Put([]byte("test"), []byte("ok"))
	}); err != nil {
		return CheckResult{
			Status:      CheckFail,
			Description: fmt.Sprintf("Database write test failed: %v", err),
			Duration:    time.Since(start).String(),
		}
	}

	// Test read operation
	if err := db.View(func(tx *bbolt.Tx) error {
		val := tx.Bucket([]byte("health_check")).Get([]byte("test"))
		if string(val) != "ok" {
			return fmt.Errorf("read test failed")
		}
		return nil
	}); err != nil {
		return CheckResult{
			Status:      CheckFail,
			Description: fmt.Sprintf("Database read test failed: %v", err),
			Duration:    time.Since(start).String(),
		}
	}

	// Clean up test data
	db.Update(func(tx *bbolt.Tx) error {
		return tx.DeleteBucket([]byte("health_check"))
	})

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

// checkDiskSpace checks available disk space
func (c *Checker) checkDiskSpace(ctx context.Context) CheckResult {
	start := time.Now()
	
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return CheckResult{
			Status:      CheckWarn,
			Description: fmt.Sprintf("Cannot check disk space: %v", err),
			Duration:    time.Since(start).String(),
		}
	}

	// Calculate available space in bytes and MB
	availableBytes := stat.Bavail * uint64(stat.Bsize)
	availableMB := availableBytes / (1024 * 1024)
	totalBytes := stat.Blocks * uint64(stat.Bsize)
	totalMB := totalBytes / (1024 * 1024)
	usedPercent := float64(totalBytes-availableBytes) / float64(totalBytes) * 100

	details := map[string]interface{}{
		"available_mb": availableMB,
		"total_mb":     totalMB,
		"used_percent": usedPercent,
	}

	// Determine status based on available space
	status := CheckPass
	description := "Disk space OK"
	
	if availableMB < 100 { // Less than 100MB
		status = CheckFail
		description = "Critically low disk space"
	} else if availableMB < 500 { // Less than 500MB
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

// checkMemory checks available system memory
func (c *Checker) checkMemory(ctx context.Context) CheckResult {
	start := time.Now()
	
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Get system memory (this is a simplified approach)
	// In a production environment, you might want to use more sophisticated methods
	availableMB := (m.Sys - m.Alloc) / (1024 * 1024)
	allocMB := m.Alloc / (1024 * 1024)
	sysMB := m.Sys / (1024 * 1024)

	details := map[string]interface{}{
		"available_mb": availableMB,
		"allocated_mb": allocMB,
		"system_mb":    sysMB,
		"gc_cycles":    m.NumGC,
	}

	// Determine status based on available memory
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