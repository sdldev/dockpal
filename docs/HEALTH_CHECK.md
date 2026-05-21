# Health Check Endpoints

Dockpal provides comprehensive health check endpoints for monitoring service status, liveness, and readiness. These endpoints follow Kubernetes health check patterns and are suitable for container orchestration platforms.

## Available Endpoints

### Comprehensive Health Check

- **Endpoint**: `/health` or `/healthz`
- **Method**: `GET`
- **Description**: Returns detailed health status of all system components
- **Response**: JSON object with overall status and detailed component checks

### Liveness Probe

- **Endpoints**: `/health/live` or `/livez`
- **Method**: `GET`
- **Description**: Indicates if the application process is running and responsive
- **Response**: JSON object with liveness status (always healthy if process is running)

### Readiness Probe

- **Endpoints**: `/health/ready` or `/readyz`
- **Method**: `GET`
- **Description**: Indicates if the application is ready to serve traffic
- **Response**: JSON object with readiness status based on dependencies

## Response Format

All health endpoints return JSON responses with the following structure:

```json
{
  "status": "healthy|degraded|unhealthy",
  "timestamp": "2023-01-01T00:00:00Z",
  "uptime": "1h30m45s",
  "version": "v0.9.0",
  "checks": {
    "component_name": {
      "status": "pass|fail|warn",
      "description": "Human-readable description",
      "duration": "1.234567ms",
      "details": {
        "additional": "component-specific data"
      }
    }
  }
}
```

## Health Checks

The comprehensive health check includes the following components:

### Database Check
- **Name**: `database`
- **Purpose**: Verifies database connectivity and basic operations
- **Status**: `pass` if database is accessible, `fail` otherwise
- **Details**: Connection test duration and any error messages

### Docker Daemon Check
- **Name**: `docker`
- **Purpose**: Verifies Docker daemon connectivity
- **Status**: `pass` if Docker daemon is accessible, `fail` otherwise
- **Details**: Docker API ping response time

### Disk Space Check
- **Name**: `disk`
- **Purpose**: Monitors available disk space
- **Status**: `pass` if sufficient space, `warn` if low, `fail` if critically low
- **Details**: Total, used, and available disk space in MB

### Memory Check
- **Name**: `memory`
- **Purpose**: Monitors available memory
- **Status**: `pass` if sufficient memory, `warn` if low, `fail` if critically low
- **Details**: Available, allocated, and system memory in MB

### Process Check
- **Name**: `process` (liveness only)
- **Purpose**: Basic process health verification
- **Status**: `pass` if process is running
- **Details**: Process runtime information

## HTTP Status Codes

Health endpoints return appropriate HTTP status codes based on the overall health status:

- **200 OK**: Service is healthy or degraded
- **503 Service Unavailable**: Service is unhealthy
- **500 Internal Server Error**: Unexpected error during health check

## Kubernetes Integration

### Liveness Probe Configuration

```yaml
livenessProbe:
  httpGet:
    path: /health/live
    port: 3012
  initialDelaySeconds: 30
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3
```

### Readiness Probe Configuration

```yaml
readinessProbe:
  httpGet:
    path: /health/ready
    port: 3012
  initialDelaySeconds: 5
  periodSeconds: 5
  timeoutSeconds: 3
  failureThreshold: 3
```

### Startup Probe Configuration

```yaml
startupProbe:
  httpGet:
    path: /health/live
    port: 3012
  initialDelaySeconds: 10
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 30
```

## Monitoring Integration

### Prometheus Metrics

Health check results are automatically exposed as Prometheus metrics:

- `dockpal_health_check_status`: Health check status (1 for healthy, 0 for unhealthy)
- `dockpal_health_check_duration_seconds`: Health check execution time
- `dockpal_component_check_status`: Individual component check status

Metrics are available at `/metrics` endpoint when Prometheus integration is enabled.

### Alerting Examples

#### Prometheus Alert Rules

```yaml
groups:
- name: dockpal
  rules:
  - alert: DockpalUnhealthy
    expr: dockpal_health_check_status == 0
    for: 2m
    labels:
      severity: critical
    annotations:
      summary: "Dockpal is unhealthy"
      description: "Dockpal health check has been failing for 2 minutes"

  - alert: DockpalDatabaseDown
    expr: dockpal_component_check_status{component="database"} == 0
    for: 1m
    labels:
      severity: warning
    annotations:
      summary: "Dockpal database is down"
      description: "Database connectivity check is failing"
```

## Troubleshooting

### Common Issues

1. **Database Timeout**
   - Check database file permissions
   - Verify disk space availability
   - Check for database locks

2. **Docker Daemon Unreachable**
   - Verify Docker daemon is running
   - Check Docker socket permissions
   - Verify Docker API accessibility

3. **Low Memory/Disk Space**
   - Monitor system resources
   - Clean up unused containers/images
   - Consider increasing resources

### Debugging

Use the comprehensive health endpoint to get detailed diagnostics:

```bash
curl -s http://localhost:3012/health | jq .
```

Check individual components:

```bash
# Database status
curl -s http://localhost:3012/health | jq '.checks.database'

# Docker status
curl -s http://localhost:3012/health | jq '.checks.docker'

# System resources
curl -s http://localhost:3012/health | jq '.checks.memory, .checks.disk'
```

## Configuration

Health check behavior can be configured through environment variables:

- `DOCKPAL_HEALTH_CHECK_TIMEOUT`: Timeout for individual health checks (default: 5s)
- `DOCKPAL_HEALTH_CHECK_INTERVAL`: Interval between automatic health checks (default: 30s)
- `DOCKPAL_MIN_MEMORY_MB`: Minimum memory threshold in MB (default: 100)
- `DOCKPAL_MIN_DISK_MB`: Minimum disk space threshold in MB (default: 1000)

## Security Considerations

- Health endpoints do not require authentication for monitoring purposes
- Sensitive information is not exposed in health check responses
- Rate limiting may be applied to prevent abuse
- Consider restricting access to health endpoints in production environments

## Examples

### Basic Health Check

```bash
# Check overall health
curl http://localhost:3012/health

# Check liveness
curl http://localhost:3012/health/live

# Check readiness
curl http://localhost:3012/health/ready
```

### Monitoring Script

```bash
#!/bin/bash
# Simple health monitoring script

HEALTH_URL="http://localhost:3012/health"
RESPONSE=$(curl -s "$HEALTH_URL")
STATUS=$(echo "$RESPONSE" | jq -r '.status')

if [ "$STATUS" = "healthy" ]; then
    echo "✅ Dockpal is healthy"
    exit 0
elif [ "$STATUS" = "degraded" ]; then
    echo "⚠️  Dockpal is degraded"
    exit 1
else
    echo "❌ Dockpal is unhealthy"
    echo "$RESPONSE" | jq .
    exit 2
fi
```

### Kubernetes Deployment Example

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dockpal
spec:
  replicas: 3
  selector:
    matchLabels:
      app: dockpal
  template:
    metadata:
      labels:
        app: dockpal
    spec:
      containers:
      - name: dockpal
        image: dockpal:latest
        ports:
        - containerPort: 3012
        livenessProbe:
          httpGet:
            path: /health/live
            port: 3012
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health/ready
            port: 3012
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
```