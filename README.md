# Dockpal

Self-hosted Docker management panel — single binary, embedded UI, no dependencies.

Manage containers, deploy compose stacks, monitor resources, control multiple remote Docker hosts from one dashboard.

---

## Quick Start

### Production Install (Debian/Ubuntu)

```bash
curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh | sudo bash
```

Installs systemd service at `/etc/systemd/system/dockpal.service`.

#### Post-install

```bash
# Check status
systemctl status dockpal

# Read logs
journalctl -u dockpal -f

# Get admin password (first run only)
journalctl -u dockpal | grep "admin password"

# Set custom password for next restart
DOCKPAL_INITIAL_ADMIN_PASSWORD=mypassword systemctl restart dockpal
```

Update command: same as install.

---

## Update

Auto-update via `update.sh` script. Runs manually or via cron.

**Daily update (cron):**

```bash
# Edit crontab
crontab -e

# Add line (daily at 2 AM)
0 2 * * * /opt/dockpal/update.sh >> /var/log/dockpal-update.log 2>&1
```

Manual update:

```bash
/opt/dockpal/update.sh
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
| `DOCKPAL_INITIAL_ADMIN_PASSWORD` | random | Admin password (only on first startup) |

### TLS modes

- **Let's Encrypt**: `DOCKPAL_TLS_DOMAIN=panel.example.com DOCKPAL_TLS=true ./dockpal server`
- **Custom cert**: `DOCKPAL_TLS=true DOCKPAL_TLS_CERT=/path/cert.crt DOCKPAL_TLS_KEY=/path/key.crt ./dockpal server`
- **Self-signed**: `DOCKPAL_TLS=true ./dockpal server` (testing only)

---

## CLI Reference

```bash
dockpal <subcommand> [flags]

Subcommands:
  server            Start the HTTP server
  backup            Create a database backup
  restore           Restore database from backup
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
| **Deploy** | Compose YAML, Git repo, 5 built-in templates (PostgreSQL 17, MariaDB, Redis 7, Grafana, Adminer) |
| **Images** | Pull, updates check, registry auth, prune |
| **Files** | Browse, read, write, upload, download inside containers |
| **Domains** | Traefik integration, custom routing, SSL |
| **Monitoring** | Prometheus metrics, real-time charts, health checks |
| **Multi-host** | Manage remote Docker hosts (direct HTTP or edge WebSocket) |
| **Security** | RBAC (admin/operator/viewer), JWT auth, audit log |
| **Backup** | Scheduled + manual, SHA-256 checksum, retention policy |

---

## Health & Metrics

Endpoints:

| Path | Description |
|---|---|
| `/health` | Full health report (HTTP 200/503) |
| `/api/metrics` | Prometheus metrics (no auth) |
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
├── main.go                    # Entry point
├── Makefile                   # Build/test targets
├── internal/                  # Go packages
│   ├── server/                # Gin routes, middleware, RBAC
│   ├── docker/                # Moby client wrapper
│   ├── auth/                  # JWT, login, passwords
│   ├── db/                    # BBolt persistence
│   └── ...
├── web/                       # Embedded frontend
├── templates/                 # JSON deploy templates
└── update.sh                  # Self-updater script
```

---

## License

MIT
