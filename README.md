# Dockpal

**Self-hosted Docker management panel** — a single Go binary that embeds a
Svelte 5 SPA and a BBolt database. Manage containers, images, compose stacks,
domains, registries, and remote Docker hosts from one web UI.

- Single static binary (Gin HTTP server + embedded SPA + BBolt) — no external DB
- RBAC (admin / operator / viewer), JWT auth, audit log
- Multi-host: manage remote Docker daemons (direct HTTP or edge WebSocket agent)
- Per-server control panel: health, 30-day metrics history, security activity
- SSH hardening: toggle password auth / root login / fail2ban; live fail2ban +
  firewall monitoring with one-click unban
- UI-driven system update with verified swap, automatic backup, and rollback

---

## Quick Start

### Production Install (Debian/Ubuntu, linux/amd64)

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh | sudo bash
```

The installer:

- Installs dependencies (`curl`, `lsof`, `jq`, `tar`) and Docker Engine if missing
- Downloads the latest `dockpal-linux-amd64` binary to `/usr/local/bin/dockpal`
- Provisions the `/opt/dockpal` data layout and deploy templates
- Installs and starts `dockpal.service` (systemd, hardened) plus the
  `dockpal-updater.path` unit used for in-UI updates
- Prints the generated admin password on first run

Pin a specific release with `DOCKPAL_VERSION=v2.1.0`.

#### Post-install

```bash
# Check status
systemctl status dockpal

# Read logs
journalctl -u dockpal -n 100 --no-pager

# Get admin password (first run only)
journalctl -u dockpal --no-pager | grep "generated password"

# Set a custom admin password (only before the first start)
sudo systemctl set-environment DOCKPAL_INITIAL_ADMIN_PASSWORD=your-secure-password
sudo systemctl restart dockpal
```

Access the panel at `http://<server-ip>:3012`.

#### Reset the admin password

If the first-run password was not captured (or you simply want a new one), reset
it from the host. **The server must be stopped first** — a running server holds
the BBolt database lock and the command will time out:

```bash
sudo systemctl stop dockpal
sudo /usr/local/bin/dockpal reset-password --username admin --password '<new-password>'
sudo systemctl start dockpal
```

This is the only way to change a password outside the UI; passwords set via the
UI are preserved across updates.

> ⚠️ **Remote servers**: when the host has a public (non-RFC-1918) IP address,
> the installer and `dockpal install` **require** `DOCKPAL_INITIAL_ADMIN_PASSWORD`
> to be set — an auto-generated password would otherwise leak to `journalctl` on
> a publicly reachable box.

---

## Update

Dockpal can be updated two ways: from the web UI (recommended) or from the host
with `update.sh`. Both resolve the release, verify the download (SHA-256 + ELF
smoke check), back up the current binary and templates, and roll back
automatically if the updated panel fails its health check.

### Update from the web UI

When a newer release is available, an **update badge** appears in the top bar
(for admins) and the details land in **Settings → Administration → Update**.
From there an admin can review the changelog and click **Update** — no SSH
required.

Because the panel runs as the locked-down `dockpal` user (it cannot replace its
own binary), a UI update works by writing a small trigger file that a root-owned
`systemd` path unit consumes; that unit runs `update.sh` as root and restarts
the panel. The installer sets this up automatically. If the updater units are
missing, the UI button is inert — update from the host instead.

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_UPDATE_ENABLED` | `true` | Master switch for the in-UI update feature |
| `DOCKPAL_UPDATE_CHECK_INTERVAL` | `6h` | How often the panel checks for a new release (`0` = background check off) |
| `DOCKPAL_REPO` | `sdldev/dockpal` | GitHub repository polled for releases |

### Update from the host

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh | sudo bash
```

**Daily auto-update (cron):** save the script once, then schedule it.

```bash
# Download the updater (one-time)
sudo curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh \
  -o /opt/dockpal/update.sh && sudo chmod +x /opt/dockpal/update.sh

# Add to root's crontab (daily at 2 AM)
0 2 * * * /opt/dockpal/update.sh >> /var/log/dockpal-update.log 2>&1
```

Optional environment variables for `update.sh`:

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_VERSION` | `latest` | Release tag to install |
| `DOCKPAL_REPO` | `sdldev/dockpal` | GitHub repository |
| `DOCKPAL_FORCE` | `0` | Force reinstall even if already up-to-date |
| `DOCKPAL_UPDATE_TEMPLATES` | `1` | Refresh templates from release |

A backup is taken on every update; rollback happens automatically if the health
check fails.

---

## Configuration

All via environment variables. No config file needed.

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_DATA_DIR` | `/opt/dockpal/data` | Root directory for db, log, secret, backups |
| `DOCKPAL_DB_PATH` | `<data>/dockpal.db` | BBolt database path |
| `DOCKPAL_LOG_PATH` | `<data>/dockpal.log` | Rotating log path |
| `PORT` | `3012` | Server listen port |
| `JWT_SECRET` | — | Override the JWT signing key directly |
| `DOCKPAL_TLS` | `false` | Enable TLS |
| `DOCKPAL_TLS_CERT` / `DOCKPAL_TLS_KEY` | — | TLS cert/key paths |
| `DOCKPAL_TLS_DOMAIN` | — | Domain for ACME/Let's Encrypt auto-cert |
| `DOCKPAL_BACKUP_INTERVAL` | `24h` | Scheduled backup interval (`0` = disabled) |
| `DOCKPAL_BACKUP_RETENTION` | `168h` | Backup retention window (7 days) |
| `DOCKPAL_AUDIT_LOG_RETENTION` | `2160h` | Audit log retention (90 days) |
| `DOCKPAL_INITIAL_ADMIN_PASSWORD` | random | Admin password (only on first startup; **required on remote hosts**) |
| `DOCKPAL_UPDATE_ENABLED` | `true` | Enable the in-UI system update feature |
| `DOCKPAL_UPDATE_CHECK_INTERVAL` | `6h` | Release-check interval (`0` = background check off) |
| `DOCKPAL_REPO` | `sdldev/dockpal` | GitHub repo polled for releases |
| `DOCKPAL_AGENT_IMAGE` | — | Image for remote agent install commands |

### TLS modes

- **Let's Encrypt**: `DOCKPAL_TLS_DOMAIN=panel.example.com DOCKPAL_TLS=true ./dockpal server`
- **Custom cert**: `DOCKPAL_TLS=true DOCKPAL_TLS_CERT=/path/cert.crt DOCKPAL_TLS_KEY=/path/key.crt ./dockpal server`
- **Self-signed**: `DOCKPAL_TLS=true ./dockpal server` (testing only)

---

## Development Mode

Dockpal is a single Go binary with an embedded Svelte 5 SPA served at `/`. The
Go backend and the SPA communicate over the `/api` API.

### Prerequisites

- Go 1.26+
- Node.js 24 LTS (engines allow `^22 || >=24`) & npm
- A running Docker daemon (some tests and the compose stacks feature)
- **Docker Compose CLI plugin** (`docker compose version`) — required for the
  Stacks feature. Without it, `/api/stacks*` endpoints return
  `501 Not Implemented`. Stacks are stored as
  `<DOCKPAL_DATA_DIR>/../compose/<stack>/compose.yaml` (+ per-stack `.env` and a
  shared `global.env`).

### 1. Full-stack mode (single binary) — for Go backend work

`.go` changes need a rebuild.

```bash
make dev          # build + run the server on :3012, data in .data/
make dev-watch    # hot-reload the backend via reflex (watch *.go, rebuild)
```

- Server at `http://localhost:3012`; the SPA at `http://localhost:3012/`
- An admin user is created on first run; the password is printed to the log, or
  set it first: `DOCKPAL_INITIAL_ADMIN_PASSWORD=dev123 make dev`

> Dev data lives in `./.data/` (bbolt, log, backups) — delete it for a full
> reset. `/opt/dockpal` is not used in dev mode.

### 2. Svelte dev mode (HMR) — for SPA work

```bash
# Terminal 1 — backend on :3012
make dev

# Terminal 2 — Vite dev server on :5173 (proxies /api and /ws to :3012)
make svelte-dev
```

Open `http://localhost:5173/`. API calls are proxied to the backend (see `proxy`
in `svelte/vite.config.ts`), so Svelte components hot-reload without a rebuild.

### 3. After SPA changes: embed + production build

```bash
make prod-build   # svelte-embed (vite build → copy to web/svelteDist) + go build
```

Or step by step:

```bash
make svelte-build   # vite build to svelte/dist
make svelte-embed   # copy svelte/dist → web/svelteDist (embedded via go:embed)
make build          # compile the ./dockpal binary
```

> Cross-compile: `make cross` produces `dockpal-linux-amd64` +
> `dockpal-linux-arm64` + `SHA256SUMS.txt`. macOS is intentionally unsupported
> (`internal/agent` uses Linux-only syscalls).

### Code quality

```bash
make test              # go test ./...
make test-integration  # integration tests (build tag: integration)
make lint              # go vet ./...
make svelte-check      # SPA type check
make svelte-test       # vitest unit tests
```

A pre-commit hook runs `go vet` + build + `go test` (plus `svelte-check` for SPA
changes) before each commit:

```bash
make install-hooks
```

### Debugging tips

- **"Invalid credentials" even though the log printed an admin password** — the
  generated password only applies when the admin user is **first created**.
  Existing users are never changed by a restart; use `./dockpal reset-password`.
- **`dockpal install` fails "remote installation requires DOCKPAL_INITIAL_ADMIN_PASSWORD"** —
  the host has a public IP, so a random password is refused. Set the env var, or
  pass `--password` explicitly. Behind NAT (private IP) this does not apply.
- **SPA changes don't appear at `/`** — you forgot `make svelte-embed` + rebuild;
  the binary only serves the embedded `web/svelteDist`.
- **`reset-password` fails with "timeout"** — the server is still running and
  holds the bbolt lock; stop it first (`systemctl stop dockpal`).

---

## CLI Reference

```bash
dockpal <subcommand> [flags]

Subcommands:
  server            Start the HTTP server
  backup            Create a database backup
  restore           Restore database from backup
  install           First-time setup: create admin user (--username, --password)
  reset-password    Reset user password
  rotate-secrets    Rotate the JWT secret and derived keys
  version           Print version
  help              Show help
```

Examples:

```bash
# Start server
./dockpal server

# On-demand backup
./dockpal backup --output /tmp/backup.db

# First-time setup on a fresh data dir
DOCKPAL_DB_PATH=/opt/dockpal/data/dockpal.db ./dockpal install --username admin --password NewPass123

# Reset admin password (stop the server first)
./dockpal reset-password --username admin --password NewPass123

# View version
./dockpal version
```

> Passwords set via the UI are preserved across updates; only the
> `reset-password` CLI command changes them.

---

## Features

| Category | Details |
|---|---|
| **Servers** | Fleet overview + per-server control panel (`/servers/:id`): health, 30-day metrics history, containers, security activity |
| **Containers** | List, start, stop, restart, delete, inspect, logs, stats, terminal, file manager |
| **Deploy** | Compose YAML, Git repo, built-in templates (PostgreSQL, MariaDB, Redis, Grafana, Adminer, …) |
| **Stacks** | Dockge-style compose stacks: create, edit, deploy, update, env files |
| **Images** | Pull, update checks, registry auth, prune |
| **Domains** | Traefik integration, custom routing, SSL |
| **Monitoring** | Prometheus metrics, real-time charts, health checks, 30-day history |
| **Multi-host** | Manage remote Docker hosts (direct HTTP or edge WebSocket agent); SSH-based agent install + saved SSH keys |
| **Security** | RBAC (admin/operator/viewer), JWT auth, audit log, remote-deploy password enforcement |
| **SSH hardening** | One Apply flow: disable password auth / root login, install + enable fail2ban (sshd jail), verified against lockout |
| **Security monitoring** | Per-server fail2ban (banned IPs, events) + firewall (ufw/firewalld) status and 24h block log, TTL-cached over SSH; unban from the UI (audited) |
| **System update** | In-UI version check + one-click update with verified swap, backup, and auto-rollback |
| **Backup** | Scheduled + manual, SHA-256 checksum, retention policy |

---

## Health & Metrics

| Path | Description |
|---|---|
| `/health` | Full health report (HTTP 200/503) |
| `/api/metrics` | Prometheus metrics (requires auth — JWT bearer or `X-API-Key`, viewer role or higher) |
| `/api/docs` | API documentation UI (Redoc + OpenAPI) |

Example Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: dockpal
    static_configs:
      - targets: ['localhost:3012']
    metrics_path: /api/metrics
```

---

## Project Structure

```
dockpal/
├── main.go                    # Entry point + CLI subcommands
├── Makefile                   # Build/test/dev targets (see: make help)
├── installer.sh               # Production installer (Debian/Ubuntu)
├── update.sh                  # Host self-updater (verify + backup + rollback)
├── internal/                  # Go packages
│   ├── server/                # Gin routes, middleware, RBAC, stacks routes
│   ├── agent/                 # AgentClient (local/direct/edge) + WS client helpers
│   ├── update/                # System self-update: release checker + request manager
│   ├── composecli/            # docker compose CLI runner (stacks engine)
│   ├── docker/                # Moby client wrapper + stack store
│   ├── auth/                  # JWT, login, passwords
│   ├── security/              # Remote-host detection, deploy password enforcement
│   ├── config/                # Env config loading + validation
│   ├── db/                    # BBolt persistence
│   └── ...                    # health, backup, metrics, git, ssh, traefik, tunnel, …
├── packaging/                 # systemd units (dockpal-updater.path/.service)
├── scripts/                   # dockpal-update-helper.sh (root update executor)
├── svelte/                    # Svelte 5 SPA source (served at /)
│   ├── src/components/        # Pages + UI components
│   ├── src/lib/               # API clients, stores, router
│   └── vite.config.ts         # Dev proxy /api → :3012
├── web/                       # Embedded SPA (web/svelteDist = vite build output)
└── templates/                 # JSON deploy templates
```

> Contributor guide for AI/editors (commands, architecture, testing notes) lives
> in [AGENTS.md](AGENTS.md).

---

## License

MIT
