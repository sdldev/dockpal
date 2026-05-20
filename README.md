# 🐳 Dockpal

A simple, lightweight Docker management platform — single binary, embedded UI, no external dependencies.

Manage containers, deploy stacks from compose or templates, monitor resources, and configure routing with Traefik or Cloudflare Tunnel — all from a clean web interface.

---

## ✨ Features

- **Real-time monitoring** — Live CPU, memory, and network charts for host and per-container
- **One-click deploy** — 25+ pre-configured templates (PostgreSQL, Redis, Grafana, n8n, Nextcloud, etc.)
- **Compose & Git deploy** — Deploy from raw YAML or Git repository with auto-pull
- **Streamed deployment logs** — WebSocket-based live progress with smart error diagnostics
- **Container management** — Start, stop, restart, remove, and in-place edit (memory/CPU limits, restart policy, ports, volumes)
- **Fleet Dashboard & Bulk Deploy** — Monitor remote agent resource metrics globally and orchestrate multi-agent compose stack deployments in parallel
- **Webhook Deploy Trigger** — Automate deployment updates via HMAC-SHA256 authenticated webhook endpoints
- **Role-Based Access Control (RBAC)** — Secure access with roles (`admin`, `operator`, `viewer`) mapped to endpoints and WebSocket logs
- **System Audit Log** — Store and display structured database action logs for administrator inspection
- **Built-in HTTPS/TLS** — Zero-config self-signed certificates fallback, manual custom certificate paths, or automated ACME Let's Encrypt certificates
- **Interactive OpenAPI Documentation** — Fully integrated Redoc API UI interactive documentation at `/api/docs`
- **Private registry support** — Store encrypted GitHub PAT tokens to pull from private ghcr.io registries
- **Live log viewer** — Tail container logs over WebSocket
- **Traefik integration** — Auto-generate reverse proxy config with Let's Encrypt
- **Cloudflare Tunnel** — Expose services without opening firewall ports
- **Auto-recovery** — Background health monitor restarts crashed containers
- **Auto-update** — Safe, asynchronous self-updates utilizing `--no-block` systemd service restarts
- **Security hardened** — JWT versioning, rate limiting, AES-256-GCM encrypted credentials, input validation
- **Embedded UI** — No external CDN, works offline, single binary deployment

---

## 🚀 Quick Install

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh | sudo bash
```

This installs Dockpal as a systemd service on port `3012`. Open `http://localhost:3012` and log in with `admin` / `admin`.

> **Note:** The installer supports Debian/Ubuntu on `linux/amd64` only.

---

## 📦 Manual Install

**Requirements:** Linux, Docker, Go 1.25+ (for building from source)

### Build from source

```bash
git clone https://github.com/sdldev/dockpal.git
cd dockpal
go build -o dockpal .
sudo mv dockpal /usr/local/bin/
```

### Setup data directory

```bash
sudo mkdir -p /opt/dockpal/data
sudo chown $USER:$USER /opt/dockpal/data
```

### Run

```bash
dockpal server
```

Visit `http://localhost:3012` and sign in.

### Run as systemd service (production)

```bash
sudo cp dockpal.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now dockpal
```

### Run without root (local development)

By default Dockpal writes to `/opt/dockpal/`, which requires root permissions. To run as a regular user, set the data directory to a user-writable path:

```bash
# Use a local directory (e.g. ~/.dockpal or ./.data)
export DOCKPAL_DATA_DIR="$HOME/.dockpal/data"
export DOCKPAL_DB_PATH="$HOME/.dockpal/data/dockpal.db"
export DOCKPAL_LOG_PATH="$HOME/.dockpal/data/dockpal.log"

# Create the directory
mkdir -p "$DOCKPAL_DATA_DIR"

# Run
go build -o dockpal . && ./dockpal server
```

> **Note:** Compose deployments, templates, Git repos, and Traefik configs also use hardcoded paths under `/opt/dockpal/`. For full non-root functionality, create symlinks or run with `sudo` for production-style setups. See [DEVELOPMENT.md](DEVELOPMENT.md) for the reflex-based dev workflow.

### Development

See [DEVELOPMENT.md](DEVELOPMENT.md) for setting up the file-watching development workflow with reflex.

---

## 🔧 Configuration

Environment variables (optional):

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | auto-generated | JWT signing key (persisted to `/opt/dockpal/data/.secret`) |
| `DOCKPAL_DATA_DIR` | `/opt/dockpal/data` | Data directory for database and secrets |
| `DOCKPAL_DB_PATH` | `<DATA_DIR>/dockpal.db` | BBolt database path |
| `DOCKPAL_LOG_PATH` | `<DATA_DIR>/dockpal.log` | Log file path (auto-rotated at 2MB, retains 5 files) |
| `DOCKPAL_TLS_DOMAIN`| | Domain name to request ACME Let's Encrypt TLS certificates |
| `DOCKPAL_TLS_CERT`  | | Path to custom SSL/TLS certificate file |
| `DOCKPAL_TLS_KEY`   | | Path to custom SSL/TLS private key file |

Reset admin password:

```bash
dockpal reset-password
```

---

## 📋 Recommendations

- **Secure with HTTPS/TLS** — Either configure built-in TLS (via Let's Encrypt or custom cert environment variables) or run behind a reverse proxy (Traefik, Caddy, Nginx).
- **Change default password** immediately after first login
- **Use templates first** before writing custom compose files — saves time and avoids common pitfalls
- **Mark critical containers** with label `dockpal.auto-recover=true` to enable auto-restart
- **Backup `/opt/dockpal/data/`** regularly — contains your service configs, domains, and credentials hash

---

## 🛠️ Tech Stack

Go 1.25 · Gin · BBolt · Docker SDK · Alpine.js · Tailwind CSS · Chart.js

---

## 📁 Project Structure

```
dockpal/
├── main.go              # Entry point, CLI commands
├── internal/
│   ├── auth/            # JWT, login, password, secret management
│   ├── db/              # BBolt persistence (users, services, domains, registries)
│   ├── docker/          # Docker SDK wrapper, compose parser, container edit, health monitor
│   ├── registry/        # Private registry credential management (AES-256-GCM encryption)
│   ├── server/          # Gin routes, middleware, rate limiter
│   ├── traefik/         # Reverse proxy config generator
│   ├── tunnel/          # Cloudflare tunnel lifecycle
│   ├── git/             # Git deploy helper
│   ├── logging/         # Log file rotation
│   ├── update/          # Version check, cache, scheduler, binary update service
│   └── validator/       # Input validation (names, URLs, env vars)
├── web/
│   ├── index.html       # Shell with #include directives
│   ├── pages/           # One file per route (dashboard, containers, registry, etc.)
│   ├── partials/        # Reusable components (sidebar, dialogs, toast)
│   ├── assets/
│   │   ├── app.js       # Module orchestrator
│   │   ├── modules/     # Feature modules (auth, charts, containers, registry, ...)
│   │   ├── styles.css   # Custom CSS
│   │   └── vendor/      # Tailwind, Alpine.js, Chart.js (offline-ready)
│   └── embed.go         # go:embed + HTML assembler
└── templates/           # Individual JSON files per template (nginx.json, postgres16.json, ...)
```

---

## 📄 License

MIT