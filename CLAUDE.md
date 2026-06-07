# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Commands

```bash
make build              # build ./dockpal (version from git tags)
make build-linux-amd64  # cross-compile dockpal-linux-amd64
make dev                # build + run server on :3012, data in .data/
make dev-watch          # build + run with hot reload via reflex (watch *.go)
make test               # go test -v ./...
make lint               # go vet ./...
make install-hooks      # install .git/hooks/pre-commit (runs vet + test)
make clean              # remove binaries and coverage.out
```

**Run targeted tests:**

```bash
go test ./internal/server -run TestName -v
go test ./internal/docker -run TestName -count=1      # bypass cache
go test ./... -run 'TestA|TestB'
```

**Dev requirements:** Go 1.25+, Docker daemon running.

**First startup:** server auto-creates `admin` user and prints a generated password to the log. Set `DOCKPAL_INITIAL_ADMIN_PASSWORD` before first startup to override.

**Hot reload:** `make dev-watch` uses `reflex` to watch `*.go` files (excluding `*_test.go`), rebuilds and restarts the server on every save. Frontend changes (HTML/JS in `web/`) take effect on browser refresh without restarting.

## Runtime and configuration

Server subcommands: `server`, `backup`, `restore`, `reset-password`, `version`, `help`.

Default port: `3012` (HTTP), `3443` (TLS). Override with `PORT`.

Key environment variables:

| Variable | Default | Purpose |
|---|---|---|
| `DOCKPAL_DATA_DIR` | `/opt/dockpal/data` | Root for db, log, secret, backups |
| `DOCKPAL_DB_PATH` | `<data>/dockpal.db` | BBolt database path |
| `DOCKPAL_LOG_PATH` | `<data>/dockpal.log` | Rotating log path |
| `DOCKPAL_SECRET_PATH` | `<data>/.secret` | JWT signing key file |
| `JWT_SECRET` | — | Override JWT key directly |
| `DOCKPAL_INITIAL_ADMIN_PASSWORD` | random | Bootstrap admin password — **only used when admin user does not yet exist in DB** (first startup). Has no effect on subsequent restarts or updates. |
| `DOCKPAL_BACKUP_INTERVAL` | `24h` | Scheduled backup interval (`0` = off) |
| `DOCKPAL_BACKUP_RETENTION` | `168h` | Backup retention window |
| `DOCKPAL_AUDIT_LOG_RETENTION` | `2160h` | Audit log retention |
| `DOCKPAL_TLS` | `false` | Enable TLS |
| `DOCKPAL_TLS_CERT` | — | TLS cert path |
| `DOCKPAL_TLS_KEY` | — | TLS key path |
| `DOCKPAL_TLS_DOMAIN` | — | Domain for ACME auto-cert |
| `DOCKPAL_AGENT_IMAGE` | — | Image for remote agent install commands |

## Architecture

Dockpal is a single Go binary: Gin HTTP server + embedded Alpine.js/Tailwind UI + BBolt database.

`main.go` wires: data/log paths → BBolt DB → JWT secret → Docker client → health monitor → agent manager → metrics collector → HTTP routes → backup scheduler → audit retention worker.

### Backend packages (`internal/`)

| Package | Responsibility |
|---|---|
| `server` | Gin setup, middleware, route registration, RBAC, audit logging, WebSocket, SSE, instance-scoped routes |
| `auth` | Login, JWT create/validate (token-version checks), roles, password reset, secret loading |
| `db` | BBolt persistence — users, services, domains, registry creds, instances, audit logs, webhooks |
| `agent` | `AgentClient` interface — returns local/direct/WebSocket-backed client by instance ID |
| `docker` | Moby client wrapper — containers, images, compose deploy, streamed events, file ops, auto-recovery |
| `registry` | Private registry credentials encrypted with key derived from JWT secret |
| `health` | Health check handlers — uses `DBPinger` interface (injected `*db.DB`), never opens a second DB file |
| `backup` | Scheduled + on-demand backup, restore, SHA-256 checksum, retention cleanup |
| `metrics` | Prometheus metrics registration and collector |
| `git` | Git clone/pull for deploy |
| `ssh` | SSH helpers for remote agent install |
| `installer` | Remote DockPal Agent install command generation |
| `traefik` | Traefik reverse proxy/domain integration |
| `tunnel` | Cloudflare Tunnel setup and teardown |
| `logging` | Rotating log file setup |
| `validator` | Input validation helpers |
| `config` | Config/env helpers |

### Routing model

Routes are registered in `internal/server/routes.go` via `RegisterRoutes(...)`.

**Unauthenticated:** `/api/login`, `/api/webhooks/deploy/:id`, `/api/config`, `/api/docs`, `/api/metrics`, health endpoints.

**Protected (any authenticated user):** all read routes.

**Operator:** container mutations, deploy, images, files write, domains, registry, webhooks.

**Admin:** user management, API keys, audit logs, backup trigger, role assignment, tunnel.

**Instance-scoped routes** live under `/api/instances/:instance_id/...`. `InstanceMiddleware` resolves the instance, sets `agent_client` / `instance_id` / `database` / `registry_manager` in Gin context, and returns 503 for offline agents.

**WebSocket routes** that can't send `Authorization` headers accept JWT via query param `?token=...` and still validate Origin against the request host.

### Agent client model

`agentMgr.GetClient(instanceID)` returns the right client:
- `"local"` → Docker client on the server itself
- direct agents → HTTP client to the remote host
- edge agents → WebSocket-backed client (agent dials in)

New multi-instance features: use the `AgentClient` interface via instance-scoped routes, not the local Docker client directly.

### Frontend

`web/index.html` uses `<!--#include "...">` directives; `web.AssembleHTML()` resolves them at startup.

Alpine modules in `web/assets/modules/` attach to `window.Dockpal.*`. `web/assets/app.js` merges all module descriptors into one `dockpalApp()` object.

**To add a module:**
1. Create `web/assets/modules/myfeature.js` → `window.Dockpal.myfeature = { … }`
2. Add `<script src="/assets/modules/myfeature.js"></script>` in `web/index.html` **before** `app.js`
3. Add `D.myfeature` to the spread/merge array in `web/assets/app.js`

Templates (deploy presets) are JSON files in `templates/`. Runtime checks `./templates/` first, then `/opt/dockpal/templates`.

## Testing notes

- Property tests use `pgregory.net/rapid`. Baseline guidance in `scripts/pbt-baseline.txt`.
- Prefer focused package tests while iterating; run `make test && make lint` before committing.
- Some tests require a running Docker daemon (docker integration paths).
- `internal/health` tests use a `mockDB` struct implementing `DBPinger` — no real DB file is opened by the health checker.
- The `internal/update` package has been removed. Do not re-add it; the self-update mechanism was removed intentionally.
- No `dockpal-agent/` subdirectory exists in this repo — agent client code lives in `internal/agent/`.
- Dev data lives in `.data/`; do not assume `/opt/dockpal/` exists locally.
