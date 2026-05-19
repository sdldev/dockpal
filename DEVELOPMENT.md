# Development Guide

## Prerequisites

- **Go 1.25+** — required for building DockPal and installing reflex
- **Docker** — must be running for container management features
- **reflex** — file watcher for automated rebuild/restart/test workflow

## Installing Reflex

```bash
go install github.com/cespare/reflex@latest
```

Ensure `$GOPATH/bin` (or `$HOME/go/bin`) is in your `PATH`.

## Environment Setup

The server defaults to `/opt/dockpal/data/` for its database and logs, which requires root permissions. For local development, set these environment variables to use a project-local `.data/` directory:

```bash
export DOCKPAL_DATA_DIR="$(pwd)/.data"
export DOCKPAL_DB_PATH="$(pwd)/.data/dockpal.db"
export DOCKPAL_LOG_PATH="$(pwd)/.data/dockpal.log"
```

Add these to your shell profile or run them before starting the watchers. The `.data/` directory is already in `.gitignore`.

## Running All Watchers

Start the full development workflow with a single command:

```bash
reflex -c reflex.conf
```

> **Note:** This must be run from the project root directory where `reflex.conf` is located.

This starts all 4 watchers simultaneously. When you save a `.go` file, the binary rebuilds, the server restarts, and tests run automatically.

## Watchers Overview

| Watcher | Purpose | Pattern |
|---------|---------|---------|
| Go Source Rebuild | Recompiles binary on Go file changes | `\.go$` (excluding `vendor/`, `*_generated.go`, `*.pb.go`) |
| Server Auto-Restart | Restarts dev server when binary updates | `^dockpal$` |
| Web Asset Notification | Prints changed frontend file paths | `^web/.*\.(html|css|js)$` (excluding `web/assets/vendor/`) |
| Test Runner | Runs full test suite after Go changes | `\.go$` (with 2s delay for rebuild) |

## Running Individual Watchers

You can run each watcher independently without the `reflex.conf` file:

### Go Source Rebuild

```bash
reflex -r '\.go$' -R '^vendor/' -R '_generated\.go$' -R '\.pb\.go$' -- sh -c 'echo "Building..." && go build -o dockpal . && echo "Build succeeded at $(date +%H:%M:%S)"'
```

### Server Auto-Restart

```bash
reflex -sr '^dockpal$' -- ./dockpal server
```

### Web Asset Notification

```bash
reflex -r '^web/.*\.(html|css|js)$' -R '^web/assets/vendor/' -- echo "⟳ changed: {}"
```

### Test Runner

```bash
reflex -r '\.go$' -R '^vendor/' -R '_generated\.go$' -R '\.pb\.go$' -- sh -c 'sleep 2 && go test ./...'
```

## Workflow Tips

- **2-second delay assumption:** The test runner uses `sleep 2` before running tests to allow the rebuild watcher to finish. If your build takes longer than 2 seconds, tests may occasionally run against the previous binary. Re-run tests manually if you suspect stale results.
- **Selective execution:** Run only the watchers you need. For example, if you're only working on frontend assets, run just the Web Asset Notification watcher.
- **Server restart depends on rebuild:** The server auto-restart watcher triggers from the binary file changing, not from Go source changes. If the build fails, the server keeps running with the last successful binary.
- **Vendor directory ignored:** Changes in `vendor/` and `web/assets/vendor/` never trigger watchers.
- **No dry-run mode:** Reflex does not have a built-in dry-run or validation mode. Run `reflex -c reflex.conf` and verify watchers start without parse errors.
