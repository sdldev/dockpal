# Dockpal

Lightweight Docker management platform — single binary, embedded UI, multi-instance support.

Manage containers, deploy stacks, monitor resources, and orchestrate remote Docker hosts from a clean web dashboard.

## Features

- Real-time CPU, memory, and network monitoring (host + per-container)
- One-click deploy from 25+ templates (PostgreSQL, Redis, Grafana, n8n, etc.)
- Compose & Git deploy with streamed progress logs
- Multi-instance management — manage remote Docker hosts via DockPal Agent
- Fleet Dashboard — global view of all instances and bulk deploy
- RBAC — admin, operator, viewer roles
- Private registry support (encrypted PAT tokens)
- Traefik integration & Cloudflare Tunnel support
- Auto-recovery for crashed containers
- Self-update mechanism
- Embedded UI — works offline, no CDN dependencies
- Prometheus metrics export for external monitoring

## Quick Start (Development)

```bash
git clone https://github.com/sdldev/dockpal.git
cd dockpal
make dev
```

This builds the binary and starts the server on port 3012 with local data directory (`.data/`).

Open http://localhost:3012 and log in as `admin`. On first startup, DockPal generates an initial admin password and prints it in the server logs. To choose the bootstrap password yourself, set `DOCKPAL_INITIAL_ADMIN_PASSWORD` before the first startup.

## Development

### Prerequisites

- Go 1.25+
- Docker (running)

### Commands

```bash
make dev              # Build + run server locally
make build            # Build binary
make test             # Run all tests
make lint             # Run go vet
make clean            # Remove build artifacts
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DOCKPAL_DATA_DIR` | `/opt/dockpal/data` | Data directory (DB, logs, secrets) |
| `DOCKPAL_DB_PATH` | `<DATA_DIR>/dockpal.db` | BBolt database path |
| `DOCKPAL_LOG_PATH` | `<DATA_DIR>/dockpal.log` | Log file path |
| `DOCKPAL_SECRET_PATH` | `<DATA_DIR>/.secret` | JWT secret file |
| `DOCKPAL_INITIAL_ADMIN_PASSWORD` | auto-generated | Initial `admin` password used only when the admin user is first created |
| `JWT_SECRET` | auto-generated | Override JWT signing key |
| `DOCKPAL_TLS_DOMAIN` | | ACME Let's Encrypt domain |
| `DOCKPAL_TLS_CERT` | | Custom TLS certificate path |
| `DOCKPAL_TLS_KEY` | | Custom TLS key path |
| `DOCKPAL_BACKUP_INTERVAL` | `24h` | Automatic backup interval (set to `0` to disable) |
| `DOCKPAL_BACKUP_RETENTION` | `168h` | How long to keep automatic backups |

### Configuration Validation

Dockpal performs comprehensive configuration validation at startup to prevent runtime errors. The validation checks:

**Path Validation**
- All directories are created and writable
- Minimum 100MB disk space available (with warnings for low space)
- Absolute path requirements enforced

**System Dependencies**
- Docker daemon connectivity and accessibility
- Port availability (detects conflicts with existing services)
- Database file creation and read/write operations

**Resource Checks**
- Available system memory (warnings for low memory conditions)
- TLS configuration validation (when enabled)

**Configuration Logging**
At startup, Dockpal logs all configuration values **without sensitive data**:
- Passwords and secrets are shown as `[SET]` or `[WILL BE GENERATED]`
- Full configuration summary helps with troubleshooting

**Example Validation Output**
```
Starting configuration validation...
Docker daemon connection: OK
Port 3012 availability: OK
Database connectivity: OK
System resources: 512.0 MB memory available
=== Configuration Summary ===
Data Directory: /opt/dockpal/data
Database Path: /opt/dockpal/data/dockpal.db
Log Path: /opt/dockpal/data/dockpal.log
Port: 3012
TLS Enabled: false
Admin Password: [SET]
JWT Secret: [WILL BE GENERATED]
=== End Configuration Summary ===
Configuration validation completed successfully
```

If validation fails, Dockpal will display specific error messages and exit with a non-zero status code, preventing partial startup states.

### Reset Admin Password

```bash
./dockpal reset-password
```

### Backup & Restore

**CLI Backup** (requires server to be stopped because BoltDB uses file locking):

```bash
# Default backup path: <data_dir>/backups/dockpal-<timestamp>.db
./dockpal backup

# Custom output path
./dockpal backup --output /opt/dockpal/backups/my-backup.db
```

**Hot Backup** (while server is running) via the admin API:

```bash
curl -X POST https://localhost:3012/api/backup \
  -H "Authorization: Bearer <admin-jwt>"
```

**Restore** (requires server to be stopped):

```bash
# Interactive confirmation
./dockpal restore --from /opt/dockpal/backups/dockpal-20260521-120000.db

# Skip confirmation
./dockpal restore --from /path/to/backup.db --force
```

Backups include a sidecar `.sha256` checksum file. The restore command validates
the backup integrity and checksum before replacing the live database.

### Automated Backup Scheduling

The server includes a built-in background scheduler that automatically backs up
the database at regular intervals. It is enabled by default with a `24h` interval.

**Configuration:**

```bash
# Disable automatic backups
DOCKPAL_BACKUP_INTERVAL=0 ./dockpal server

# Backup every 6 hours, keep for 3 days
DOCKPAL_BACKUP_INTERVAL=6h DOCKPAL_BACKUP_RETENTION=72h ./dockpal server
```

The scheduler logs every backup success/failure and automatically removes backups
older than the retention period.

## Prometheus Metrics

DockPal exports Prometheus-compatible metrics at `/api/metrics` for external monitoring and alerting.

### Available Metrics

- **Container metrics**: CPU, memory, network I/O per container
- **Host metrics**: CPU, memory, disk usage per instance  
- **HTTP metrics**: Request count, duration, and error rates
- **Build info**: Version and build information

### Quick Setup

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'dockpal'
    static_configs:
      - targets: ['localhost:3012']
    metrics_path: '/api/metrics'
    scrape_interval: 15s
```

### Example Queries

```promql
# Total running containers
sum(dockpal_containers_total{status="running"})

# Host CPU usage
dockpal_host_cpu_percent

# Container memory usage
topk(10, dockpal_container_memory_bytes)

# HTTP request rate
sum(rate(dockpal_http_requests_total[5m])) by (endpoint)
```

For detailed documentation, see [docs/PROMETHEUS_METRICS.md](docs/PROMETHEUS_METRICS.md).

## Multi-Instance Architecture

DockPal supports managing multiple Docker hosts from a single dashboard.

```
┌─────────────────┐         ┌──────────────────────┐
│  DockPal Server │◄────────│  Browser (UI)        │
│  (port 3012)    │         └──────────────────────┘
└────────┬────────┘
         │
         ├── local Docker (This Server)
         │
         ├── HTTPS ──► Agent @ 192.168.x.x:9273  (direct mode)
         │
         └── WSS ◄── Agent @ remote-host          (edge mode)
```

**Direct mode**: Server connects to agent via HTTPS. Agent must be reachable from server.

**Edge mode**: Agent connects to server via WebSocket. For agents behind NAT/firewall.

### DockPal Agent

Agent source code lives in `dockpal-agent/`. It's a separate Go module with its own repo.

```bash
cd dockpal-agent
CGO_ENABLED=0 go build -o dockpal-agent .
```

Agent runs as a Docker container on remote hosts:

```bash
docker run -d --name dockpal-agent --restart unless-stopped \
  -e DOCKPAL_MODE=direct \
  -e DOCKPAL_TOKEN=<token> \
  -p 9273:9273 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/dockpal-agent:/opt/dockpal-agent \
  ghcr.io/sdldev/dockpal-agent:latest
```

## Project Structure

```
dockpal/
├── main.go                 # Entry point, CLI (server, reset-password)
├── Makefile                # Build, test, dev commands
├── internal/
│   ├── agent/              # Multi-instance client (direct HTTP, edge WebSocket)
│   ├── auth/               # JWT, RBAC, login, secret management
│   ├── db/                 # BBolt persistence (users, services, instances, audit)
│   ├── docker/             # Docker SDK wrapper, compose, health monitor
│   ├── git/                # Git deploy helper
│   ├── installer/          # Remote agent SSH installer
│   ├── logging/            # Log rotation
│   ├── registry/           # Private registry credentials (AES-256-GCM)
│   ├── server/             # HTTP routes, middleware, WebSocket handlers
│   ├── ssh/                # SSH client for remote agent install
│   ├── traefik/            # Reverse proxy config generator
│   ├── tunnel/             # Cloudflare tunnel lifecycle
│   ├── update/             # Self-update service
│   └── validator/          # Input validation
├── web/
│   ├── index.html          # Shell with #include directives
│   ├── pages/              # Page templates (dashboard, containers, instances, etc.)
│   ├── partials/           # Reusable components (sidebar, dialogs)
│   ├── assets/modules/     # Alpine.js feature modules
│   └── embed.go            # go:embed + HTML assembler
├── templates/              # Deploy template JSON files
├── dockpal-agent/          # Agent source (separate Go module)
│   ├── main.go
│   ├── internal/
│   │   ├── server/         # Agent HTTP/WebSocket server
│   │   ├── docker/         # Docker operations (deploy, compose, images)
│   │   ├── auth/           # Token authentication middleware
│   │   ├── config/         # Agent configuration
│   │   ├── host/           # System info & stats
│   │   └── edge/           # Edge mode WebSocket client
│   └── Dockerfile
└── scripts/
    └── pbt-baseline.txt    # Property-based test iteration baselines
```

## Tech Stack

- **Backend**: Go 1.25, Gin, BBolt, Docker SDK
- **Frontend**: Alpine.js, Tailwind CSS, Chart.js (all embedded)
- **Agent**: Go, Chi router, gorilla/websocket
- **Security**: JWT with versioning, bcrypt, AES-256-GCM, rate limiting

## Production Install

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh | sudo bash
```

Installs as systemd service on port 3012. Supports Debian/Ubuntu on linux/amd64.

## Update

Update an existing systemd installation to the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh | sudo bash
```

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh | sudo DOCKPAL_VERSION=v1.1.1 bash
```

The updater downloads and verifies the release binary, backs up the current binary and templates, restarts the service, and rolls back automatically if health checks fail.

Optional variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `DOCKPAL_VERSION` | `latest` | Release tag to install |
| `DOCKPAL_UPDATE_TEMPLATES` | `1` | Refresh templates from the release archive |
| `DOCKPAL_FORCE` | `0` | Reinstall even when already on the target version |
| `DOCKPAL_BACKUP_DIR` | `/opt/dockpal/backups` | Backup directory for binary/templates |

## License

MIT
