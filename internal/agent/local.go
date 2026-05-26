package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
)

// LocalAppOps bundles the dependencies the LocalClient needs to satisfy the
// auto-image-update pieces of the AgentClient interface (task 6.2).
//
// Worker drives the manual TriggerAppUpdate pipeline; Monitor and Store
// are passed to (*docker.Client).ListApps so the local /apps response
// includes update-availability and the most recent attempt; SetAutoUpdate
// is a closure provided by routes.go that knows how to rewrite the compose
// YAML, persist the db.Service, and redeploy with forcePull=false. Keeping
// SetAutoUpdate as a callback (rather than threading *db.DB and
// *registry.Manager into the agent package) keeps imports minimal and lets
// tests substitute a recorder.
//
// Any field may be nil; methods that need a missing dependency surface
// errAppOpsNotWired so callers can detect a mis-wired process.
type LocalAppOps struct {
	Worker        *docker.AutoUpdateWorker
	Monitor       *docker.ImageUpdateMonitor
	Store         db.AppUpdateStore
	SetAutoUpdate func(ctx context.Context, app string, enabled bool) error
}

// LocalClient wraps the docker.Client to implement the AgentClient interface
// for the local Docker instance.
type LocalClient struct {
	dockerClient *docker.Client

	// appOpsMu guards appOps so WireAppOps can be called concurrently with
	// in-flight RPCs (e.g. an HTTP test that wires the deps after starting
	// a background request). Reads in the hot paths (ListApps, etc.) take
	// the read lock and snapshot the struct; writes in WireAppOps take the
	// write lock.
	appOpsMu sync.RWMutex
	appOps   LocalAppOps
}

// NewLocalClient creates a new LocalClient wrapping the provided docker.Client.
//
// The auto-image-update operations (ListApps, ListAppUpdates, GetAppUpdate,
// TriggerAppUpdate, SetAppAutoUpdate) return errAppOpsNotWired until
// WireAppOps is called with the matching dependencies, so the AgentClient
// interface stays satisfied even before routes.go has constructed the
// worker, store, and registry plumbing.
func NewLocalClient(client *docker.Client) *LocalClient {
	return &LocalClient{
		dockerClient: client,
	}
}

// WireAppOps injects the auto-image-update dependencies into a previously
// constructed LocalClient. It is called from routes.go after the
// AutoUpdateWorker, ImageUpdateMonitor, AppUpdateStore, and the registry
// manager have been wired so the LocalClient.* app methods can delegate to
// them. Calling WireAppOps multiple times overwrites the previous
// configuration; passing a zero-value LocalAppOps effectively un-wires the
// app methods and they revert to errAppOpsNotWired.
func (c *LocalClient) WireAppOps(ops LocalAppOps) {
	c.appOpsMu.Lock()
	defer c.appOpsMu.Unlock()
	c.appOps = ops
}

// snapshotAppOps returns a copy of the wired app ops so callers can read
// fields without holding the lock for the duration of a Docker RPC. The
// LocalAppOps fields are pointers / interfaces / closures, so the copy is
// cheap and safe.
func (c *LocalClient) snapshotAppOps() LocalAppOps {
	c.appOpsMu.RLock()
	defer c.appOpsMu.RUnlock()
	return c.appOps
}

// Container operations

func (c *LocalClient) ListContainers(ctx context.Context, all bool) ([]docker.ContainerInfo, error) {
	return c.dockerClient.ListContainers(ctx, all)
}

func (c *LocalClient) InspectContainer(ctx context.Context, id string) (*docker.ContainerDetail, error) {
	return c.dockerClient.InspectContainer(ctx, id)
}

func (c *LocalClient) StartContainer(ctx context.Context, id string) error {
	return c.dockerClient.StartContainer(ctx, id)
}

func (c *LocalClient) StopContainer(ctx context.Context, id string) error {
	return c.dockerClient.StopContainer(ctx, id)
}

func (c *LocalClient) RestartContainer(ctx context.Context, id string) error {
	return c.dockerClient.RestartContainer(ctx, id)
}

func (c *LocalClient) RemoveContainer(ctx context.Context, id string, force bool) error {
	return c.dockerClient.RemoveContainer(ctx, id, force)
}

func (c *LocalClient) EditContainer(ctx context.Context, id string, req docker.ContainerEditRequest) (*docker.ContainerDetail, error) {
	return c.dockerClient.EditContainer(ctx, id, req)
}

func (c *LocalClient) UpdateContainerImage(ctx context.Context, id string, registryAuth string) (*docker.ContainerDetail, error) {
	return c.dockerClient.UpdateContainerImage(ctx, id, registryAuth)
}

func (c *LocalClient) GetContainerStats(ctx context.Context, id string) (*docker.ContainerStats, error) {
	return c.dockerClient.GetContainerStats(ctx, id)
}

func (c *LocalClient) ContainerLogs(ctx context.Context, id string, tail string) (io.ReadCloser, error) {
	return c.dockerClient.ContainerLogs(ctx, id, tail)
}

// Compose operations

// getAuthHeader creates an AuthHeaderFunc from a registryAuths map.
// This is used to convert the map-based auth passed from routes into
// the function-based auth expected by DeployCompose.
func getAuthHeader(registryAuths map[string]string) func(imageRef string) (string, error) {
	return func(imageRef string) (string, error) {
		// Extract domain from image reference
		domain := extractImageDomain(imageRef)
		if auth, ok := registryAuths[domain]; ok {
			return auth, nil
		}
		// Fallback: try the image itself as the key
		if auth, ok := registryAuths[imageRef]; ok {
			return auth, nil
		}
		return "", nil
	}
}

// extractImageDomain extracts the registry domain from an image reference.
func extractImageDomain(image string) string {
	parts := strings.SplitN(image, "/", 2)
	if len(parts) >= 2 && strings.Contains(parts[0], ".") {
		return parts[0]
	}
	return "docker.io"
}

func (c *LocalClient) DeployCompose(ctx context.Context, name, composeYAML string, registryAuths map[string]string, forcePull bool) error {
	var authFn docker.AuthHeaderFunc
	if len(registryAuths) > 0 {
		authFn = getAuthHeader(registryAuths)
	}
	return c.dockerClient.DeployCompose(ctx, name, composeYAML, authFn, forcePull)
}

func (c *LocalClient) DeployComposeStreamed(ctx context.Context, name, composeYAML string, session *docker.DeploySession, registryAuths map[string]string, forcePull bool) error {
	var authFn docker.AuthHeaderFunc
	if len(registryAuths) > 0 {
		authFn = getAuthHeader(registryAuths)
	}
	return c.dockerClient.DeployComposeStreamed(ctx, name, composeYAML, session, authFn, forcePull)
}

// Image operations

func (c *LocalClient) ListImages(ctx context.Context) ([]docker.ImageInfo, error) {
	return c.dockerClient.ListImages(ctx)
}

func (c *LocalClient) PullImage(ctx context.Context, image string) error {
	return c.dockerClient.PullImage(ctx, image)
}

func (c *LocalClient) PullImageWithAuth(ctx context.Context, image, registryAuth string) error {
	return c.dockerClient.PullImageWithAuth(ctx, image, registryAuth)
}

func (c *LocalClient) RemoveImage(ctx context.Context, id string) error {
	return c.dockerClient.RemoveImage(ctx, id)
}

func (c *LocalClient) CheckImageUpdate(ctx context.Context, image string) (*docker.ImageUpdateResult, error) {
	return c.dockerClient.CheckImageUpdate(ctx, image, "")
}

func (c *LocalClient) ForcePullImage(ctx context.Context, image, registryAuth string) error {
	return c.dockerClient.ForcePullImage(ctx, image, registryAuth)
}

// App auto-update operations
//
// The methods below satisfy the AgentClient interface for the local
// instance. They delegate to the dependencies wired by WireAppOps:
//
//   - ListApps    → dockerClient.ListApps(ctx, monitor, store)
//   - ListAppUpdates / GetAppUpdate → store
//   - TriggerAppUpdate → worker.TriggerApp (with bypassCooldown=true and
//     bypassWindow=true, matching the manual-trigger semantics of POST
//     /apps/:name/update). The implementation kicks the pipeline off in a
//     goroutine and polls the store for the new attempt id, mirroring the
//     handler-level pattern so callers (the instance-scoped handler for
//     remote instances and tests that route through the AgentClient
//     interface) get a quick response with the attempt id rather than
//     blocking on the full pull → recreate → verify pipeline.
//   - SetAppAutoUpdate → SetAutoUpdate callback (a closure provided by
//     routes.go that performs the compose-rewrite + redeploy).
//
// Methods return errAppOpsNotWired when the corresponding dependency is
// nil, which lets test setups partially wire the LocalClient and lets the
// production process surface a clear error instead of panicking.
var errAppOpsNotWired = errors.New("local app auto-update not wired")

func (c *LocalClient) ListApps(ctx context.Context) ([]docker.AppSummary, error) {
	ops := c.snapshotAppOps()
	if c.dockerClient == nil {
		return nil, errAppOpsNotWired
	}
	apps, err := c.dockerClient.ListApps(ctx, ops.Monitor, ops.Store)
	if err != nil {
		return nil, err
	}
	if apps == nil {
		apps = []docker.AppSummary{}
	}
	for i := range apps {
		apps[i].InstanceID = "local"
	}
	return apps, nil
}

func (c *LocalClient) ListAppUpdates(ctx context.Context, app string, limit int) ([]db.AppUpdateRecord, error) {
	ops := c.snapshotAppOps()
	if ops.Store == nil {
		return nil, errAppOpsNotWired
	}
	recs, err := ops.Store.ListAppUpdates(app, limit)
	if err != nil {
		return nil, err
	}
	if recs == nil {
		recs = []db.AppUpdateRecord{}
	}
	return recs, nil
}

func (c *LocalClient) GetAppUpdate(ctx context.Context, attemptID string) (*db.AppUpdateRecord, error) {
	ops := c.snapshotAppOps()
	if ops.Store == nil {
		return nil, errAppOpsNotWired
	}
	return ops.Store.GetAppUpdate(attemptID)
}

// TriggerAppUpdate runs the manual auto-update pipeline for one app via the
// wired AutoUpdateWorker. It mirrors POST /apps/:name/update:
//
//  1. Snapshot the latest attempt id so a new record can be detected.
//  2. Spawn worker.TriggerApp in a goroutine with bypassCooldown=true and
//     bypassWindow=true, since this is a manual override (R6.1).
//  3. Poll the store every 25ms for up to 5 seconds for a new attempt id.
//     The first stage event ("pulling") is persisted near the top of the
//     pipeline so the new id is normally available within a few ms; the
//     5-second cap is a safety net for slow disks or contention.
//  4. Return the new attempt id, or surface the worker error if it
//     finished before the polling loop saw a new record.
//
// The pipeline goroutine uses context.Background() so it outlives the
// caller's ctx — the worker's own Stop() path is the cancellation source.
// docker.ErrUpdateAlreadyRunning is bubbled up verbatim so the
// instance-scoped handler can map it to HTTP 409.
func (c *LocalClient) TriggerAppUpdate(ctx context.Context, app string) (string, error) {
	ops := c.snapshotAppOps()
	if ops.Worker == nil || ops.Store == nil {
		return "", errAppOpsNotWired
	}
	if app == "" {
		return "", fmt.Errorf("trigger app update: empty app")
	}

	var prevAttempt string
	if recs, err := ops.Store.ListAppUpdates(app, 1); err == nil && len(recs) > 0 {
		prevAttempt = recs[0].AttemptID
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- ops.Worker.TriggerApp(context.Background(), app, true, true, "user:agent")
	}()

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				// Bubble up the worker error verbatim so callers can match
				// docker.ErrUpdateAlreadyRunning by substring.
				return "", err
			}
			if recs, lerr := ops.Store.ListAppUpdates(app, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
				return recs[0].AttemptID, nil
			}
			// TriggerApp returned without error but produced no new record
			// (cooldown / window skipped despite bypass=true, or the
			// pipeline short-circuited before its first save). Treat as
			// success with an empty attempt id so callers can distinguish
			// from the "in flight" case.
			return "", nil
		case <-ticker.C:
			if recs, lerr := ops.Store.ListAppUpdates(app, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
				return recs[0].AttemptID, nil
			}
		case <-timeout.C:
			if recs, lerr := ops.Store.ListAppUpdates(app, 1); lerr == nil && len(recs) > 0 && recs[0].AttemptID != prevAttempt {
				return recs[0].AttemptID, nil
			}
			return "", fmt.Errorf("trigger did not produce a record in time")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (c *LocalClient) SetAppAutoUpdate(ctx context.Context, app string, enabled bool) error {
	ops := c.snapshotAppOps()
	if ops.SetAutoUpdate == nil {
		return errAppOpsNotWired
	}
	return ops.SetAutoUpdate(ctx, app, enabled)
}

// Host operations

// GetHostInfo returns system information about the local host.
func (c *LocalClient) GetHostInfo(ctx context.Context) (*HostInfo, error) {
	hostname := getHostname()

	totalRAM, _ := getMemoryInfo()

	dockerVersion, err := c.dockerClient.ServerVersion(ctx)
	if err != nil {
		dockerVersion = ""
	}

	return &HostInfo{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		CPUCores:      runtime.NumCPU(),
		TotalMemory:   totalRAM,
		DockerVersion: dockerVersion,
	}, nil
}

// GetHostStats returns real-time resource usage statistics for the local host.
func (c *LocalClient) GetHostStats(ctx context.Context) (*HostStats, error) {
	cpuPercent := getCPUPercent()
	totalRAM, usedRAM := getMemoryInfo()

	var stat syscall.Statfs_t
	syscall.Statfs("/", &stat)

	totalDisk := stat.Blocks * uint64(stat.Bsize)
	usedDisk := (stat.Blocks - stat.Bfree) * uint64(stat.Bsize)

	return &HostStats{
		CPUPercent: cpuPercent,
		UsedRAM:    usedRAM,
		TotalRAM:   totalRAM,
		UsedDisk:   usedDisk,
		TotalDisk:  totalDisk,
	}, nil
}

// Connection

func (c *LocalClient) Ping(ctx context.Context) error {
	return c.dockerClient.Ping(ctx)
}

// Close is a no-op for LocalClient since the docker.Client lifecycle
// is managed by main.go.
func (c *LocalClient) Close() error {
	return nil
}

// === Helper functions moved from routes.go ===

// getHostname returns the system hostname.
func getHostname() string {
	h, _ := os.Hostname()
	return h
}

// getMemoryInfo reads memory information from cgroup (if available) or falls back to /proc/meminfo.
// This correctly reports memory on LXC containers where syscall.Sysinfo returns host memory.
func getMemoryInfo() (total, used uint64) {
	// Try cgroup v2 first
	if data, err := readFile("/sys/fs/cgroup/memory.max"); err == nil {
		s := strings.TrimSpace(string(data))
		if s != "max" {
			if limit, err := strconv.ParseUint(s, 10, 64); err == nil && limit > 0 {
				total = limit
				used = getCgroupMemoryUsage()
				if used > 0 {
					return total, used
				}
			}
		}
	}

	// Try cgroup v1
	if data, err := readFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		s := strings.TrimSpace(string(data))
		if limit, err := strconv.ParseUint(s, 10, 64); err == nil && limit > 0 && limit < (1<<62) {
			total = limit
			if usage, err := readFile("/sys/fs/cgroup/memory/memory.usage_in_bytes"); err == nil {
				s2 := strings.TrimSpace(string(usage))
				if u, err := strconv.ParseUint(s2, 10, 64); err == nil {
					return total, u
				}
			}
		}
	}

	// Fall back to /proc/meminfo (works correctly on VPS/bare metal and most containers)
	if data, err := readFile("/proc/meminfo"); err == nil {
		var memTotal, memAvailable uint64
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			val, _ := strconv.ParseUint(fields[1], 10, 64)
			val *= 1024 // /proc/meminfo reports in kB
			switch fields[0] {
			case "MemTotal:":
				memTotal = val
			case "MemAvailable:":
				memAvailable = val
			}
		}
		if memTotal > 0 {
			return memTotal, memTotal - memAvailable
		}
	}

	// Last resort: syscall (may report host memory on LXC)
	var sysinfo syscall.Sysinfo_t
	syscall.Sysinfo(&sysinfo)
	return uint64(sysinfo.Totalram), uint64(sysinfo.Totalram) - uint64(sysinfo.Freeram)
}

// getCgroupMemoryUsage reads current memory usage from cgroup v2.
func getCgroupMemoryUsage() uint64 {
	if data, err := readFile("/sys/fs/cgroup/memory.current"); err == nil {
		s := strings.TrimSpace(string(data))
		if val, err := strconv.ParseUint(s, 10, 64); err == nil {
			return val
		}
	}
	return 0
}

// getCPUPercent reads /proc/stat twice with a 200ms interval to compute CPU usage.
func getCPUPercent() float64 {
	read := func() (idle, total uint64) {
		data, err := readFile("/proc/stat")
		if err != nil {
			return 0, 0
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) == 0 {
			return 0, 0
		}
		fields := strings.Fields(lines[0])
		if len(fields) < 5 {
			return 0, 0
		}
		var sum uint64
		for i := 1; i < len(fields); i++ {
			val, _ := strconv.ParseUint(fields[i], 10, 64)
			sum += val
			if i == 4 {
				idle = val
			}
		}
		return idle, sum
	}

	idle1, total1 := read()
	time.Sleep(200 * time.Millisecond)
	idle2, total2 := read()

	totalDelta := float64(total2 - total1)
	if totalDelta == 0 {
		return 0
	}
	idleDelta := float64(idle2 - idle1)
	return (1.0 - idleDelta/totalDelta) * 100.0
}

// readFile is a wrapper around os.ReadFile.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
