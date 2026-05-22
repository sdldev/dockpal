# Codebase Map

## Top-level directories

- `.github/` — GitHub Actions workflows, including release automation.
- `.kiro/` — feature specs and task plans for major initiatives.
- `.vscode/` — editor settings for local development.
- `cmd/` — auxiliary Go commands; currently `split-templates` for migrating template JSON.
- `docs/` — project documentation for health checks, metrics, and repository structure.
- `internal/` — private Go backend packages used by the main Dockpal binary.
- `scripts/` — development/test support artifacts such as property-test baseline notes.
- `templates/` — deployable service template JSON files consumed by template routes.
- `web/` — embedded frontend UI, static assets, page fragments, and partials.

## Top-level files

- `.gitignore` — ignored local files and build artifacts.
- `CLAUDE.md` — contributor guidance for agents working in this repository.
- `Makefile` — build, dev, test, lint, and cleanup targets.
- `README.md` — product overview and usage documentation.
- `dockpal-proposal.md` — strategic and technical proposal document.
- `dockpal.service` — systemd unit for running Dockpal as a service.
- `go.mod`, `go.sum` — Go module definition and checksums.
- `installer.sh` — production install script for Linux/amd64 hosts.
- `main.go` — primary application entry point and CLI command dispatcher.
- `update.sh` — operational update script with download, verification, and rollback flow.

## Application entry points and scripts

- `main.go` builds the `dockpal` binary. It dispatches these CLI subcommands:
  - `server` — starts Gin HTTP/HTTPS server, initializes data/log paths, BBolt DB, JWT secret, default admin, local Docker instance, update services, metrics, health checks, routes, and embedded UI.
  - `backup` — writes and validates a database backup.
  - `restore` — restores the database from a backup, with optional `--force`.
  - `reset-password` — resets the admin password interactively.
  - `version`, `help` — print version or CLI usage.
- `cmd/split-templates/main.go` is a maintenance command that splits a monolithic `templates.json` into individual template files and verifies round-trip loading.
- `Makefile` exposes common developer commands: `build`, `build-linux-amd64`, `dev`, `test`, `lint`, `install-hooks`, and `clean`.
- `installer.sh` installs Dockpal and supporting files on Debian/Ubuntu-style Linux systems.
- `update.sh` updates an installed Dockpal binary with checksum verification and rollback handling.
- `.github/workflows/release.yml` automates release builds and artifacts.
- `dockpal.service` runs `dockpal server` under systemd.
- `web/embed.go` embeds `web/index.html`, `web/assets`, `web/pages`, and `web/partials`; `web.AssembleHTML()` resolves include directives at server startup.

## Main backend modules

- `internal/server` — Gin router setup, middleware, API route registration, RBAC, audit logging, instance-scoped routes, WebSocket handlers, health/update endpoints, template routes, and TLS helpers.
- `internal/auth` — login handler support, JWT creation/validation, token versioning, role definitions, and secret loading.
- `internal/db` — BBolt persistence for users, services, domains, registry credentials, instances, audit logs, webhooks, and backups.
- `internal/agent` — `AgentClient` abstraction and manager for local Docker, direct remote agents, and edge/WebSocket agents.
- `internal/docker` — Moby client wrapper for containers, images, compose deployment, streamed deploy events, file operations, and recovery.
- `internal/registry` — encrypted private registry credential storage and Docker auth header generation.
- `internal/update` — version checks, release metadata caching, checksum validation, binary verification, atomic install, and restart support.
- `internal/health` — liveness/readiness/health checks for database, Docker, disk, and memory.
- `internal/metrics` — Prometheus metric registration and collection.
- `internal/backup` — scheduled backup support.
- `internal/config` — runtime configuration validation.
- `internal/git` — Git-backed deployment support.
- `internal/installer` and `internal/ssh` — remote install and conflict-detection helpers.
- `internal/logging` — rotating log writer.
- `internal/traefik` — Traefik config generation.
- `internal/tunnel` — Cloudflare Tunnel integration.
- `internal/validator` — input validation helpers.

## Frontend modules

- `web/index.html` is the embedded shell and includes page/partial fragments.
- `web/assets/app.js` composes Alpine modules into the main `dockpalApp()` object.
- `web/assets/modules/` contains feature modules for auth, dashboard, containers, domains, files, fleet/instances, image updates, images, profile, registry, services, templates, charts, UI state, and update banner.
- `web/pages/` contains page fragments for app views.
- `web/partials/` contains shared UI fragments such as header, sidebar, login, toast, and confirm dialog.
