# Requirements Document

## Introduction

Dockpal already ships with `ImageUpdateMonitor`, which polls the registry every 30 minutes and tells the user when a local image has a new digest, plus an `/images/pull-force` endpoint for manual pulls. What is missing: a fresh pull does not recreate the container that is already running, so the live app keeps using the old image. Visibility is also weak today, just a small amber badge on the Images page and a toast on manual pull.

This feature adds **opt-in per-app auto-update driven by a container label**, with a UI that surfaces update state for each app (compose project), an attempt history, an enable/disable toggle, and real-time progress. The goal is that an image a developer pushes to a registry can flow all the way to the running container without operator intervention, while operators who do not want auto-update still get a clear summary of available updates.

Scope covers the Go backend (`internal/docker/`, `internal/server/`, `internal/db/`) and the HTML+JS frontend (`web/pages/`, `web/assets/modules/`). Multi-instance (remote agent) is included because the `AgentClient` interface already exposes `ForcePullImage` and `DeployCompose`.

## Glossary

- **Auto_Update**: The mechanism that periodically checks the registry for images used by running containers and pulls plus recreates those containers when a new digest is published, but only for apps that have opted in.
- **App**: A compose project deployed via Dockpal, identified by the `dockpal.project=<name>` label on its containers and by a `db.Service` record.
- **Image_Update_Monitor**: The existing background goroutine in `internal/docker/image_updater.go` that maintains a cache of `imageRef → ImageUpdateStatus`.
- **Auto_Update_Worker**: The new component that runs after each Image_Update_Monitor cycle and decides whether an app should be redeployed.
- **Pull_Plan**: The list of apps that have `has_update=true` and the opt-in label `dockpal.auto-update=true`, grouped so that all images in one project are pulled together and the recreate happens in a single compose operation.
- **Update_Window**: An optional time range during which auto-update is allowed for an app, declared via the label `dockpal.auto-update.window=<spec>`. An empty value means "any time".
- **Cooldown**: The minimum time between two auto-update attempts for the same app. Default is 1 hour.
- **App_Update_Record**: A persistent record of one update attempt for an app, holding timestamp, image, old digest, new digest, stage, and error code.
- **Update_Status_Stage**: One of `pending`, `pulling`, `recreating`, `verifying`, `completed`, `failed`, `rolled_back`.
- **Auto_Update_Setting**: The per-app boolean that decides whether the app participates in Auto_Update. The source of truth is the container label `dockpal.auto-update=true`. The UI shows and edits this label by rewriting the compose YAML and redeploying.
- **App_Update_Feed**: The SSE channel at `/api/apps/updates/stream` that broadcasts Update_Status_Stage events for every app, so the UI can update in real time without polling.
- **Rollback**: The process of restoring a container to the previous image when the new container fails to start or fails the health probe after recreate.
- **Health_Probe**: The post-recreate check that waits for every container in the project to reach state `running` with exit code 0 for at least `HealthGraceSeconds`.
- **HealthGraceSeconds**: Default 60 seconds, can be overridden per app via the label `dockpal.auto-update.grace=<seconds>`.
- **DefaultCheckInterval**: 30 minutes, matching the existing `Image_Update_Monitor` (env `DOCKPAL_IMAGE_CHECK_INTERVAL`).
- **MaxConcurrentUpdates**: 2, the cap on the number of apps updated in parallel so a small host is not overloaded.

## Requirements

### Requirement 1: Per-app opt-in via label

**User Story:** As an operator, I want to enable auto-update only on apps that I trust to update unattended, so that critical apps stay on a pinned digest until I review the change.

#### Acceptance Criteria

1. WHEN Auto_Update_Worker evaluates an app, THE Auto_Update_Worker SHALL only schedule an update if at least one of the app's containers has the label `dockpal.auto-update=true`.
2. WHEN none of the app's containers has the label `dockpal.auto-update=true`, THE Auto_Update_Worker SHALL skip the app without writing an App_Update_Record.
3. IF the compose YAML declares `dockpal.auto-update=true` at service-level labels, THEN on redeploy the resulting container SHALL carry that label.
4. WHEN the user toggles auto-update from the UI, THE backend SHALL rewrite the compose YAML with the appropriate `dockpal.auto-update` label and call `DeployCompose` again with `forcePull=false`, so the container is recreated with the new label without pulling a new image.

### Requirement 2: Periodic pull and recreate

**User Story:** As an operator, I want apps with auto-update enabled to be redeployed in the background when a new image is published, so that I don't have to manually click pull and redeploy.

#### Acceptance Criteria

1. WHEN Image_Update_Monitor finishes a `checkAll()` cycle, THE Auto_Update_Worker SHALL read the cache via `GetAllStatuses()` and build a Pull_Plan.
2. WHEN the Pull_Plan is non-empty, THE Auto_Update_Worker SHALL process apps in parallel up to MaxConcurrentUpdates.
3. WHEN an app contains multiple services and several of them have `has_update=true`, THE Auto_Update_Worker SHALL pull all changed images and then call `DeployCompose` once with `forcePull=true` to recreate the entire project atomically.
4. IF the app cooldown has not yet elapsed (`last_attempt_at + Cooldown > now`), THEN THE Auto_Update_Worker SHALL skip the app and emit an App_Update_Feed event `skipped_cooldown` without writing a new App_Update_Record.
5. IF Update_Window is non-empty and the current time is outside the window, THEN THE Auto_Update_Worker SHALL skip the app and emit an App_Update_Feed event `skipped_window`.
6. WHEN Auto_Update_Worker starts an update for an app, THE Auto_Update_Worker SHALL write an App_Update_Record at stage `pulling` and then transition through `recreating`, `verifying`, and finally `completed` or `failed`.

### Requirement 3: Health verification and rollback

**User Story:** As an operator, I want a failed container after auto-update to roll back to the previous image, so that a bad release does not silently take down my app.

#### Acceptance Criteria

1. BEFORE calling `DeployCompose` with `forcePull=true`, THE Auto_Update_Worker SHALL capture the previous image ID and tag-by-digest as `previous_image` per service.
2. AFTER `DeployCompose` returns without error, THE Auto_Update_Worker SHALL run Health_Probe for HealthGraceSeconds against every container in the project.
3. IF Health_Probe fails (any container has non-zero exit code or stays in state `dead`/`exited` during the probe), THEN THE Auto_Update_Worker SHALL perform Rollback by rewriting the compose YAML to pin `image: repo@<previous_digest>` and then call `DeployCompose(... forcePull=false)`.
4. WHEN Rollback finishes, THE Auto_Update_Worker SHALL update the App_Update_Record to stage `rolled_back` with `error_code=health_probe_failed`.
5. IF Rollback itself fails, THEN THE Auto_Update_Worker SHALL update the App_Update_Record to stage `failed` with `error_code=rollback_failed`, emit an error-level log, and trigger webhook notifications when configured.

### Requirement 4: UI visibility on the Apps page

**User Story:** As an operator, I want a single dashboard that shows update availability, last attempt, and current state for each app, so that I never have to guess whether my apps are up to date.

#### Acceptance Criteria

1. WHEN the user opens the Apps page, THE UI SHALL show an `Update` column for each app with one of these visual states: `Up to date` (green), `Update available` (amber), `Updating…` (animated blue), `Failed` (red), `Rolled back` (orange).
2. WHEN the user clicks an app row, THE UI SHALL open a detail panel that shows: image refs, current digest, latest available digest, last attempt timestamp, last status, and an `Update now` button (manual trigger).
3. WHEN the user toggles the `Auto-update` switch in the detail panel, THE UI SHALL call `PATCH /apps/:name/auto-update` with body `{enabled: bool}` and, on a successful response, optimistically update the app's label in the table.
4. WHEN App_Update_Feed emits an event for an app, THE UI SHALL update that app's visual state without a page reload, and append a row to the timeline in the detail panel if it is open.
5. WHEN a Pull_Plan is in flight, THE sidebar SHALL show a numeric badge on the Apps nav item with the count of apps currently in `pulling` or `recreating`.

### Requirement 5: History and audit

**User Story:** As an operator, I want to see what auto-update did to my apps over the last weeks, so that I can correlate downtime with image releases.

#### Acceptance Criteria

1. WHEN an update attempt finishes (success, failed, or rolled_back), THE backend SHALL persist an App_Update_Record to the `app_updates` bucket.
2. WHEN the user opens the `History` tab in the app detail panel, THE UI SHALL show the most recent App_Update_Records, up to 50 entries, with columns timestamp, service, old digest (8 chars), new digest (8 chars), final stage, and error code if any.
3. WHEN the user clicks a history entry, THE UI SHALL open a modal with the full event log (every stage transition with timestamps) for that attempt.
4. WHEN App_Update_Records are pruned (for retention), THE backend SHALL keep at least the 100 most recent entries per app and a global maximum of 1000 most recent entries.

### Requirement 6: Manual trigger and override

**User Story:** As an operator, I want to force an update outside the auto-update schedule, so that I can apply a known-good release immediately.

#### Acceptance Criteria

1. WHEN the user clicks `Update now`, THE backend SHALL run the same pipeline as auto-update with `bypass_cooldown=true` and `bypass_window=true`.
2. WHEN a manual trigger is in flight for an app, THE backend SHALL reject any further trigger for that same app with HTTP 409 and body `{error: "update_already_running"}`.
3. WHEN a manual trigger finishes (success or failure), THE backend SHALL emit App_Update_Feed events as in auto-update and SHALL persist an App_Update_Record with `triggered_by=user:<username>`.

### Requirement 7: Global configuration

**User Story:** As an operator, I want sane defaults but the ability to tune intervals and limits, so that I can adapt the mechanism to small VPSes or large fleets.

#### Acceptance Criteria

1. THE Auto_Update_Worker SHALL read the env var `DOCKPAL_AUTO_UPDATE_ENABLED` (default `true`); when `false` the feature is fully disabled and the UI shows an informational banner.
2. THE Auto_Update_Worker SHALL read the env var `DOCKPAL_AUTO_UPDATE_COOLDOWN_MINUTES` (default `60`).
3. THE Auto_Update_Worker SHALL read the env var `DOCKPAL_AUTO_UPDATE_CONCURRENCY` (default `2`, max `8`).
4. THE Auto_Update_Worker SHALL read the env var `DOCKPAL_AUTO_UPDATE_HEALTH_GRACE_SECONDS` (default `60`).
5. WHEN any env var is outside the valid range, THE Auto_Update_Worker SHALL fall back to the default and log a single warning at startup.

### Requirement 8: Security and authorization

**User Story:** As an admin, I want only operators to trigger updates and toggle auto-update, so that read-only viewers cannot disrupt production apps.

#### Acceptance Criteria

1. THE endpoint `POST /apps/:name/update` SHALL require role `operator` or higher (`RequireRole(auth.RoleOperator)`).
2. THE endpoint `PATCH /apps/:name/auto-update` SHALL require role `operator` or higher.
3. THE endpoints `GET /apps`, `GET /apps/:name/updates`, `GET /apps/updates/stream` SHALL require role `viewer` or higher only.
4. WHEN audit logging is enabled, THE backend SHALL write an `app_update_attempted` entry containing actor, app, image, and result.
5. WHEN an app uses a private registry, THE Auto_Update_Worker SHALL fetch credentials via `registryManager.GetAuthHeader(image)`, the same path as a normal deploy, and SHALL NOT store credentials in the App_Update_Record.

### Requirement 9: Multi-instance support

**User Story:** As an operator with several remote hosts, I want auto-update to work per-instance with the same UI, so that I don't manage two separate workflows.

#### Acceptance Criteria

1. THE Auto_Update_Worker SHALL run inside the edge process for the `local` instance and inside each remote agent for other instances.
2. THE App_Update_Record SHALL store `instance_id` so the UI can filter per instance.
3. WHEN the UI scope is a single instance, THE endpoint `/apps` SHALL accept the query `?instance_id=<id>` and only return apps on that instance.
4. WHEN the UI consumes App_Update_Feed, THE event SHALL include `instance_id` so the UI maps it to the correct row.

### Requirement 10: Observability

**User Story:** As an operator, I want metrics and logs that I can graph and alert on, so that auto-update failures are visible in my monitoring stack.

#### Acceptance Criteria

1. THE Auto_Update_Worker SHALL expose the Prometheus counter `dockpal_auto_update_attempts_total{instance,app,result}`.
2. THE Auto_Update_Worker SHALL expose the histogram `dockpal_auto_update_duration_seconds{instance,app,stage}`.
3. THE Auto_Update_Worker SHALL expose the gauge `dockpal_auto_update_apps_with_pending_update`.
4. THE Auto_Update_Worker SHALL log the structured fields `attempt_id`, `app`, `instance_id`, `service`, `image`, `old_digest`, `new_digest`, `stage`, `error_code` for every transition.
5. THE log output SHALL NEVER contain registry credentials or image content.
