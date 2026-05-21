package metrics

import (
	"context"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sdldev/dockpal/internal/agent"
)

var (
	// Container metrics
	containerCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_containers_total",
			Help: "Total number of containers by status and instance",
		},
		[]string{"instance_id", "status"},
	)

	containerCPU = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_container_cpu_percent",
			Help: "Container CPU usage percentage",
		},
		[]string{"instance_id", "container_id", "container_name", "image"},
	)

	containerMemory = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_container_memory_bytes",
			Help: "Container memory usage in bytes",
		},
		[]string{"instance_id", "container_id", "container_name", "image"},
	)

	containerMemoryPercent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_container_memory_percent",
			Help: "Container memory usage percentage",
		},
		[]string{"instance_id", "container_id", "container_name", "image"},
	)

	containerNetworkRx = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_container_network_rx_bytes_total",
			Help: "Container network receive bytes total",
		},
		[]string{"instance_id", "container_id", "container_name", "image"},
	)

	containerNetworkTx = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_container_network_tx_bytes_total",
			Help: "Container network transmit bytes total",
		},
		[]string{"instance_id", "container_id", "container_name", "image"},
	)

	// Host metrics
	hostCPU = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_host_cpu_percent",
			Help: "Host CPU usage percentage",
		},
		[]string{"instance_id", "hostname", "os"},
	)

	hostMemory = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_host_memory_bytes",
			Help: "Host memory usage in bytes",
		},
		[]string{"instance_id", "hostname", "os", "type"},
	)

	hostDisk = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_host_disk_bytes",
			Help: "Host disk usage in bytes",
		},
		[]string{"instance_id", "hostname", "os", "type"},
	)

	// HTTP request metrics
	httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dockpal_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dockpal_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint", "status"},
	)

	// Build info
	buildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dockpal_build_info",
			Help: "Build information about Dockpal",
		},
		[]string{"version", "go_version"},
	)
)

// MetricsCollector handles collecting and exposing Prometheus metrics
type MetricsCollector struct {
	agentMgr      *agent.Manager
	version       string
	updateInterval time.Duration
	mu           sync.RWMutex
	running      bool
	stopCh       chan struct{}
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(agentMgr *agent.Manager, version string) *MetricsCollector {
	return &MetricsCollector{
		agentMgr:       agentMgr,
		version:        version,
		updateInterval: 15 * time.Second, // Collect metrics every 15 seconds
		stopCh:         make(chan struct{}),
	}
}

// RegisterMetrics registers all Prometheus metrics
func RegisterMetrics(version string) error {
	// Set build info
	buildInfo.WithLabelValues(version, runtime.Version()).Set(1)

	// Register all metrics
	prometheus.MustRegister(
		containerCount,
		containerCPU,
		containerMemory,
		containerMemoryPercent,
		containerNetworkRx,
		containerNetworkTx,
		hostCPU,
		hostMemory,
		hostDisk,
		httpRequests,
		httpRequestDuration,
		buildInfo,
	)

	return nil
}

// Start begins the metrics collection loop
func (mc *MetricsCollector) Start() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.running {
		return
	}

	mc.running = true
	go mc.collectLoop()
	log.Println("Metrics collector started")
}

// Stop stops the metrics collection loop
func (mc *MetricsCollector) Stop() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if !mc.running {
		return
	}

	close(mc.stopCh)
	mc.running = false
	log.Println("Metrics collector stopped")
}

// collectLoop runs the metrics collection loop
func (mc *MetricsCollector) collectLoop() {
	ticker := time.NewTicker(mc.updateInterval)
	defer ticker.Stop()

	// Initial collection
	mc.collectMetrics()

	for {
		select {
		case <-ticker.C:
			mc.collectMetrics()
		case <-mc.stopCh:
			return
		}
	}
}

// collectMetrics gathers all metrics from all instances
func (mc *MetricsCollector) collectMetrics() {
	instances, err := mc.agentMgr.ListInstances()
	if err != nil {
		log.Printf("Failed to list instances for metrics: %v", err)
		return
	}

	for _, instance := range instances {
		go mc.collectInstanceMetrics(instance.ID)
	}
}

// collectInstanceMetrics collects metrics for a specific instance
func (mc *MetricsCollector) collectInstanceMetrics(instanceID string) {
	client, err := mc.agentMgr.GetClient(instanceID)
	if err != nil {
		log.Printf("Failed to get client for instance %s: %v", instanceID, err)
		return
	}

	// Collect container metrics
	mc.collectContainerMetrics(instanceID, client)

	// Collect host metrics
	mc.collectHostMetrics(instanceID, client)
}

// collectContainerMetrics collects container-related metrics
func (mc *MetricsCollector) collectContainerMetrics(instanceID string, client agent.AgentClient) {
	containers, err := client.ListContainers(context.Background(), true)
	if err != nil {
		log.Printf("Failed to list containers for instance %s: %v", instanceID, err)
		return
	}

	// Count containers by status
	statusCount := make(map[string]int)
	for _, container := range containers {
		statusCount[container.State]++
		
		// Collect per-container stats
		go mc.collectContainerStats(instanceID, container.ID, container.Name, container.Image, client)
	}

	// Update container count metrics
	for status, count := range statusCount {
		containerCount.WithLabelValues(instanceID, status).Set(float64(count))
	}
}

// collectContainerStats collects stats for a specific container
func (mc *MetricsCollector) collectContainerStats(instanceID, containerID, containerName, image string, client agent.AgentClient) {
	stats, err := client.GetContainerStats(context.Background(), containerID)
	if err != nil {
		// Container might be stopped or not exist
		return
	}

	containerCPU.WithLabelValues(instanceID, containerID, containerName, image).Set(stats.CPUPercent)
	containerMemory.WithLabelValues(instanceID, containerID, containerName, image).Set(float64(stats.MemoryUsage))
	containerMemoryPercent.WithLabelValues(instanceID, containerID, containerName, image).Set(stats.MemoryPercent)
	containerNetworkRx.WithLabelValues(instanceID, containerID, containerName, image).Set(float64(stats.NetworkRx))
	containerNetworkTx.WithLabelValues(instanceID, containerID, containerName, image).Set(float64(stats.NetworkTx))
}

// collectHostMetrics collects host-related metrics
func (mc *MetricsCollector) collectHostMetrics(instanceID string, client agent.AgentClient) {
	// Get host info for labels
	info, err := client.GetHostInfo(context.Background())
	if err != nil {
		log.Printf("Failed to get host info for instance %s: %v", instanceID, err)
		return
	}

	// Get host stats
	stats, err := client.GetHostStats(context.Background())
	if err != nil {
		log.Printf("Failed to get host stats for instance %s: %v", instanceID, err)
		return
	}

	labels := []string{instanceID, info.Hostname, info.OS}

	hostCPU.WithLabelValues(labels...).Set(stats.CPUPercent)
	hostMemory.WithLabelValues(append(labels, "used")...).Set(float64(stats.UsedRAM))
	hostMemory.WithLabelValues(append(labels, "total")...).Set(float64(stats.TotalRAM))
	hostDisk.WithLabelValues(append(labels, "used")...).Set(float64(stats.UsedDisk))
	hostDisk.WithLabelValues(append(labels, "total")...).Set(float64(stats.TotalDisk))
}

// MetricsMiddleware returns Gin middleware for HTTP request metrics
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Process request
		c.Next()

		// Record metrics after request completes
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		method := c.Request.Method
		endpoint := c.FullPath()

		if endpoint == "" {
			endpoint = "unknown"
		}

		httpRequests.WithLabelValues(method, endpoint, status).Inc()
		httpRequestDuration.WithLabelValues(method, endpoint, status).Observe(duration)
	}
}

// Handler returns the Prometheus metrics HTTP handler
func Handler() http.Handler {
	return promhttp.Handler()
}