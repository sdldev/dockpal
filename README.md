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

### Reset Admin Password

```bash
./dockpal reset-password
```

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
