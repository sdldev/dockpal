// Package docker exposes the auto-updater error code identifiers.
//
// The constants in this file are the persisted `error_code` values written
// to App_Update_Records and emitted on the App_Update_Feed by the
// Auto_Update_Worker state machine (see design.md "Error Handling"). They are
// stable, lowercase, snake_case identifiers — not Go `error` values — so the
// frontend, audit logs, Prometheus labels, and webhook payloads can all match
// them as plain strings.
//
// When adding a new code, also append it to KnownAutoUpdaterErrorCodes so
// observability and audit code that enumerates the full set stays in sync.
package docker

// Auto-updater error codes persisted on App_Update_Records and emitted on the
// App_Update_Feed. See requirements R3.4 and R6.2.
const (
	// ErrPullError indicates `ForcePullImage` failed for a non-auth reason
	// (network failure, registry 5xx, manifest mismatch, etc.).
	ErrPullError = "pull_error"

	// ErrAuthMissing indicates the registry rejected the pull with an
	// authentication or authorization error and no usable credentials were
	// available.
	ErrAuthMissing = "auth_missing"

	// ErrComposeError indicates `DeployCompose` failed while recreating the
	// project (compose parse error, container create/start failure, etc.).
	ErrComposeError = "compose_error"

	// ErrHealthProbeFailed indicates the post-deploy Health_Probe reported one
	// or more containers unhealthy within the grace window, triggering a
	// rollback.
	ErrHealthProbeFailed = "health_probe_failed"

	// ErrRollbackFailed indicates the rollback compose redeploy itself failed
	// after a prior failure, leaving the app in a degraded state.
	ErrRollbackFailed = "rollback_failed"

	// ErrUpdateAlreadyRunning indicates a trigger was rejected because another
	// update for the same app is already in flight (per-app mutex held).
	ErrUpdateAlreadyRunning = "update_already_running"

	// ErrSkippedCooldown indicates the worker skipped an app because the
	// cooldown window since the last attempt has not yet elapsed.
	ErrSkippedCooldown = "skipped_cooldown"

	// ErrSkippedWindow indicates the worker skipped an app because the current
	// time is outside the configured Update_Window.
	ErrSkippedWindow = "skipped_window"
)

// KnownAutoUpdaterErrorCodes lists every error code emitted by the
// Auto_Update_Worker. It is intended for audit, metrics enumeration, and
// validation helpers that need the full set in one place.
var KnownAutoUpdaterErrorCodes = []string{
	ErrPullError,
	ErrAuthMissing,
	ErrComposeError,
	ErrHealthProbeFailed,
	ErrRollbackFailed,
	ErrUpdateAlreadyRunning,
	ErrSkippedCooldown,
	ErrSkippedWindow,
}
