# Dockpal

Self-hosted Docker management panel — single binary, embedded UI, no dependencies.

Manage containers, deploy compose stacks, monitor resources, and control multiple remote Docker hosts from one dashboard.

---

## Table of Contents

- [Features](#features)
- [Quick Start](#quick-start)
- [Development](#development)
- [Configuration](#configuration)
- [CLI Reference](#cli-reference)
- [API Reference](#api-reference)
- [Health Checks](#health-checks)
- [Metrics](#metrics)
- [Backup & Restore](#backup--restore)
- [TLS](#tls)
- [Multi-Instance](#multi-instance)
- [RBAC](#rbac)
- [Production Install](#production-install)
- [Project Structure](#project-structure)

---

## Features

| Category | Details |
|---|---|
| **Containers** | List, start, stop, restart, delete, inspect, tail logs, real-time stats |
| **Deploy** | Compose YAML, Git repo deploy, 25+ built-in templates (PostgreSQL, Redis, Grafana, n8n, …) |
| **Images** | Pull, check updates, prune, registry auth |
| **Files** | Browse, read, write, upload, download files inside containers |
| **Domains** | Traefik integration, custom domain routing |
| **Tunnel** | Cloudflare Tunnel setup & teardown |
| **Multi-instance** | Manage remote Docker hosts via DockPal Agent (direct or WebSocket edge) |
| **Fleet** | Global dashboard across all instances, bulk deploy |
| **Security** | RBAC (admin / operator / viewer), JWT auth, rate limiting, audit log |
| **Registry** | Private registry credentials, encrypted at rest |
| **Webhooks** | Trigger deploys from CI/CD pipelines |
| **API Keys** | Programmatic access without username/password |
| **Monitoring** | Prometheus metrics, health endpoints, auto-recovery for crashed containers |
| **Backup** | Scheduled + on-demand database backup, restore, retention policy |

---

## Quick Start

### Prerequisites

- Go 1.25+
- Docker daemon running
- `git`

### Run locally

```bash
git clone https://github.com/sdldev/dockpal
cd dockpal
make dev
```

Server starts at **http://localhost:3012**.

On first startup, admin credentials are printed to the log:

```
Generated initial admin password for username admin: <password>
```

Set `DOCKPAL_INITIAL_ADMIN_PASSWORD` before first startup to choose your own password instead.

---

## Development

### Hot reload (recommended)

Requires [`reflex`](https://github.com/cespare/reflex):

```bash
# install reflex once
go install github.com/cespare/reflex@latest
# or on Debian/Ubuntu
sudo apt install reflex

# start dev server with auto-rebuild on .go changes
make dev-watch
```

Every time you save a `.go` file, reflex kills the old process, rebuilds, and restarts the server automatically.

**Frontend changes** (HTML/JS in `web/`) take effect on browser refresh — no restart needed.

### Common commands

```bash
make build              # build ./dockpal binary
make dev                # build + run once (no watch)
make dev-watch          # build + run with hot reload
make test               # go test -v ./...
make lint               # go vet ./...
make install-hooks      # install pre-commit hook (runs vet + test)
make build-linux-amd64  # cross-compile for linux/amd64
make clean              # remove build artifacts
```

### Run a specific test

```bash
# single test in a package
go test ./internal/server -run TestName -v

# rerun without cache
go test ./internal/docker -run TestName -count=1

# multiple tests across packages
go test ./... -run 'TestA|TestB'
```

### Data directory

Dev data lives in `.data/` (created automatically):

```
.data/
├── dockpal.db        # BBolt database
├── dockpal.log       # rotating log
├── .secret           # JWT signing key
└── backups/          # scheduled backups
```

---

## Configuration

All configuration is via environment variables. No config file is required.

### Core

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_DATA_DIR` | `/opt/dockpal/data` | Root directory for db, log, secret, backups |
| `DOCKPAL_DB_PATH` | `<data_dir>/dockpal.db` | BBolt database path |
| `DOCKPAL_LOG_PATH` | `<data_dir>/dockpal.log` | Rotating log file path |
| `DOCKPAL_SECRET_PATH` | `<data_dir>/.secret` | JWT signing key file |
| `JWT_SECRET` | — | Override JWT signing key directly (skips file) |
| `PORT` | `3012` (HTTP) / `3443` (TLS) | Server listen port |
| `DOCKPAL_INITIAL_ADMIN_PASSWORD` | random (printed to log) | Admin password on first startup |

### TLS

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_TLS` | `false` | Enable TLS |
| `DOCKPAL_TLS_CERT` | — | Path to TLS certificate file |
| `DOCKPAL_TLS_KEY` | — | Path to TLS key file |
| `DOCKPAL_TLS_DOMAIN` | — | Domain for ACME/Let's Encrypt auto-cert |

TLS modes (pick one):
- **Custom cert**: set `DOCKPAL_TLS=true` + `DOCKPAL_TLS_CERT` + `DOCKPAL_TLS_KEY`
- **ACME/Let's Encrypt**: set `DOCKPAL_TLS_DOMAIN` (cert auto-generated)
- **Self-signed**: set `DOCKPAL_TLS=true` without cert/key (generates on startup)

### Backup

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_BACKUP_INTERVAL` | `24h` | Scheduled backup interval (`0` = disable) |
| `DOCKPAL_BACKUP_RETENTION` | `168h` (7 days) | How long to keep automatic backups |

### Agent

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_AGENT_IMAGE` | — | Docker image used for remote agent install commands |

### Audit log

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_AUDIT_LOG_RETENTION` | `2160h` (90 days) | How long to retain audit log entries |

### Example: minimal dev env

```bash
DOCKPAL_DATA_DIR=$(pwd)/.data \
DOCKPAL_INITIAL_ADMIN_PASSWORD=mypassword \
./dockpal server
```

### Example: production with TLS

```bash
DOCKPAL_DATA_DIR=/opt/dockpal/data \
DOCKPAL_TLS_DOMAIN=panel.example.com \
PORT=443 \
./dockpal server
```

---

## CLI Reference

```
dockpal <subcommand> [flags]

Subcommands:
  server            Start the HTTP server
  backup            Create a database backup
  restore           Restore a database from backup
  reset-password    Reset a user's password
  version           Print version
  help              Show this help
```

### `dockpal server`

Starts the web server. Reads all config from environment variables.

```bash
DOCKPAL_DATA_DIR=/opt/dockpal/data ./dockpal server
```

### `dockpal backup`

On-demand backup of the database.

```bash
./dockpal backup --output /tmp/dockpal-backup.db
```

### `dockpal restore`

Restore database from a backup file.

```bash
# Stop the server first, then:
./dockpal restore --input /tmp/dockpal-backup.db
```

### `dockpal reset-password`

Reset a user's password without logging in. Server must be stopped first.

```bash
# Set a specific password
./dockpal reset-password --username admin --password MyNewPassword123

# Generate a random password (printed to stdout)
./dockpal reset-password --username admin

# Defaults to username=admin if --username is omitted
./dockpal reset-password --password MyNewPassword123
```

Flags:
- `--username` — user to reset (default: `admin`)
- `--password` — new password, min 8 chars; omit to auto-generate

> Passwords set by users in the UI are **never touched** by `update.sh` or server restarts.

---

## API Reference

Base URL: `http://localhost:3012/api`

All protected routes require: `Authorization: Bearer <jwt_token>`

### Authentication

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/api/login` | — | Login, returns JWT |
| `POST` | `/api/logout` | ✓ | Invalidate token |
| `POST` | `/api/auth/reset-password` | ✓ | Change own password |
| `GET` | `/api/profile` | ✓ | Get current user profile |
| `PUT` | `/api/profile/password` | ✓ | Update own password |

### Users & API Keys (admin only)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/users` | List all users |
| `PUT` | `/api/users/:username/role` | Change user role |
| `GET` | `/api/api-keys` | List API keys |
| `POST` | `/api/api-keys` | Create API key |
| `DELETE` | `/api/api-keys/:id` | Delete API key |

### Containers

All routes below are available both as legacy (`/api/...`) and instance-scoped (`/api/instances/:instance_id/...`).

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/containers` | ✓ | List containers |
| `GET` | `/api/containers/:id` | ✓ | Inspect container |
| `PUT` | `/api/containers/:id` | operator | Update container |
| `DELETE` | `/api/containers/:id` | operator | Remove container |
| `POST` | `/api/containers/:id/start` | operator | Start container |
| `POST` | `/api/containers/:id/stop` | operator | Stop container |
| `POST` | `/api/containers/:id/restart` | operator | Restart container |
| `GET` | `/api/containers/:id/logs` | ✓ | Tail container logs |
| `GET` | `/api/containers/:id/stats` | ✓ | Resource stats (polling) |
| `GET` | `/api/containers/:id/stats/ws` | ✓ | Resource stats (WebSocket) |
| `GET` | `/api/containers/:id/files` | ✓ | — (see Files) |
| `POST` | `/api/containers/:id/files/write` | operator | Write file into container |

### Deploy

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/api/deploy/compose` | operator | Deploy Compose YAML |
| `POST` | `/api/deploy/stream` | operator | Deploy with SSE progress stream |
| `POST` | `/api/deploy/git` | operator | Deploy from Git repo |
| `GET` | `/api/deploy/stream/:id` | ✓ | WebSocket attach to deploy stream |

### Templates

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/templates` | ✓ | List available templates |
| `GET` | `/api/templates/:id` | ✓ | Get template detail |
| `POST` | `/api/templates/:id/deploy` | operator | Deploy a template |
| `POST` | `/api/templates/:id/deploy/stream` | operator | Deploy a template (streamed) |

### Images

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/images` | ✓ | List images |
| `GET` | `/api/images/updates` | ✓ | Check for image updates |
| `POST` | `/api/images/pull` | operator | Pull an image |
| `POST` | `/api/images/pull-force` | operator | Force re-pull image |
| `POST` | `/api/images/check` | operator | Check image exists |
| `POST` | `/api/images/prune` | operator | Remove unused images |
| `DELETE` | `/api/images/:id` | operator | Remove an image |

### Apps (running compose stacks)

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/apps` | ✓ | List deployed apps |
| `GET` | `/api/apps/:name/updates` | ✓ | List update attempts |
| `GET` | `/api/apps/:name/updates/:attemptID` | ✓ | Get update attempt detail |
| `GET` | `/api/apps/updates/stream` | ✓ | Stream update events (WebSocket) |
| `POST` | `/api/apps/:name/update` | operator | Trigger app update |
| `PATCH` | `/api/apps/:name/auto-update` | operator | Toggle auto-update |

### Files

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/files` | ✓ | Browse filesystem |
| `GET` | `/api/files/read` | ✓ | Read a file |
| `GET` | `/api/files/download` | ✓ | Download a file |
| `POST` | `/api/files/upload` | operator | Upload a file |
| `POST` | `/api/files/write` | operator | Write file content |
| `DELETE` | `/api/files` | operator | Delete a file |

### Domains

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/domains` | ✓ | List domains |
| `POST` | `/api/domains` | operator | Create domain |
| `DELETE` | `/api/domains/:id` | operator | Remove domain |

### Registry Credentials

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/registries` | ✓ | List registries |
| `GET` | `/api/registries/:id` | ✓ | Get registry |
| `POST` | `/api/registries` | operator | Add registry |
| `PUT` | `/api/registries/:id` | operator | Update registry |
| `POST` | `/api/registries/:id/test` | operator | Test registry credentials |
| `DELETE` | `/api/registries/:id` | operator | Remove registry |

### Webhooks

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/webhooks` | ✓ | List webhooks |
| `POST` | `/api/webhooks` | operator | Create webhook |
| `DELETE` | `/api/webhooks/:webhook_id` | operator | Delete webhook |
| `POST` | `/api/webhooks/deploy/:webhook_id` | — | Trigger deploy (no auth, secret in URL) |

### Cloudflare Tunnel

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/api/tunnel` | admin | Create tunnel |
| `DELETE` | `/api/tunnel` | admin | Remove tunnel |

### System

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/system/info` | ✓ | Host info (CPU, disk, Docker version) |
| `POST` | `/api/backup` | admin | Trigger manual backup |
| `GET` | `/api/audit-logs` | admin | List audit log entries |
| `GET` | `/api/config` | — | Server public config |
| `GET` | `/api/docs` | — | API docs (Redoc UI) |
| `GET` | `/api/docs/swagger.json` | — | OpenAPI spec |

### GitHub

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/github/repos` | ✓ | List accessible repos (via PAT) |

### Multi-Instance (Agent)

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/agent/connect` | ✓ | WebSocket — edge agent registration |
| `GET` | `/api/instances/:instance_id/...` | ✓ | All above routes, scoped to an instance |

---

## Health Checks

| Endpoint | Description |
|---|---|
| `GET /health` | Full health report (database, docker, disk, memory) |
| `GET /healthz` | Alias for `/health` (Kubernetes style) |
| `GET /health/live` | Liveness — is the process alive? |
| `GET /livez` | Alias for `/health/live` |
| `GET /health/ready` | Readiness — can the server accept traffic? (checks db + docker) |
| `GET /readyz` | Alias for `/health/ready` |

### Example response

```json
{
  "status": "healthy",
  "timestamp": "2026-06-07T10:00:00Z",
  "uptime": "2h30m",
  "version": "v0.9.0",
  "checks": {
    "database": { "status": "pass", "description": "Database connectivity and operations OK" },
    "docker":   { "status": "pass", "description": "Docker daemon connectivity OK" },
    "disk_data":{ "status": "pass", "details": { "available_mb": 50000 } },
    "disk_root":{ "status": "pass" },
    "memory":   { "status": "pass", "details": { "available_mb": 512 } }
  }
}
```

Status values: `healthy` (HTTP 200) · `degraded` (HTTP 200) · `unhealthy` (HTTP 503)

---

## Metrics

Prometheus-compatible metrics at `GET /api/metrics` (no auth required).

### Available metrics

| Metric | Type | Description |
|---|---|---|
| `dockpal_containers_total` | Gauge | Total containers per instance |
| `dockpal_containers_running` | Gauge | Running containers per instance |
| `dockpal_http_requests_total` | Counter | HTTP requests by method/path/status |
| `dockpal_http_duration_seconds` | Histogram | HTTP request latency |
| `dockpal_instances_total` | Gauge | Total registered instances |

### Prometheus scrape config

```yaml
scrape_configs:
  - job_name: dockpal
    static_configs:
      - targets: ['localhost:3012']
    metrics_path: /api/metrics
```

---

## Backup & Restore

### Automatic backups

Configured via environment variables:

```bash
DOCKPAL_BACKUP_INTERVAL=24h     # how often to backup (0 = disabled)
DOCKPAL_BACKUP_RETENTION=168h   # how long to keep backups (7 days)
```

Backups are written to `<data_dir>/backups/` as `.db` files with SHA-256 checksums.

### Manual backup via API

```bash
curl -X POST http://localhost:3012/api/backup \
  -H "Authorization: Bearer $TOKEN"
```

### Manual backup via CLI

```bash
./dockpal backup --output /tmp/dockpal-$(date +%Y%m%d).db
```

### Restore

```bash
# 1. Stop the server
systemctl stop dockpal   # production
# or kill the dev process

# 2. Restore
./dockpal restore --input /tmp/dockpal-20260607.db

# 3. Restart
systemctl start dockpal
```

---

## TLS

### Let's Encrypt (recommended for production)

```bash
DOCKPAL_TLS_DOMAIN=panel.example.com ./dockpal server
```

- Port defaults to 3443
- Cert stored in `<data_dir>/certs/`
- Auto-renews

### Custom certificate

```bash
DOCKPAL_TLS=true \
DOCKPAL_TLS_CERT=/etc/ssl/certs/panel.crt \
DOCKPAL_TLS_KEY=/etc/ssl/private/panel.key \
./dockpal server
```

### Self-signed (testing only)

```bash
DOCKPAL_TLS=true ./dockpal server
```

---

## Multi-Instance

Dockpal can manage multiple remote Docker hosts using DockPal Agent.

### Agent types

| Type | Description |
|---|---|
| `local` | The Docker daemon on the Dockpal server itself |
| `direct` | Remote host reachable by HTTP (same network) |
| `edge` | Remote host behind NAT — connects out via WebSocket |

### Add an instance

1. Open **Instances** in the UI  
2. Click **Add Instance**  
3. For edge agents: copy the install command, run it on the remote host  
4. The agent connects back to Dockpal over WebSocket

### API access

All container/deploy/image routes work per-instance:

```
GET /api/instances/{instance_id}/containers
POST /api/instances/{instance_id}/deploy/compose
```

`instance_id = "local"` always refers to the local host.

---

## RBAC

Three roles, assigned per user:

| Role | Can view | Can operate | Can administrate |
|---|---|---|---|
| `viewer` | ✓ | — | — |
| `operator` | ✓ | ✓ | — |
| `admin` | ✓ | ✓ | ✓ |

Admin-only operations: user management, API key management, audit logs, backup trigger, role assignment, tunnel management.

### Change a user's role (API)

```bash
curl -X PUT http://localhost:3012/api/users/alice/role \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"role": "operator"}'
```

---

## Production Install

Install as a systemd service on Debian/Ubuntu (linux/amd64):

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh | sudo bash
```

- Installs binary to `/opt/dockpal/dockpal`  
- Creates systemd unit `dockpal.service`  
- Data directory: `/opt/dockpal/data`
- Starts on port 3012

### Post-install

```bash
# Check status
systemctl status dockpal

# Read logs
journalctl -u dockpal -f

# Get the generated admin password (first run only)
journalctl -u dockpal | grep "admin password"

# Set a custom admin password for next startup
DOCKPAL_INITIAL_ADMIN_PASSWORD=mypassword systemctl restart dockpal
```

---

## Project Structure

```
dockpal/
├── main.go                    # Entry point — wires all services
├── Makefile                   # Build, dev, test targets
├── go.mod / go.sum
│
├── internal/
│   ├── agent/                 # AgentClient abstraction (local/direct/edge/WebSocket)
│   ├── auth/                  # JWT, login, roles, password reset
│   ├── backup/                # Backup scheduler and restore logic
│   ├── config/                # Config helpers
│   ├── db/                    # BBolt persistence (users, containers, audit, webhooks, …)
│   ├── docker/                # Moby client wrapper (containers, images, compose, stats)
│   ├── git/                   # Git deploy support
│   ├── health/                # Health check endpoints (db ping via interface, no double-open)
│   ├── installer/             # Remote agent install command generation
│   ├── logging/               # Rotating log setup
│   ├── metrics/               # Prometheus metrics collector
│   ├── registry/              # Private registry credentials (encrypted)
│   ├── server/                # Gin setup, middleware, route registration, RBAC, audit
│   ├── ssh/                   # SSH helpers for remote agent install
│   ├── traefik/               # Traefik domain/proxy integration
│   ├── tunnel/                # Cloudflare Tunnel management
│   └── validator/             # Input validation helpers
│
├── web/                       # Embedded frontend
│   ├── embed.go               # go:embed directive
│   ├── index.html             # Shell with <!--#include--> directives
│   ├── assets/
│   │   ├── app.js             # Alpine.js root — merges all modules
│   │   ├── modules/           # One JS module per feature domain
│   │   │   ├── auth.js        # Login / logout / token
│   │   │   ├── containers.js  # Container list + actions
│   │   │   ├── dashboard.js   # Real-time charts
│   │   │   ├── deploy.js      # (inline in routes)
│   │   │   ├── images.js      # Image management
│   │   │   ├── imageUpdates.js# Pull-on-update logic
│   │   │   ├── apps.js        # Running compose apps
│   │   │   ├── domains.js     # Traefik domains
│   │   │   ├── files.js       # File browser
│   │   │   ├── fleet.js       # Multi-instance fleet view
│   │   │   ├── instances.js   # Instance management
│   │   │   ├── registry.js    # Private registry
│   │   │   ├── profile.js     # User profile
│   │   │   ├── router.js      # Client-side routing
│   │   │   ├── state.js       # Shared Alpine state
│   │   │   ├── ui.js          # Toast, modal helpers
│   │   │   ├── lifecycle.js   # Init / cleanup hooks
│   │   │   └── …
│   │   └── vendor/            # Alpine.js, Tailwind, Chart.js (offline-safe)
│   └── pages/ partials/       # HTML fragments (included at startup)
│
├── templates/                 # JSON deploy templates (PostgreSQL, Redis, …)
└── scripts/                   # PBT baseline, tooling scripts
```

### Adding a frontend module

1. Create `web/assets/modules/myfeature.js` — attach to `window.Dockpal.myfeature = { … }`
2. Add `<script src="/assets/modules/myfeature.js"></script>` in `web/index.html` before `app.js`
3. Add `D.myfeature` to the merge array in `web/assets/app.js`

---

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.25, Gin, BBolt, Moby (Docker SDK) |
| Frontend | Alpine.js, Tailwind CSS, Chart.js (all embedded, no CDN) |
| Auth | JWT (HS256), bcrypt passwords |
| Storage | BBolt (single-file embedded KV) |
| Metrics | Prometheus-compatible |
| Live data | WebSocket (gorilla/websocket), SSE |

---

## License

MIT
