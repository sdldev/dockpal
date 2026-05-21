# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

- `make dev` — build `./dockpal` and run the server on port 3012 with `DOCKPAL_DATA_DIR=$(pwd)/.data`.
- `make build` — build the local binary with version ldflags from git.
- `make build-linux-amd64` — cross-compile `dockpal-linux-amd64`.
- `make test` — run `go test -v ./...`.
- `make lint` — run `go vet ./...`.
- `make clean` — remove built binaries and `coverage.out`.
- `go test ./internal/server -run TestName` — run one test in a package.
- `go test ./internal/docker -run TestName -count=1` — rerun one test without cache.
- `go test ./... -run 'TestName|TestOther'` — run selected tests across packages.
- `go test ./internal/server -run TestName -v` — use verbose output for a package-level test.

Development requires Go 1.25+ and a running Docker daemon for app startup and Docker-integration paths. On first startup, the server creates an `admin` user; set `DOCKPAL_INITIAL_ADMIN_PASSWORD` before first startup to choose the bootstrap password instead of reading the generated one from logs.

## Runtime and configuration

The main binary supports `server`, `backup`, `restore`, `reset-password`, `version`, and `help` subcommands. The server defaults to HTTP port 3012, or 3443 when TLS is enabled, unless `PORT` is set.

Important environment variables:

- `DOCKPAL_DATA_DIR` — data directory for local dev/prod state; production default is `/opt/dockpal/data`.
- `DOCKPAL_DB_PATH` — BBolt database path; default is `<data dir>/dockpal.db`.
- `DOCKPAL_LOG_PATH` — rotating log path; default is `<data dir>/dockpal.log`.
- `DOCKPAL_SECRET_PATH` — JWT secret file; default is `<data dir>/.secret`.
- `JWT_SECRET` — override JWT signing key.
- `DOCKPAL_TLS`, `DOCKPAL_TLS_CERT`, `DOCKPAL_TLS_KEY`, `DOCKPAL_TLS_DOMAIN` — TLS/self-signed/ACME configuration.
- `DOCKPAL_AGENT_IMAGE` — image used when generating or running remote DockPal Agent install commands.

## Architecture

Dockpal is a single Go binary that serves a Gin API and an embedded Alpine.js/Tailwind UI. `main.go` wires the runtime: data/log paths, BBolt database, JWT secret, default admin/local instance, Docker client, health monitor, update services, agent manager, API routes, and embedded web assets.

The backend is organized by domain under `internal/`:

- `internal/server` owns Gin setup, middleware, API route registration, WebSocket endpoints, RBAC enforcement, audit logging, instance CRUD, instance-scoped routes, install-log streaming, and update endpoints.
- `internal/auth` handles login, JWT creation/validation with token-version checks, roles, password reset, and secret loading.
- `internal/db` is the BBolt persistence layer for users, services, domains, registry credentials, instances, audit logs, and webhooks.
- `internal/agent` provides the `AgentClient` abstraction used by route handlers. The manager returns a local Docker-backed client for `local`, direct HTTP clients for direct agents, and WebSocket-backed clients for edge agents.
- `internal/docker` wraps the Moby client for containers, images, compose deployment, streamed deploy events, file operations, and auto-recovery.
- `internal/registry` stores private registry credentials encrypted with a key derived from the JWT secret and supplies Docker registry auth headers.
- `internal/git`, `internal/ssh`, `internal/installer`, `internal/traefik`, `internal/tunnel`, `internal/update`, `internal/logging`, and `internal/validator` support deploy, remote install, reverse proxy, Cloudflare Tunnel, self-update, logs, and input validation flows.

The API has both legacy local routes like `/api/containers` and instance-scoped routes under `/api/instances/:instance_id/...`. New multi-instance features should usually use the instance-scoped path and the `AgentClient` interface rather than calling the local Docker client directly. `InstanceMiddleware` resolves the instance, sets `agent_client`, `instance_id`, `database`, and `registry_manager` in Gin context, and maps offline agents to 503.

RBAC is layered in `RegisterRoutes`: unauthenticated login and webhook trigger routes are registered first, protected routes use `AuthMiddleware`, and viewer/operator/admin route groups enforce role capabilities. WebSocket routes that cannot send authorization headers pass JWTs as query parameters and still validate Origin against the request host.

Templates are JSON files in `templates/`. Runtime template loading checks the local `templates` directory first, then `/opt/dockpal/templates`. Template deploy handlers replace env/port placeholders before sending compose YAML to the agent client.

The frontend lives in `web/` and is embedded by `web/embed.go`. `web/index.html` uses `<!--#include "..."-->` directives for page and partial fragments; `web.AssembleHTML()` resolves those includes at startup. Alpine modules in `web/assets/modules/` attach behavior to `window.Dockpal`, and `web/assets/app.js` merges module descriptors into one `dockpalApp()` data object. If adding a module, include its script in `web/index.html` before `web/assets/app.js` and add it to the merge list in `app.js`.

## Testing notes

The repository has extensive Go tests, including property tests using `pgregory.net/rapid` with baseline guidance in `scripts/pbt-baseline.txt`. Prefer focused package tests while iterating, then run `make test` and `make lint` before reporting completion.

Some behavior depends on Docker daemon availability or local filesystem paths such as `.data/` and `/opt/dockpal/compose`. Avoid assuming the README's `dockpal-agent/` directory exists in this checkout; the current server repo has agent client/edge/direct code under `internal/agent`, but no separate agent module directory.
