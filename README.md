# Dockpal

Self-hosted Docker management panel — single binary, embedded UI, no dependencies.

Manage containers, deploy compose stacks, monitor resources, control multiple remote Docker hosts from one dashboard.

---

## Quick Start

### Production Install (Debian/Ubuntu)

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh | sudo bash
```

Installs Docker (if missing), provisions `/opt/dockpal`, and sets up the systemd
service at `/etc/systemd/system/dockpal.service`.

#### Post-install

```bash
# Check status
systemctl status dockpal

# Read logs
journalctl -u dockpal -f

# Get admin password (first run only)
journalctl -u dockpal | grep "generated password"

# Set a custom admin password (only before the first start)
systemctl set-environment DOCKPAL_INITIAL_ADMIN_PASSWORD=mypassword
systemctl start dockpal
```

The `DOCKPAL_INITIAL_ADMIN_PASSWORD` variable is only read when the `admin` user
does not exist yet — it has no effect on later restarts. To change the password
on an existing install, run:

```bash
dockpal reset-password --username admin --password mypassword
```

> ⚠️ **Remote servers**: when the host has a public (non-RFC-1918) IP address,
> the installer and `dockpal install` **require** `DOCKPAL_INITIAL_ADMIN_PASSWORD`
> to be set — an auto-generated password would otherwise be leaked to
> `journalctl`/terminal history on a publicly reachable box. The installer prints
> a warning with the exact `systemctl set-environment` commands when it detects
> this.

Update an existing installation with `update.sh`:

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh | sudo bash
```

---

## Update

Update an existing installation with `update.sh`. It resolves the release,
verifies the download, backs up the current binary and templates, and rolls back
automatically if the updated panel fails its health check.

**Manual update:**

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh | sudo bash
```

**Daily update (cron):** save the script once, then schedule it.

```bash
# Download the updater (one-time)
sudo curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh \
  -o /opt/dockpal/update.sh && sudo chmod +x /opt/dockpal/update.sh

# Edit crontab
crontab -e

# Add line (daily at 2 AM)
0 2 * * * /opt/dockpal/update.sh >> /var/log/dockpal-update.log 2>&1
```

Optional environment variables:

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_VERSION` | `latest` | Release tag to install |
| `DOCKPAL_REPO` | `sdldev/dockpal` | GitHub repository |
| `DOCKPAL_FORCE` | `0` | Force reinstall even if already up-to-date |
| `DOCKPAL_UPDATE_TEMPLATES` | `1` | Refresh templates from release |

Backup automatic on every update. Rollback happens automatically if health check fails.

---

## Configuration

All via environment variables. No config file needed.

| Variable | Default | Description |
|---|---|---|
| `DOCKPAL_DATA_DIR` | `/opt/dockpal/data` | Root directory for db, log, backups |
| `PORT` | `3012` | Server listen port |
| `DOCKPAL_TLS_DOMAIN` | — | Domain for ACME/Let's Encrypt auto-cert |
| `DOCKPAL_BACKUP_INTERVAL` | `24h` | Scheduled backup interval (`0` = disabled) |
| `DOCKPAL_BACKUP_RETENTION` | `168h` | Backup retention window (7 days) |
| `DOCKPAL_INITIAL_ADMIN_PASSWORD` | random | Admin password (only on first startup; **required on remote hosts**) |

### TLS modes

- **Let's Encrypt**: `DOCKPAL_TLS_DOMAIN=panel.example.com DOCKPAL_TLS=true ./dockpal server`
- **Custom cert**: `DOCKPAL_TLS=true DOCKPAL_TLS_CERT=/path/cert.crt DOCKPAL_TLS_KEY=/path/key.crt ./dockpal server`
- **Self-signed**: `DOCKPAL_TLS=true ./dockpal server` (testing only)

---

## Development Mode

Dockpal adalah satu binary Go dengan frontend Svelte 5 SPA yang ter-embed dan dilayani di `/`. Backend Go dan SPA berkomunikasi lewat API `/api`.

### Prasyarat

- Go 1.26+
- Node.js 22+ & npm (untuk frontend Svelte)
- Docker daemon berjalan (sebagian test & fitur compose stacks)
- **Docker Compose CLI plugin** (`docker compose version`) — wajib untuk fitur
  Stacks (Dockge-style compose). Tanpa plugin ini, endpoint `/api/stacks*`
  merespons `501 Not Implemented`. Stack disimpan sebagai
  `<DOCKPAL_DATA_DIR>/../compose/<stack>/compose.yaml` (+ `.env` per stack dan
  `global.env` bersama di direktori yang sama).

### 1. Full-stack mode (binary tunggal) — untuk mengerjakan backend Go

Perubahan `.go` perlu rebuild.

```bash
make dev          # build + jalankan server di :3012, data di .data/
make dev-watch    # hot reload backend via reflex (watch *.go, rebuild otomatis)
```

- Server berjalan di `http://localhost:3012`
- UI: `http://localhost:3012/` (SPA Svelte; route client-side seperti `/dashboard`, `/fleet`, `/containers/:id`)
- Admin dibuat otomatis saat first-run; password tercetak di log, atau set lebih dulu:

```bash
DOCKPAL_INITIAL_ADMIN_PASSWORD=dev123 make dev
```

> Data dev ada di `./.data/` (bbolt, log, backups). Hapus folder ini untuk reset total. `/opt/dockpal` tidak dipakai di mode dev.

### 2. Svelte dev mode (HMR) — untuk mengerjakan SPA

Backend jalan seperti biasa, frontend dikembangkan lewat Vite dev server dengan hot module replacement:

```bash
# Terminal 1 — backend di :3012
make dev

# Terminal 2 — Vite dev server di :5173 (proxy /api dan /ws ke :3012)
make svelte-dev
```

Buka `http://localhost:5173/`. Semua panggilan API dari SPA di-proxies ke backend (lihat `proxy` di `svelte/vite.config.ts`), jadi tidak perlu rebuild saat mengubah komponen Svelte — browser langsung hot-reload.

Perubahan yang diambil Vite dev server mencakup: `svelte/src/**` (komponen, lib, store). Cache Vite ada di `svelte/node_modules/.vite` — hapus kalau resolusi import terasa basi.

### 3. Setelah selesai mengubah SPA: embed + build produksi

```bash
make prod-build   # svelte-embed (vite build → copy ke web/svelteDist) + go build
```

Atau bertahap:

```bash
make svelte-build   # vite build ke svelte/dist
make svelte-embed   # copy svelte/dist → web/svelteDist (di-embed via go:embed)
make build          # compile binary ./dockpal
```

Binary `./dockpal` hasilnya menyajikan SPA terbaru di `/`.

> Build multi-platform: `make cross` menghasilkan `dockpal-linux-amd64` +
> `dockpal-linux-arm64` + `SHA256SUMS.txt` (ARM64 mendukung Raspberry Pi /
> Graviton). macOS sengaja tidak di-support karena `internal/agent` memakai
> syscall Linux-only (`syscall.Sysinfo`, `/proc`, cgroups).

### Kualitas kode

```bash
make test          # go test -race ./...
make test-integration  # integration tests (build tag: integration) with -race
make lint          # go vet ./...
make svelte-check  # svelte-check (type check SPA)
make svelte-test   # vitest (unit test SPA)
```

Pre-commit hook menjalankan `go vet` + `go test` sebelum commit:

```bash
make install-hooks
```

### Debugging tips

- **Login "invalid credentials" padahal log mencetak password admin** — pesan "Generated initial admin password" hanya berlaku saat user admin **pertama kali dibuat** (first-run). User yang sudah ada tidak pernah diubah oleh restart; gunakan `./dockpal reset-password` untuk menggantinya.
- **`dockpal install` gagal "remote installation requires DOCKPAL_INITIAL_ADMIN_PASSWORD"** — host terdeteksi punya IP publik (non-RFC-1918), sehingga password acak tidak diizinkan karena akan tercetak ke log/terminal di server yang bisa diakses publik. Set env var-nya lebih dulu, atau jalankan `dockpal install --password ...` eksplisit. Deteksi berbasis interface IP; di belakang NAT dengan IP privat lokal, pembatasan ini tidak berlaku.
- **"instance not found" di halaman Stacks** — localStorage `dockpal_selected_instance` menunjuk instance yang sudah tidak ada; halaman otomatis fallback ke `local`, atau clear localStorage.
- **Perubahan SPA tidak muncul di `/`** — Anda lupa `make svelte-embed` + rebuild binary; binary hanya menyajikan hasil embed (`web/svelteDist`).
- **`go vet` gagal karena fake test client** — semua fake yang meng-implement `agent.AgentClient` harus menyediakan stub untuk seluruh method interface (termasuk stack operations).
- **Compose stack gagal deploy: "service has neither an image nor a build context"** — service tanpa `image` di compose.yaml; isi image lewat form service sebelum deploy.
- **`reset-password` gagal "timeout"** — server masih berjalan dan mengunci bbolt; matikan dulu (`pkill -f dockpal-dev` atau `systemctl stop dockpal`).

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
  version           Print version
  help              Show help
```

Examples:

```bash
# Start server
./dockpal server

# On-demand backup
./dockpal backup --output /tmp/backup.db

# First-time setup on a fresh data dir (create admin without starting server)
DOCKPAL_DB_PATH=/opt/dockpal/data/dockpal.db ./dockpal install --username admin --password NewPass123

# On a remote host, DOCKPAL_INITIAL_ADMIN_PASSWORD is required:
DOCKPAL_INITIAL_ADMIN_PASSWORD=NewPass123 DOCKPAL_DB_PATH=/opt/dockpal/data/dockpal.db \
  ./dockpal install --username admin

# Reset admin password (stop server first)
./dockpal reset-password --username admin --password NewPass123

# View version
./dockpal version
```

> Passwords set via UI are preserved across updates. Only reset-password CLI command changes them.

---

## Features

| Category | Details |
|---|---|
| **Containers** | List, start, stop, restart, delete, inspect, logs, stats |
| **Deploy** | Compose YAML, Git repo, 38 built-in templates (PostgreSQL, MariaDB, Redis, Grafana, Adminer, …) |
| **Images** | Pull, updates check, registry auth, prune |
| **Files** | Browse, read, write, upload, download inside containers |
| **Domains** | Traefik integration, custom routing, SSL |
| **Monitoring** | Prometheus metrics, real-time charts, health checks |
| **Multi-host** | Manage remote Docker hosts (direct HTTP or edge WebSocket) |
| **Security** | RBAC (admin/operator/viewer), JWT auth, audit log, remote-deploy password enforcement |
| **Backup** | Scheduled + manual, SHA-256 checksum, retention policy |

---

## Health & Metrics

Endpoints:

| Path | Description |
|---|---|
| `/health` | Full health report (HTTP 200/503) |
| `/api/metrics` | Prometheus metrics (requires auth — JWT bearer or `X-API-Key`, viewer role or higher) |
| `/api/docs` | API documentation UI |

Example metrics:

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
├── Makefile                   # Build/test/dev targets (lihat: make help)
├── internal/                  # Go packages
│   ├── server/                # Gin routes, middleware, RBAC, stacks routes
│   ├── agent/                 # AgentClient (local/direct/edge) + WS client helpers
│   ├── composecli/            # docker compose CLI runner (stacks engine)
│   ├── docker/                # Moby client wrapper + stack store
│   ├── auth/                  # JWT, login, passwords
│   ├── security/              # Remote-host detection, deploy password enforcement
│   ├── config/                # Env config loading + validation
│   ├── integration/           # Integration tests (build tag: integration)
│   ├── db/                    # BBolt persistence
│   └── ...
├── svelte/                    # SPA Svelte 5 source (served at /)
│   ├── src/components/        # Pages + compose components
│   ├── src/lib/               # API clients, yaml-sync, stores, router
│   ├── tests/                 # Vitest unit tests
│   └── vite.config.ts         # Dev proxy /api → :3012
├── web/                       # Embedded SPA (web/svelteDist = vite build output)
├── templates/                 # JSON deploy templates
└── update.sh                  # Self-updater script
```

> Panduan kontribusi untuk AI/editor (perintah, arsitektur, testing notes) ada di [AGENTS.md](AGENTS.md).

---

## License

MIT
