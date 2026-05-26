# Implementation Plan

## Overview

The auto-image-update spec is delivered in 6 waves, starting from data-model and store foundations, then worker logic, the HTTP layer, AgentClient extensions, frontend, and finally verification. Each task lists requirement traceability and dependencies.

## Tasks

- [x] 1. Foundations: Data model and store
  - [x] 1.1 Add `internal/db/app_updates.go` defining the `AppUpdateStage` (string) type with the constants `StagePending, StagePulling, StageRecreating, StageVerifying, StageCompleted, StageFailed, StageRolledBack`. Add the structs `AppUpdateRecord`, `ServiceUpdateInfo`, and `AppUpdateEvent` per the design. Requirements: R5.1, R5.2
  - [x] 1.2 Define the interface `AppUpdateStore` in `internal/db/app_updates.go` with the methods `SaveAppUpdate`, `AppendAppUpdateEvent`, `ListAppUpdates`, `ListAllAppUpdates`, `GetAppUpdate`, `PurgeOlderThan`. Requirements: R5.1, R5.2, R5.3, R5.4
  - [x] 1.3 Implement `*DB` as `AppUpdateStore`: add the buckets `app_updates` and `app_updates_by_id` to the `bdb.Update` list in `internal/db/db.go`. Implement the key encoding `app + 0x00 + bigEndian(math.MaxUint64 - unixMicro)`. Implement `SaveAppUpdate` to write to both buckets in a single transaction. Requirements: R5.1
  - [x] 1.4 Implement `ListAppUpdates(app, limit)` using `Cursor.Seek(prefixApp)` and forward iteration until the prefix changes or `limit` is reached. Implement `ListAllAppUpdates(instanceID, limit)` with a full bucket scan that filters by `instanceID` and sorts by `StartedAt` descending. Requirements: R5.2
  - [x] 1.5 Implement `PurgeOlderThan(retainPerApp, retainGlobal)`: scan and group by app, drop per-app over the limit, then trim to the global limit. Return the number of deleted entries. Requirements: R5.4
  - [x] 1.6 Add the unit test `internal/db/app_updates_test.go`: test save+list ordering newest-first, test get-by-id, test retention 100 per app and 1000 globally. Requirements: R5.1, R5.2, R5.4. Depends on: 1.1, 1.2, 1.3, 1.4, 1.5

- [x] 2. Compose label helpers and health probe
  - [x] 2.1 Add `SetServiceLabel(composeYAML, key, value string) (string, error)` in `internal/docker/compose.go` that performs a yaml.v3 round-trip changing the label `services.<svc>.labels.<key>`. When `value == ""` it removes the key. Other labels are preserved. Requirements: R1.3, R1.4
  - [x] 2.2 Add the unit test `compose_label_test.go`: add a new label, replace an existing label, remove a label, preserve comments and order. Requirements: R1.3, R1.4. Depends on: 2.1
  - [x] 2.3 Add `internal/docker/health_probe.go` with the `HealthProbeResult` type and the method `(*Client).HealthProbe(ctx, project, grace) ([]HealthProbeResult, error)`. Poll every 2 seconds, fail fast when `State.ExitCode != 0 && !State.Restarting`, return an aggregate error if any container is unhealthy when `grace` elapses. Requirements: R3.2
  - [x] 2.4 Add the unit test `health_probe_test.go` with a fake client interface that returns a sequence of inspect results: all healthy, container exits non-zero, timeout. Requirements: R3.2. Depends on: 2.3

- [x] 3. Auto Update Worker (backend core)
  - [x] 3.1 Add `internal/docker/auto_updater_errors.go` with the string constants `ErrPullError = "pull_error"`, `ErrAuthMissing = "auth_missing"`, `ErrComposeError = "compose_error"`, `ErrHealthProbeFailed = "health_probe_failed"`, `ErrRollbackFailed = "rollback_failed"`, `ErrUpdateAlreadyRunning = "update_already_running"`, `ErrSkippedCooldown = "skipped_cooldown"`, `ErrSkippedWindow = "skipped_window"`. Requirements: R3.4, R6.2
  - [x] 3.2 Add `AddCycleListener(fn cycleListener)` to `internal/docker/image_updater.go` with a `listeners` slice and a mutex. Call all listeners at the end of `checkAll()`. Add a helper `snapshotLocked()` that clones the cache map to a slice. Requirements: R2.1
  - [x] 3.3 Add `internal/docker/auto_updater.go` with the struct `AutoUpdateWorker` and the constructor `NewAutoUpdateWorker(client, monitor, store, feed, getAuth, getCompose, instanceID)`. Inject `cooldown`, `grace`, `concurrency`, `enabled` from env vars with defaults per requirements R7. Requirements: R7.1, R7.2, R7.3, R7.4, R7.5
  - [x] 3.4 Implement `(w *AutoUpdateWorker) Start(ctx)` that calls `monitor.AddCycleListener(w.onCycle)`. `onCycle` builds the Pull_Plan: for every `ImageUpdateStatus` with `has_update=true`, look up the containers using that imageRef via `ListContainersWithLabel("dockpal.auto-update=true")`, group by the `dockpal.project` label. Requirements: R1.1, R1.2, R2.1, R2.3
  - [x] 3.5 Implement `(w *AutoUpdateWorker) TriggerApp(ctx, app, bypassCooldown, bypassWindow, triggeredBy)`: acquire the per-app mutex via `sync.Map`; if held already return `ErrUpdateAlreadyRunning`. Validate cooldown (skip if `bypassCooldown`) by comparing `lastRecord.StartedAt + cooldown > time.Now()`. Validate window (skip if `bypassWindow`). Requirements: R2.4, R2.5, R6.1, R6.2
  - [x] 3.6 Implement the pipeline inside `TriggerApp`:
    1. List the project containers, capture `previous_image` from `ImageInspect.RepoDigests[0]`
    2. Save record at stage `pulling`, emit feed
    3. Loop the services with `has_update`, call `client.ForcePullImage(ctx, image, getAuth(image))`. On error: save `failed` with code `pull_error` or `auth_missing`, emit, return
    4. Save record at stage `recreating`, emit
    5. Call `client.DeployCompose(ctx, app, composeYAML, registryAuths, true)`. On error: rollback and save `rolled_back` or `failed`
    6. Save record at stage `verifying`, emit
    7. Call `client.HealthProbe(ctx, app, grace)`. On unhealthy: rollback
    8. Save record at stage `completed`, emit
    Requirements: R2.6, R3.1, R3.2, R3.3, R3.4, R3.5
  - [x] 3.7 Implement the helper `(w *AutoUpdateWorker) rollback(ctx, app, composeYAML, previousDigests map[string]string)`: parse the compose, replace each service `image:` with `repo@<previousDigest>`, call `DeployCompose(... forcePull=false)`. Return an error if rollback fails. Requirements: R3.3, R3.5
  - [x] 3.8 Simple window parser `parseWindow(spec string) (allowed func(time.Time) bool, error)`: support the `HH:MM-HH:MM` form (host local time). Other formats return an error "unsupported window spec". Requirements: R2.5
  - [x] 3.9 Add the unit test `auto_updater_test.go` with a mock client (separate `autoUpdaterClient` interface so it can be stubbed):
    - cooldown enforcement
    - window enforcement
    - opt-in label filter (skip apps without the label)
    - happy path
    - pull failure → no rollback, prod untouched
    - compose failure → rollback called
    - health probe fail → rollback called
    - rollback failure → status `failed`
    - per-app mutex blocks concurrent triggers
    Requirements: R1.1, R1.2, R2.4, R2.5, R3.1, R3.2, R3.3, R3.5, R6.2. Depends on: 3.5, 3.6, 3.7
  - [x] 3.10 (PBT) Add `auto_updater_prop_test.go` with property tests using `pgregory.net/rapid`:
    - P1: for a random plan `[apps]`, after processPlan no mutex is held
    - P2: for a random sequence of events per app, the feed subscriber receives an ordered subset matching the state machine
    Requirements: R2.6, R3.1-R3.5. Depends on: 3.5, 3.6

- [x] 4. App Update Feed (SSE broadcaster)
  - [x] 4.1 Add `internal/server/app_update_feed.go` with `AppUpdateFeed`, `AppUpdateFeedEvent`, the method `Publish` (non-blocking) and `Subscribe() (<-chan AppUpdateFeedEvent, func())`. Per-subscriber buffer 16. Requirements: R4.4
  - [x] 4.2 Add the unit test `app_update_feed_test.go`:
    - Subscribe + Publish + Receive ordering
    - Slow subscriber does not block the publisher (events are dropped for it)
    - Unsubscribe via the returned func
    Requirements: R4.4. Depends on: 4.1

- [x] 5. HTTP endpoints (local + instance-scoped)
  - [x] 5.1 Add `AppSummary` and `AppServiceSummary` in `internal/docker/compose.go` (or a new file `internal/docker/apps.go`). Implement `(c *Client) ListApps(ctx) ([]AppSummary, error)`: list every container with the `dockpal.project` label, group by project, query `ImageUpdateMonitor.GetStatus(image)` per service. Requirements: R4.1, R4.2
  - [x] 5.2 Wire `AutoUpdateWorker` and `AppUpdateFeed` in `internal/server/routes.go` after `imageUpdateMonitor.Start()`. Create `feed := server.NewAppUpdateFeed()`. Create `worker := docker.NewAutoUpdateWorker(...)`. Call `worker.Start(ctx)`. Pass `feed.Publish` as the callback to the worker. Requirements: R2.1, R7.1
  - [x] 5.3 Add handlers in `routes.go`:
    - `GET /apps` → returns `[]AppSummary`, filtered by query `instance_id`
    - `GET /apps/:name/updates` → list from the store
    - `GET /apps/:name/updates/:attemptID` → single record with events
    - `POST /apps/:name/update` → call `worker.TriggerApp(ctx, name, true, true, "user:"+username)`. Return 202 with the attemptID, or 409 when `ErrUpdateAlreadyRunning`
    - `PATCH /apps/:name/auto-update` → load db.Service, call `SetServiceLabel`, persist db, call `DeployCompose(... forcePull=false)`
    - `GET /apps/updates/stream` → SSE handler that calls `feed.Subscribe()` and writes each event as `data: <json>\n\n`
    Apply the role middleware per R8.1, R8.2, R8.3.
    Requirements: R4.1, R4.2, R4.3, R4.4, R6.1, R6.2, R6.3, R8.1, R8.2, R8.3
  - [x] 5.4 Add instance-scoped handlers in `instance_scoped_routes.go`:
    - `GET /instances/:instance_id/apps` → delegate to `agentClient.ListApps`
    - `GET /instances/:instance_id/apps/:name/updates` → delegate
    - `POST /instances/:instance_id/apps/:name/update` → delegate
    - `PATCH /instances/:instance_id/apps/:name/auto-update` → delegate
    - `GET /instances/:instance_id/apps/updates/stream` → proxy SSE from the agent
    Requirements: R9.1, R9.3, R9.4
  - [x] 5.5 Add handler unit tests with `httptest.NewRecorder` and a mock worker:
    - `POST /apps/:name/update` returns 202 with the attemptID
    - 409 on `ErrUpdateAlreadyRunning`
    - `PATCH` updates the label and triggers redeploy
    - SSE handler writes content-type `text/event-stream` and the format `data: ...\n\n`
    - Role middleware blocks viewers from POST
    Requirements: R4, R6, R8. Depends on: 5.3

- [x] 6. AgentClient extension (multi-instance)
  - [x] 6.1 Add methods to the `AgentClient` interface in `internal/agent/types.go`: `ListApps(ctx, instanceID) ([]docker.AppSummary, error)`, `ListAppUpdates(ctx, app, limit) ([]db.AppUpdateRecord, error)`, `GetAppUpdate(ctx, attemptID) (*db.AppUpdateRecord, error)`, `TriggerAppUpdate(ctx, app) (string, error)`, `SetAppAutoUpdate(ctx, app, enabled bool) error`. Requirements: R9.1
  - [x] 6.2 Implement `LocalClient` in `internal/agent/local.go`: delegate `ListApps` to `dockerClient.ListApps`; the rest delegate via the worker reference (the worker is stored on `LocalClient` at startup). Requirements: R9.1
  - [x] 6.3 Implement `EdgeClient` in `internal/agent/edge.go` and `DirectClient` in `internal/agent/direct.go`: HTTP requests to remote agent endpoints with a request body matching the schema. Requirements: R9.1, R9.3, R9.4
  - [x] 6.4 Add agent-side handlers in `internal/agent/server.go` (or equivalent) to expose `/apps/...` to the edge. Reuse the `AutoUpdateWorker` instance created on agent boot. Requirements: R9.1
  - [x] 6.5 Update `internal/server/container_protection_test.go` mock client to implement the new methods (return nil/empty). Requirements: testability. Depends on: 6.1

- [x] 7. Frontend: Apps page and real-time feed
  - [x] 7.1 Add `web/pages/apps.html` with the table layout: columns Name, Services count, Auto-update toggle (Alpine.js), Update status badge (5 colors), Last attempt time, Actions (`Update now`, expand). Use the existing utility classes from styles.css. Requirements: R4.1, R4.2, R4.5
  - [x] 7.2 Add the detail panel (expand-row pattern or side panel) with the tabs `Overview`, `History`, `Logs (live)`. Overview lists services with image, current digest (8 chars), latest digest (8 chars), state. History is a table from `loadAppHistory`. Live logs are populated from `eventsByAttempt[currentAttemptId]`. Requirements: R4.2, R5.2, R5.3
  - [x] 7.3 Add `web/assets/modules/apps.js` with the store `Dockpal.apps` per the design: `loadApps`, `loadAppHistory`, `toggleAutoUpdate`, `triggerUpdate`, `startFeed`, `handleFeedEvent`, computed `appsUpdating`. Requirements: R4.1, R4.3, R4.4, R4.5, R6.1
  - [x] 7.4 Modify `web/assets/modules/state.js` (or equivalent) to initialize `Dockpal.apps` at app start and call `startFeed()` after login. Requirements: R4.4
  - [x] 7.5 Update `web/partials/sidebar.html` with a navigation item `Apps` and a dual-count badge `appsUpdating + appsWithUpdates`. Requirements: R4.5
  - [x] 7.6 Add toast handling in `handleFeedEvent` for `rolled_back` (warning), `failed` (error), `completed` (light success). Requirements: R4.4
  - [x] 7.7 Update `web/embed.go` if the embedded file list needs `apps.html` and `apps.js`. Requirements: deployment

- [x] 8. Configuration and audit
  - [x] 8.1 Add env-var reads in the `internal/docker/auto_updater.go` constructor: `DOCKPAL_AUTO_UPDATE_ENABLED`, `DOCKPAL_AUTO_UPDATE_COOLDOWN_MINUTES`, `DOCKPAL_AUTO_UPDATE_CONCURRENCY` (max 8), `DOCKPAL_AUTO_UPDATE_HEALTH_GRACE_SECONDS`. Out-of-range values fall back to defaults and log a single warning. Requirements: R7.1-R7.5
  - [x] 8.2 Add `/api/config` (or extend the existing one) to return `{auto_update_enabled: bool}`. The UI reads it on boot and hides the toggle when disabled. Requirements: R7.1
  - [x] 8.3 Audit logging: on every `TriggerApp` call with `triggeredBy=user:<username>`, call `database.SaveAuditLog` with action `app_update_attempted`, details JSON `{app, image, result}`. Requirements: R8.4

- [x] 9. Observability
  - [x] 9.1 Add Prometheus metrics in `internal/metrics/` (or wherever the existing metrics live):
    - counter `dockpal_auto_update_attempts_total{instance,app,result}`
    - histogram `dockpal_auto_update_duration_seconds{instance,app,stage}`
    - gauge `dockpal_auto_update_apps_with_pending_update`
    The worker increments them on stage transitions. Requirements: R10.1, R10.2, R10.3
  - [x] 9.2 Structured logging in `AutoUpdateWorker`: use the standard `log` package with a key=value prefix `[auto-update]` covering `attempt_id`, `app`, `instance_id`, `service`, `image`, `old_digest`, `new_digest`, `stage`, `error_code`. Make sure no credential or registry auth header is logged. Requirements: R10.4, R10.5
  - [x] 9.3 Add a logging unit test: capture the log writer, run a scripted update, assert the required fields are present and that no substring matches "auth", "token", "password". Requirements: R10.4, R10.5

- [x] 10. Webhook notifications (best effort)
  - [x] 10.1 On `rolled_back` or on `failed` after a rollback failure, call all configured webhooks (via `database.ListWebhooks()`) with the JSON body `{type:"app_update", app, instance_id, stage, error_code, message, attempt_id}`. Best-effort; webhook errors do not fail the main operation. Requirements: R3.5

- [x] 11. Integration tests and smoke verification
  - [x] 11.1 Add `internal/docker/auto_updater_integration_test.go` (build tag `integration`):
    - deploy a compose nginx with the label `auto-update=true`
    - simulate `has_update` (mock cache)
    - call `worker.TriggerApp` manually
    - assert the container is recreated with the new image
    Requirements: R2.3, R2.6. Depends on: 3.6
  - [x] 11.2 (Optional) Add a Playwright test `e2e/apps.spec.ts` if Playwright infra exists:
    - login, navigate to `/apps`, toggle auto-update
    - click `Update now`, assert the status badge changes
    - assert the SSE event updates the UI
    Requirements: R4. Depends on: 7.x

- [x] 12. Manual verification (human task)
  - [x] 12.1 (manual) Deploy 2 apps (one with the auto-update label, one without). Push a new image to the registry that they use. Wait for `DefaultCheckInterval` or set the env interval low. Verify:
    - the labeled app updates automatically and the UI badge transitions `Updating` → `Up to date`
    - the unlabeled app stays at `Update available` (amber) without being touched
    - the History tab shows the attempt
    - rollback test: replace the image with one that exits non-zero, observe rollback to the previous digest
    Requirements: R1.1, R1.2, R2.6, R3.3, R4.1, R4.4, R5.2

## Task Dependency Graph

```json
{
  "waves": [
    {
      "id": "wave-1",
      "name": "Foundations",
      "tasks": ["1", "2"],
      "description": "Data model, store, compose label helper, health probe"
    },
    {
      "id": "wave-2",
      "name": "Worker Core",
      "tasks": ["3"],
      "description": "AutoUpdateWorker logic, errors, listener wiring"
    },
    {
      "id": "wave-3",
      "name": "Transport",
      "tasks": ["4", "5", "6"],
      "description": "Feed, HTTP endpoints, AgentClient extension"
    },
    {
      "id": "wave-4",
      "name": "Frontend",
      "tasks": ["7"],
      "description": "Apps page and real-time feed UI"
    },
    {
      "id": "wave-5",
      "name": "Cross-cutting",
      "tasks": ["8", "9", "10"],
      "description": "Config, observability, webhook notifications"
    },
    {
      "id": "wave-6",
      "name": "Verification",
      "tasks": ["11", "12"],
      "description": "Integration tests and manual smoke"
    }
  ]
}
```

## Notes

- All PBT tests use `pgregory.net/rapid` (already in go.mod via the update-mechanism spec).
- `previous_image` digest is taken from `RepoDigests[0]` after splitting on `@`. If empty (a local image with no pull history), rollback skips with code `rollback_no_previous_digest` and the attempt is marked `failed`.
- Compose YAML rewriting for rollback uses a new helper `RewriteImageDigest(yaml, service, digest)` that turns `image: repo:tag` into `image: repo@<digest>` for one service. This helper is added in task 3.7 as a sub-helper.
- `web/pages/apps.html` is a new page. If the team prefers to merge it into the existing `containers.html`, tasks 7.1 and 7.2 are folded into modifications of `containers.html`. Default recommendation is a separate page so the containers page is not overloaded.
- Task 12 is a human-only task.
