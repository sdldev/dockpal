# 🐳 Dockpal

A simple, lightweight Docker management platform — single binary, embedded UI, no external dependencies.

Manage containers, deploy stacks from compose or templates, monitor resources, and configure routing with Traefik or Cloudflare Tunnel — all from a clean web interface.

---

## ✨ Features

- **Real-time monitoring** — Live CPU, memory, and network charts for host and per-container
- **One-click deploy** — 25+ pre-configured templates (PostgreSQL, Redis, Grafana, n8n, Nextcloud, etc.)
- **Compose & Git deploy** — Deploy from raw YAML or Git repository with auto-pull
- **Streamed deployment logs** — WebSocket-based live progress with smart error diagnostics
- **Container management** — Start, stop, restart, remove with confirmation dialogs
- **Live log viewer** — Tail container logs over WebSocket
- **Traefik integration** — Auto-generate reverse proxy config with Let's Encrypt
- **Cloudflare Tunnel** — Expose services without opening firewall ports
- **Auto-recovery** — Background health monitor restarts crashed containers
- **Security hardening** — JWT versioning, rate limiting, path traversal protection, input validation
- **Embedded UI** — No external CDN, works offline, single binary deployment

---

## 🚀 Quick Install

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh | sudo bash
```

This installs Dockpal as a systemd service on port `3012`. Open `http://localhost:3012` and log in with `admin` / `admin`.

---

## Upgrading from Dockara

If you're upgrading from Dockara, the installer automatically handles the migration:

- **Data migration**: The installer copies all data from `/opt/dockara/` to `/opt/dockpal/`
- **Service migration**: The installer replaces the systemd unit `dockara.service` with `dockpal.service` and enables it
- **Binary cleanup**: The installer removes the old binary at `/usr/local/bin/dockara`

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

---

## 🔧 Configuration

Environment variables (optional):

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | auto-generated | JWT signing key (persisted to `/opt/dockpal/data/.secret`) |
| `DOCKPAL_DATA_DIR` | `/opt/dockpal/data` | Data directory for database and secrets |
| `DOCKPAL_DB_PATH` | `<DATA_DIR>/dockpal.db` | BBolt database path |
| `DOCKPAL_LOG_PATH` | `<DATA_DIR>/dockpal.log` | Log file path (auto-rotated at 2MB, retains 5 files) |

Reset admin password:

```bash
dockpal reset-password
```

---

## 📋 Recommendations

- **Run behind a reverse proxy** (Traefik, Caddy, Nginx) with HTTPS — Dockpal serves plain HTTP
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
│   ├── db/              # BBolt persistence (users, services, domains)
│   ├── docker/          # Docker SDK wrapper, compose parser, health monitor
│   ├── server/          # Gin routes, middleware, rate limiter
│   ├── traefik/         # Reverse proxy config generator
│   ├── tunnel/          # Cloudflare tunnel lifecycle
│   ├── git/             # Git deploy helper
│   ├── logging/         # Log file rotation
│   └── validator/       # Input validation (names, URLs, env vars)
├── web/
│   ├── index.html       # Shell with #include directives
│   ├── pages/           # One file per route (dashboard, containers, etc.)
│   ├── partials/        # Reusable components (sidebar, dialogs, toast)
│   ├── assets/
│   │   ├── app.js       # Module orchestrator
│   │   ├── modules/     # Feature modules (auth, charts, containers, ...)
│   │   ├── styles.css   # Custom CSS
│   │   └── vendor/      # Tailwind, Alpine.js, Chart.js (offline-ready)
│   └── embed.go         # go:embed + HTML assembler
└── templates/
    └── templates.json   # 25+ pre-configured app templates
```

---

## 📄 License

MIT