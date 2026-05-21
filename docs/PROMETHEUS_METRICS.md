# Prometheus Metrics Integration

This document explains how to set up Prometheus to scrape metrics from Dockpal.

## Overview

Dockpal exposes a Prometheus-compatible metrics endpoint at `/api/metrics` that provides:

- Container metrics (CPU, memory, network I/O)
- Host resource metrics (CPU, memory, disk)
- HTTP request metrics (count, duration, error rates)
- Build information

## Available Metrics

### Container Metrics

- `dockpal_containers_total` - Total number of containers by status and instance
  - Labels: `instance_id`, `status`

- `dockpal_container_cpu_percent` - Container CPU usage percentage
  - Labels: `instance_id`, `container_id`, `container_name`, `image`

- `dockpal_container_memory_bytes` - Container memory usage in bytes
  - Labels: `instance_id`, `container_id`, `container_name`, `image`

- `dockpal_container_memory_percent` - Container memory usage percentage
  - Labels: `instance_id`, `container_id`, `container_name`, `image`

- `dockpal_container_network_rx_bytes_total` - Container network receive bytes total
  - Labels: `instance_id`, `container_id`, `container_name`, `image`

- `dockpal_container_network_tx_bytes_total` - Container network transmit bytes total
  - Labels: `instance_id`, `container_id`, `container_name`, `image`

### Host Metrics

- `dockpal_host_cpu_percent` - Host CPU usage percentage
  - Labels: `instance_id`, `hostname`, `os`

- `dockpal_host_memory_bytes` - Host memory usage in bytes
  - Labels: `instance_id`, `hostname`, `os`, `type` (used/total)

- `dockpal_host_disk_bytes` - Host disk usage in bytes
  - Labels: `instance_id`, `hostname`, `os`, `type` (used/total)

### HTTP Metrics

- `dockpal_http_requests_total` - Total number of HTTP requests
  - Labels: `method`, `endpoint`, `status`

- `dockpal_http_request_duration_seconds` - HTTP request duration in seconds
  - Labels: `method`, `endpoint`, `status`

### Build Info

- `dockpal_build_info` - Build information about Dockpal
  - Labels: `version`, `go_version`

## Prometheus Configuration

Add the following job to your Prometheus configuration (`prometheus.yml`):

```yaml
scrape_configs:
  - job_name: 'dockpal'
    static_configs:
      - targets: ['localhost:3012']  # Replace with your Dockpal server address
    metrics_path: '/api/metrics'
    scrape_interval: 15s  # How often to scrape metrics
    scrape_timeout: 10s   # Timeout for scraping
```

### Multi-Instance Setup

If you have multiple Dockpal instances:

```yaml
scrape_configs:
  - job_name: 'dockpal'
    static_configs:
      - targets: 
          - 'dockpal1.example.com:3012'
          - 'dockpal2.example.com:3012'
          - 'dockpal3.example.com:3012'
    metrics_path: '/api/metrics'
    scrape_interval: 15s
    scrape_timeout: 10s
```

### Service Discovery (Optional)

For dynamic environments, you can use service discovery:

```yaml
scrape_configs:
  - job_name: 'dockpal'
    consul_sd_configs:
      - server: 'consul.example.com:8500'
        services: ['dockpal']
    metrics_path: '/api/metrics'
    scrape_interval: 15s
```

## Example Queries

### Container Monitoring

- **Total running containers:**
  ```
  sum(dockpal_containers_total{status="running"})
  ```

- **Container CPU usage by name:**
  ```
  dockpal_container_cpu_percent
  ```

- **Top 10 containers by memory usage:**
  ```
  topk(10, dockpal_container_memory_bytes)
  ```

- **Container memory usage percentage:**
  ```
  dockpal_container_memory_percent
  ```

### Host Monitoring

- **Host CPU usage:**
  ```
  dockpal_host_cpu_percent
  ```

- **Host memory usage percentage:**
  ```
  dockpal_host_memory_bytes{type="used"} / dockpal_host_memory_bytes{type="total"} * 100
  ```

- **Host disk usage percentage:**
  ```
  dockpal_host_disk_bytes{type="used"} / dockpal_host_disk_bytes{type="total"} * 100
  ```

### HTTP Monitoring

- **HTTP request rate:**
  ```
  sum(rate(dockpal_http_requests_total[5m])) by (endpoint)
  ```

- **HTTP error rate:**
  ```
  sum(rate(dockpal_http_requests_total{status=~"5.."}[5m])) by (endpoint)
  ```

- **Average request duration:**
  ```
  histogram_quantile(0.95, sum(rate(dockpal_http_request_duration_seconds_bucket[5m])) by (le, endpoint))
  ```

## Grafana Dashboard

You can create a Grafana dashboard to visualize these metrics. Here are some example panel configurations:

### Container Overview Panel

- **Title:** Container Count by Status
- **Type:** Stat Panel
- **Query:** `sum(dockpal_containers_total) by (status)`

### Host Resources Panel

- **Title:** Host CPU Usage
- **Type:** Gauge Panel
- **Query:** `dockpal_host_cpu_percent`

- **Title:** Host Memory Usage
- **Type:** Gauge Panel
- **Query:** `dockpal_host_memory_bytes{type="used"} / dockpal_host_memory_bytes{type="total"} * 100`

### HTTP Performance Panel

- **Title:** Request Rate
- **Type:** Graph Panel
- **Query:** `sum(rate(dockpal_http_requests_total[5m])) by (endpoint)`

- **Title:** Response Time
- **Type:** Graph Panel
- **Query:** `histogram_quantile(0.95, sum(rate(dockpal_http_request_duration_seconds_bucket[5m])) by (le, endpoint))`

## Alerting Examples

### Prometheus Alerting Rules

```yaml
groups:
  - name: dockpal_alerts
    rules:
      - alert: HighCPUUsage
        expr: dockpal_host_cpu_percent > 80
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High CPU usage on {{ $labels.hostname }}"
          description: "CPU usage is {{ $value }}% on {{ $labels.hostname }}"

      - alert: HighMemoryUsage
        expr: dockpal_host_memory_bytes{type="used"} / dockpal_host_memory_bytes{type="total"} * 100 > 90
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "High memory usage on {{ $labels.hostname }}"
          description: "Memory usage is {{ $value }}% on {{ $labels.hostname }}"

      - alert: ContainerDown
        expr: dockpal_containers_total{status="running"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "No running containers on {{ $labels.instance_id }}"
          description: "Instance {{ $labels.instance_id }} has no running containers"

      - alert: HighErrorRate
        expr: sum(rate(dockpal_http_requests_total{status=~"5.."}[5m])) / sum(rate(dockpal_http_requests_total[5m])) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High HTTP error rate"
          description: "Error rate is {{ $value | humanizePercentage }} over the last 5 minutes"
```

## Security Considerations

- The `/api/metrics` endpoint is publicly accessible by design
- If you need to restrict access, consider using authentication middleware or network policies
- Metrics don't contain sensitive data, but they do reveal container names and images
- For production environments, consider using network segmentation to restrict access

## Troubleshooting

### Metrics Not Available

1. Check if Dockpal is running: `curl http://your-dockpal-server:3012/api/metrics`
2. Check Prometheus configuration: Verify the target URL and metrics path
3. Check Prometheus logs for scraping errors

### Missing Metrics

1. Verify the agent manager is properly initialized
2. Check if containers are running on the instance
3. Verify Docker daemon is accessible

### Performance Impact

- Metrics collection runs every 15 seconds by default
- The overhead is minimal but can be adjusted if needed
- For high-scale deployments, consider increasing the collection interval

## Label-Based Filtering

All metrics support filtering by labels. Common use cases:

- **Filter by instance:** `{instance_id="local"}`
- **Filter by container:** `{container_name="redis"}`
- **Filter by status:** `{status="running"}`
- **Filter by endpoint:** `{endpoint="/api/containers"}`

Example:
```
# CPU usage for a specific container
dockpal_container_cpu_percent{container_name="redis"}

# Memory usage for a specific instance
dockpal_host_memory_bytes{instance_id="production", type="used"}

# Error rate for a specific endpoint
sum(rate(dockpal_http_requests_total{endpoint="/api/containers", status=~"5.."}[5m]))
```

## Integration with Other Tools

The metrics are compatible with any tool that supports Prometheus format:

- **Grafana** - For dashboards and visualization
- **Alertmanager** - For alert routing and notification
- **VictoriaMetrics** - Alternative Prometheus-compatible storage
- **Thanos** - For long-term storage and global querying
- **Cortex** - For multi-tenant Prometheus setup