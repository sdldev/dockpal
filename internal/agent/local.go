package agent

import (
	"context"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sdldev/dockpal/internal/docker"
)

// LocalClient wraps the docker.Client to implement the AgentClient interface
// for the local Docker instance.
type LocalClient struct {
	dockerClient *docker.Client
}

// NewLocalClient creates a new LocalClient wrapping the provided docker.Client.
func NewLocalClient(client *docker.Client) *LocalClient {
	return &LocalClient{
		dockerClient: client,
	}
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

func (c *LocalClient) DeployCompose(ctx context.Context, name, composeYAML string, registryAuths map[string]string) error {
	var authFn docker.AuthHeaderFunc
	if len(registryAuths) > 0 {
		authFn = getAuthHeader(registryAuths)
	}
	return c.dockerClient.DeployCompose(ctx, name, composeYAML, authFn)
}

func (c *LocalClient) DeployComposeStreamed(ctx context.Context, name, composeYAML string, session *docker.DeploySession, registryAuths map[string]string) error {
	var authFn docker.AuthHeaderFunc
	if len(registryAuths) > 0 {
		authFn = getAuthHeader(registryAuths)
	}
	return c.dockerClient.DeployComposeStreamed(ctx, name, composeYAML, session, authFn)
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
	_, usedRAM := getMemoryInfo()

	var stat syscall.Statfs_t
	syscall.Statfs("/", &stat)

	totalDisk := stat.Blocks * uint64(stat.Bsize)
	usedDisk := (stat.Blocks - stat.Bfree) * uint64(stat.Bsize)

	totalRAM, _ := getMemoryInfo()

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