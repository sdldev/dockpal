// Package docker exposes the AutoUpdateWorker that consumes
// ImageUpdateMonitor cycle events and drives the per-app
// pull → recreate → verify → rollback pipeline described in the
// auto-image-update design.
//
// This file contains the worker shell, the constructor, env-var configuration,
// the cycle listener wiring, and the full TriggerApp pipeline (pull →
// recreate → verify → rollback). The rollback helper rewrites the compose
// YAML to pin each service to its previously-running RepoDigest and
// redeploys without a force pull (phase 1 strategy from design.md).
package docker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// Default values for the AutoUpdateWorker configuration. They match the
// requirements R7.2-R7.4.
const (
	defaultAutoUpdateEnabled     = true
	defaultAutoUpdateCooldown    = 60 * time.Minute
	defaultAutoUpdateConcurrency = 2
	defaultAutoUpdateGrace       = 60 * time.Second

	// minAutoUpdateCooldownMinutes is the smallest cooldown accepted from the
	// env var. Anything lower would risk hammering the registry.
	minAutoUpdateCooldownMinutes = 1

	// minAutoUpdateConcurrency / maxAutoUpdateConcurrency clamp the parallel
	// update fan-out (per requirement R7.3).
	minAutoUpdateConcurrency = 1
	maxAutoUpdateConcurrency = 8

	// minAutoUpdateGraceSeconds prevents the health probe window from being
	// shorter than a single 2-second poll cycle plus margin.
	minAutoUpdateGraceSeconds = 5
)

// Env var names for AutoUpdateWorker configuration (requirements R7.1-R7.4).
const (
	envAutoUpdateEnabled         = "DOCKPAL_AUTO_UPDATE_ENABLED"
	envAutoUpdateCooldownMinutes = "DOCKPAL_AUTO_UPDATE_COOLDOWN_MINUTES"
	envAutoUpdateConcurrency     = "DOCKPAL_AUTO_UPDATE_CONCURRENCY"
	envAutoUpdateGraceSeconds    = "DOCKPAL_AUTO_UPDATE_HEALTH_GRACE_SECONDS"
)

// AppUpdateFeedPayload is the worker-local view of an App_Update_Feed event.
//
// The App_Update_Feed broadcaster lives in package server (added by task 4.1
// as `server.AppUpdateFeed` / `server.AppUpdateFeedEvent`). To avoid a
// server → docker import cycle, the worker publishes through this local
// payload struct via an AppUpdateFeedFunc. Wire-up code (task 5.2) is
// expected to provide a small adapter that translates this payload into the
// server-side event type before calling `feed.Publish`. Field names match
// the server event verbatim so the adapter is a one-to-one copy.
type AppUpdateFeedPayload struct {
	AttemptID  string `json:"attempt_id"`
	InstanceID string `json:"instance_id"`
	App        string `json:"app"`
	Stage      string `json:"stage"`
	ErrorCode  string `json:"error_code,omitempty"`
	Message    string `json:"message,omitempty"`
	At         int64  `json:"at"`
}

// AppUpdateFeedFunc is the publish callback the worker uses to broadcast
// stage transitions. Implementations must be non-blocking; a nil func is
// treated as a no-op so tests can omit the feed dependency entirely.
type AppUpdateFeedFunc func(event AppUpdateFeedPayload)

// WebhookListFunc returns the list of notification webhook URLs configured
// in the database. The worker calls this on rolled_back or failed (after
// rollback failure) to send best-effort notifications. A nil func is
// treated as "no webhooks configured".
type WebhookListFunc func() ([]db.NotificationWebhook, error)

// AutoUpdateMetricsHooks bundles the optional metric callbacks the worker
// invokes on stage transitions and cycle completion. The hooks let the
// server wire Prometheus collectors (internal/metrics) into the docker
// package without creating an internal/metrics → internal/agent →
// internal/docker import cycle.
//
// Every hook is optional; a nil hook is a no-op so unit tests and the
// auto_updater_test.go fakes can skip wiring entirely. Hooks must be
// non-blocking: they run on the worker goroutine that drives the pipeline,
// so an expensive hook would stall the per-app pipeline.
type AutoUpdateMetricsHooks struct {
	// Attempt is called once per terminal pipeline outcome with the result
	// label set to one of: "success", "failed", "rolled_back",
	// "skipped_cooldown", "skipped_window", "update_already_running".
	Attempt func(instance, app, result string)

	// Duration is called once per stage transition with the wall-clock
	// time spent in that stage. The stage value matches the persisted
	// AppUpdateRecord stage strings ("pulling", "recreating", "verifying").
	Duration func(instance, app, stage string, seconds float64)

	// PendingUpdate is called from the cycle listener with the count of
	// opt-in apps that have at least one image waiting to be auto-updated.
	// Called on every monitor cycle, including cycles where the count is 0.
	PendingUpdate func(n int)
}

// Result label values for the AutoUpdateMetricsHooks.Attempt counter. They
// match the auto-image-update spec (task 9.1).
const (
	resultSuccess              = "success"
	resultFailed               = "failed"
	resultRolledBack           = "rolled_back"
	resultSkippedCooldown      = "skipped_cooldown"
	resultSkippedWindow        = "skipped_window"
	resultUpdateAlreadyRunning = "update_already_running"
)

// autoUpdateLogFields collects the canonical structured-log fields for an
// AutoUpdateWorker log line. Field semantics match requirement R10.4: every
// line carries the optional pieces of context that apply to the transition,
// rendered as key=value pairs with the [auto-update] prefix. Empty values
// are omitted so the line stays compact.
//
// Free-form prose (e.g. error text) goes into Message; never mix it into
// AttemptID / App / Service to keep log parsers happy. Values that match
// forbiddenSubstrings are replaced with redactedMarker before rendering, so
// callers can pass a raw error string without worrying about whether the
// underlying client accidentally surfaced a registry auth header.
type autoUpdateLogFields struct {
	AttemptID  string
	App        string
	InstanceID string
	Service    string
	Image      string
	OldDigest  string
	NewDigest  string
	Stage      string
	ErrorCode  string
	Message    string
}

// forbiddenSubstrings names credential / registry-auth markers that must
// never appear in [auto-update] log output (R10.5). logAutoUpdate scans
// every value before rendering and substitutes the redacted marker if a
// case-insensitive match is found.
//
// The slice is exported lowercase (package-private) so tests in the same
// package can iterate it to assert that captured log output never contains
// any of these patterns. The list intentionally targets credential-bearing
// shapes (HTTP header names, struct-field-style log of registry auth, the
// HTTP "Bearer " prefix) rather than natural-language words like
// "unauthorized", which legitimately appear in registry error messages.
var forbiddenSubstrings = []string{
	"x-registry-auth",
	"registryauth=",
	"registry_auth=",
	"authheader=",
	"auth_header=",
	"password=",
	"passwd=",
	"Bearer ",
}

// redactedMarker is the placeholder logAutoUpdate substitutes for any value
// that contains a forbidden substring. The square brackets make the
// redaction visually distinct in log output and keep the key=value layout
// parser-friendly without quoting.
const redactedMarker = "[REDACTED]"

// logAutoUpdateFieldOrder is the canonical column order for [auto-update]
// log lines, matching requirement R10.4. logAutoUpdate iterates this list
// so every log consumer can rely on a stable column layout regardless of
// which fields a particular call site populates.
var logAutoUpdateFieldOrder = []string{
	"attempt_id",
	"app",
	"instance_id",
	"service",
	"image",
	"old_digest",
	"new_digest",
	"stage",
	"error_code",
	"msg",
}

// logAutoUpdate emits a single [auto-update] line with the canonical
// key=value fields. Empty fields are skipped, values containing whitespace
// / quotes / equals are quoted via strconv.Quote, and any value matching a
// forbiddenSubstring is replaced with redactedMarker (R10.5).
//
// The function is goroutine-safe (log.Print serializes internally) and is
// invoked on the worker's hot path; it allocates a single strings.Builder
// per call.
func logAutoUpdate(fields autoUpdateLogFields) {
	values := map[string]string{
		"attempt_id":  fields.AttemptID,
		"app":         fields.App,
		"instance_id": fields.InstanceID,
		"service":     fields.Service,
		"image":       fields.Image,
		"old_digest":  fields.OldDigest,
		"new_digest":  fields.NewDigest,
		"stage":       fields.Stage,
		"error_code":  fields.ErrorCode,
		"msg":         fields.Message,
	}

	var b strings.Builder
	b.WriteString("[auto-update]")
	for _, key := range logAutoUpdateFieldOrder {
		v := values[key]
		if v == "" {
			continue
		}
		if containsForbiddenSubstring(v) {
			v = redactedMarker
		}
		b.WriteByte(' ')
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(quoteLogValue(v))
	}
	log.Print(b.String())
}

// containsForbiddenSubstring reports whether value contains any of the
// case-insensitive markers in forbiddenSubstrings. The lowercase folding is
// done once per call.
func containsForbiddenSubstring(value string) bool {
	lower := strings.ToLower(value)
	for _, s := range forbiddenSubstrings {
		if strings.Contains(lower, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

// quoteLogValue returns value verbatim when it has no characters that would
// confuse a logfmt-style parser; otherwise it returns strconv.Quote(value)
// so spaces, equals, quotes, and control characters cannot break the
// key=value layout. Empty strings are quoted as `""` so absent fields are
// distinguishable from the literal empty string.
func quoteLogValue(value string) string {
	if needsLogQuoting(value) {
		return strconv.Quote(value)
	}
	return value
}

// needsLogQuoting reports whether a string value must be quoted to remain
// unambiguous in a logfmt-style line. Empty strings are quoted so absent
// values cannot be confused with the literal empty string.
func needsLogQuoting(v string) bool {
	if v == "" {
		return true
	}
	for _, r := range v {
		switch r {
		case ' ', '\t', '\n', '\r', '"', '=', '\\':
			return true
		}
	}
	return false
}

// autoUpdaterClient is the narrow Docker surface the AutoUpdateWorker needs.
// It is unexported so the public API (NewAutoUpdateWorker, the worker struct)
// continues to take *Client, while unit tests in this package can substitute
// a fake by constructing the worker as a struct literal. *Client satisfies
// this interface naturally; see (*Client).InspectRepoDigest for the only
// method added explicitly to back the rollback path.
type autoUpdaterClient interface {
	ListContainersWithLabel(ctx context.Context, label string) ([]ContainerInfo, error)
	ForcePullImage(ctx context.Context, image string, registryAuth string) error
	DeployCompose(ctx context.Context, projectName, composeYAML string, getAuthHeader AuthHeaderFunc, forcePull bool) error
	HealthProbe(ctx context.Context, project string, grace time.Duration) ([]HealthProbeResult, error)
	InspectRepoDigest(ctx context.Context, image string) (string, error)
}

// parseWindowFn is the package-level indirection through which TriggerApp
// resolves the Update_Window predicate. Tests override this to drive the
// "outside window" path deterministically without depending on the host's
// wall-clock minute.
var parseWindowFn = parseWindow

// AutoUpdateWorker reacts to ImageUpdateMonitor cycles, builds a per-app
// pull plan, executes pull-recreate-verify with rollback on failure,
// persists records, and broadcasts events. Fields match design.md
// "Components and Interfaces".
type AutoUpdateWorker struct {
	client     autoUpdaterClient
	monitor    *ImageUpdateMonitor
	store      db.AppUpdateStore
	feed       AppUpdateFeedFunc    // may be nil (no-op)
	listWebhooks WebhookListFunc    // may be nil (no webhooks)
	getAuth    AuthHeaderFunc
	getCompose func(project string) (string, error)

	cooldown    time.Duration
	grace       time.Duration
	concurrency int
	enabled     bool

	// perAppMu maps appName → *sync.Mutex. Lazily populated by the pipeline
	// to enforce single-flight per app (Property 8 in design.md).
	perAppMu sync.Map

	// sem bounds parallel update fan-out to `concurrency`.
	sem chan struct{}

	// instanceID is the ID of the agent / edge process running the worker.
	// "" for the local instance.
	instanceID string

	// metrics holds optional Prometheus instrumentation hooks. A zero-value
	// AutoUpdateMetricsHooks (all funcs nil) is a no-op so the worker stays
	// usable without metrics wiring (e.g. in unit tests).
	metrics AutoUpdateMetricsHooks

	// ctx is derived from the context passed to Start and used by onCycle and
	// TriggerApp. cancel releases all in-flight goroutines on Stop. Both are
	// nil until Start runs (or when the worker is disabled).
	ctx    context.Context
	cancel context.CancelFunc
}

// NewAutoUpdateWorker wires the worker with backends. It reads env-var
// configuration per requirements R7.1-R7.5, falling back to defaults and
// logging a single warning for any out-of-range value.
//
// The worker does not start until Start() is called (task 3.4).
func NewAutoUpdateWorker(
	client *Client,
	monitor *ImageUpdateMonitor,
	store db.AppUpdateStore,
	feed AppUpdateFeedFunc,
	getAuth AuthHeaderFunc,
	getCompose func(string) (string, error),
	instanceID string,
) *AutoUpdateWorker {
	enabled := readAutoUpdateEnabled()
	cooldown := readAutoUpdateCooldown()
	concurrency := readAutoUpdateConcurrency()
	grace := readAutoUpdateGrace()

	return &AutoUpdateWorker{
		client:      client,
		monitor:     monitor,
		store:       store,
		feed:        feed,
		getAuth:     getAuth,
		getCompose:  getCompose,
		cooldown:    cooldown,
		grace:       grace,
		concurrency: concurrency,
		enabled:     enabled,
		sem:         make(chan struct{}, concurrency),
		instanceID:  instanceID,
	}
}

// Start subscribes to monitor cycle events and begins processing pull plans.
//
// When the worker is disabled via DOCKPAL_AUTO_UPDATE_ENABLED=false, Start
// logs a single warning and returns without registering the cycle listener,
// so the ImageUpdateMonitor continues to populate its cache for the UI but
// no auto-recreate is ever triggered (requirement R7.1).
//
// Otherwise Start derives a cancellable context from ctx (used by onCycle
// and TriggerApp) and registers w.onCycle with monitor.AddCycleListener.
// Listeners are invoked sequentially by ImageUpdateMonitor.checkAll(), so
// onCycle dispatches per-app pipeline work to its own goroutines, bounded
// by w.sem.
func (w *AutoUpdateWorker) Start(ctx context.Context) {
	if !w.enabled {
		logAutoUpdate(autoUpdateLogFields{
			InstanceID: w.instanceID,
			Message:    fmt.Sprintf("disabled via %s=false", envAutoUpdateEnabled),
		})
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.monitor.AddCycleListener(w.onCycle)
}

// Stop releases worker resources gracefully. After Stop returns, in-flight
// goroutines spawned by onCycle observe ctx.Done() and exit; the listener
// itself remains registered on the monitor (the monitor lacks a remove
// hook), but onCycle is a no-op once the context is cancelled.
func (w *AutoUpdateWorker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
}

// Enabled reports whether the auto-update worker is enabled, as resolved
// from DOCKPAL_AUTO_UPDATE_ENABLED at construction time (R7.1). The UI
// reads this through the public /api/config endpoint to decide whether to
// render the per-app auto-update toggle and the "Update now" / history
// affordances. nil-safe so callers do not need to guard against a nil
// worker (which happens when RegisterRoutes hasn't run, e.g. in tests).
func (w *AutoUpdateWorker) Enabled() bool {
	if w == nil {
		return false
	}
	return w.enabled
}

// SetMetricsHooks installs optional Prometheus instrumentation hooks. Each
// hook is independently optional; passing a zero-value AutoUpdateMetricsHooks
// disables metrics entirely. The setter is intentionally non-thread-safe and
// is meant to be called once during boot (after NewAutoUpdateWorker, before
// Start), mirroring how the feed callback is wired through the constructor.
//
// Tests that exercise metric instrumentation typically construct the worker
// as a struct literal and assign the field directly; SetMetricsHooks exists
// so production wiring (server/routes.go) can keep the existing constructor
// signature unchanged.
func (w *AutoUpdateWorker) SetMetricsHooks(hooks AutoUpdateMetricsHooks) {
	if w == nil {
		return
	}
	w.metrics = hooks
}

// SetWebhookLister installs the callback used to retrieve notification
// webhook URLs. Called once during boot (after NewAutoUpdateWorker, before
// Start). When set, the worker sends best-effort POST notifications to all
// configured webhooks on rolled_back or failed (rollback_failed) outcomes.
func (w *AutoUpdateWorker) SetWebhookLister(fn WebhookListFunc) {
	if w == nil {
		return
	}
	w.listWebhooks = fn
}

// observeAttempt invokes the metrics.Attempt hook with the worker's instance
// ID. Safe to call when the hook is nil.
func (w *AutoUpdateWorker) observeAttempt(app, result string) {
	if w == nil || w.metrics.Attempt == nil {
		return
	}
	w.metrics.Attempt(w.instanceID, app, result)
}

// observeStageDuration invokes the metrics.Duration hook with the worker's
// instance ID. Safe to call when the hook is nil.
func (w *AutoUpdateWorker) observeStageDuration(app, stage string, seconds float64) {
	if w == nil || w.metrics.Duration == nil {
		return
	}
	w.metrics.Duration(w.instanceID, app, stage, seconds)
}

// observePendingUpdate invokes the metrics.PendingUpdate hook. Safe to call
// when the hook is nil.
func (w *AutoUpdateWorker) observePendingUpdate(n int) {
	if w == nil || w.metrics.PendingUpdate == nil {
		return
	}
	w.metrics.PendingUpdate(n)
}

// onCycle is the cycle listener registered with ImageUpdateMonitor. It
// builds the Pull_Plan from the current cache:
//
//  1. Filter `updates` to entries with HasUpdate=true (R2.1).
//  2. List opt-in containers via dockpal.auto-update=true (R1.1, R1.2).
//  3. Group containers by their dockpal.project label, keeping only those
//     whose Image matches one of the imageRefs that has an update.
//  4. Schedule one TriggerApp goroutine per project, bounded by w.sem.
//
// Image matching is intentionally lenient: we accept exact equality with
// the cache key (`repo:tag`) or a repo-prefix match against the cache key.
// Digest-pinned containers are tolerated through the repo-prefix path. The
// per-app pipeline (task 3.6) re-validates the actual services to update.
func (w *AutoUpdateWorker) onCycle(updates []ImageUpdateStatus) {
	if w.ctx == nil {
		return
	}
	if err := w.ctx.Err(); err != nil {
		return
	}

	// Build the set of imageRefs that have an update available.
	imageRefHasUpdate := make(map[string]bool, len(updates))
	for _, s := range updates {
		if s.Result != nil && s.Result.HasUpdate {
			imageRefHasUpdate[s.ImageRef] = true
		}
	}
	if len(imageRefHasUpdate) == 0 {
		// Nothing pending across the entire registry cache: publish a zero
		// gauge so dashboards reflect the live state instead of holding the
		// previous cycle's value.
		w.observePendingUpdate(0)
		return
	}

	containers, err := w.client.ListContainersWithLabel(w.ctx, "dockpal.auto-update=true")
	if err != nil {
		logAutoUpdate(autoUpdateLogFields{
			InstanceID: w.instanceID,
			Message:    fmt.Sprintf("failed to list opt-in containers: %v", err),
		})
		return
	}

	// Group projects by name. A project is included when at least one of its
	// opt-in containers references an image that has an update available.
	plan := make(map[string]struct{})
	for _, ctr := range containers {
		project := ctr.Labels["dockpal.project"]
		if project == "" {
			continue
		}
		if !imageMatchesUpdate(ctr.Image, imageRefHasUpdate) {
			continue
		}
		plan[project] = struct{}{}
	}

	// Publish the gauge regardless of plan size: the value should fall to 0
	// when nothing is pending so the metric reflects the live state.
	w.observePendingUpdate(len(plan))

	if len(plan) == 0 {
		return
	}

	for app := range plan {
		go w.scheduleAutoUpdate(app)
	}
}

// imageMatchesUpdate decides whether a container Image refers to one of the
// imageRefs known to have an update. The cache key is in the form
// `repo:tag`, while a container Image may be that exact string, the same
// repo without an explicit tag, or the same repo pinned by digest
// (`repo@sha256:...`). For phase 1 we accept three shapes:
//
//   - exact equality with a cache key
//   - same `repo` portion (strip everything from the first ':' or '@')
//
// The per-app pipeline re-validates which services actually need a pull,
// so a false positive here only costs us an extra trigger that exits early.
func imageMatchesUpdate(image string, imageRefHasUpdate map[string]bool) bool {
	if image == "" {
		return false
	}
	if imageRefHasUpdate[image] {
		return true
	}
	repo := splitImageRepo(image)
	if repo == "" {
		return false
	}
	for ref := range imageRefHasUpdate {
		if splitImageRepo(ref) == repo {
			return true
		}
	}
	return false
}

// splitImageRepo returns the repository portion of an image reference,
// stripping any ":tag" or "@digest" suffix. It is intentionally tolerant:
// a registry host that contains a port (e.g. "registry:5000/foo:bar")
// resolves to "registry:5000/foo".
func splitImageRepo(image string) string {
	if at := strings.IndexByte(image, '@'); at >= 0 {
		image = image[:at]
	}
	// Find the last ':' that is part of a tag (i.e. after the final '/').
	if slash := strings.LastIndexByte(image, '/'); slash >= 0 {
		if colon := strings.LastIndexByte(image[slash:], ':'); colon >= 0 {
			return image[:slash+colon]
		}
		return image
	}
	if colon := strings.LastIndexByte(image, ':'); colon >= 0 {
		return image[:colon]
	}
	return image
}

// scheduleAutoUpdate fires one TriggerApp call for `app`, bounded by the
// worker's concurrency semaphore so the host is not overloaded by parallel
// recreates (R2.2 / MaxConcurrentUpdates). The call respects the per-app
// cooldown and update window (those checks live inside TriggerApp itself,
// added by task 3.5).
func (w *AutoUpdateWorker) scheduleAutoUpdate(app string) {
	if w.ctx == nil {
		return
	}
	select {
	case w.sem <- struct{}{}:
	case <-w.ctx.Done():
		return
	}
	defer func() { <-w.sem }()

	if err := w.TriggerApp(w.ctx, app, false, false, "auto"); err != nil {
		logAutoUpdate(autoUpdateLogFields{
			App:        app,
			InstanceID: w.instanceID,
			Message:    fmt.Sprintf("trigger failed: %v", err),
		})
	}
}

// TriggerApp runs the pull → recreate → verify → rollback pipeline for one
// app:
//
//  1. Per-app single-flight via a sync.Map of *sync.Mutex. If another
//     trigger is already in flight for `app`, the call returns an error
//     wrapping ErrUpdateAlreadyRunning so the HTTP layer can map it to 409
//     (R6.2).
//  2. Cooldown check (skipped when bypassCooldown=true). Reads the most
//     recent App_Update_Record via store.ListAppUpdates and compares
//     StartedAt + cooldown to time.Now() in microseconds (R2.4).
//  3. Update_Window check (skipped when bypassWindow=true). Reads the
//     dockpal.auto-update.window label off any container in the project
//     and parses it via parseWindow (R2.5).
//  4. Resolve the compose YAML via getCompose, list project containers, and
//     capture the previously-running RepoDigest per service (R3.1).
//  5. Save the App_Update_Record at stage `pulling` and emit a feed event,
//     then call ForcePullImage for every service whose monitor cache
//     reports `has_update=true`. On a pull failure, classify the error as
//     `auth_missing` or `pull_error`, mark the record `failed`, and return
//     without touching prod (R3 "rollback preserves prod").
//  6. Save stage `recreating` and call DeployCompose with forcePull=true.
//     On compose failure, run rollback() to pin previous digests and
//     redeploy with forcePull=false; mark `rolled_back` (or `failed` /
//     `rollback_failed` if rollback itself errors).
//  7. Save stage `verifying` and run HealthProbe with the configured grace.
//     On unhealthy: rollback as in step 6, with code `health_probe_failed`.
//  8. Save stage `completed` and emit a final feed event.
//
// On a cooldown or window skip, the call publishes a feed event with the
// matching error code and returns nil — skipping is not a hard error
// (matches the design's "feed event only, no record" contract).
func (w *AutoUpdateWorker) TriggerApp(ctx context.Context, app string, bypassCooldown, bypassWindow bool, triggeredBy string) error {
	if app == "" {
		return errors.New("auto-update: empty app")
	}

	// (a) Per-app single-flight via sync.Map of *sync.Mutex.
	muAny, _ := w.perAppMu.LoadOrStore(app, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	if !mu.TryLock() {
		w.observeAttempt(app, resultUpdateAlreadyRunning)
		return fmt.Errorf("%s: app %q", ErrUpdateAlreadyRunning, app)
	}
	defer mu.Unlock()

	now := time.Now()
	nowMicros := now.UnixMicro()

	// (b) Cooldown guard (R2.4, R6.1). bypassCooldown=true is the manual
	// trigger path; it always proceeds.
	if !bypassCooldown {
		recs, err := w.store.ListAppUpdates(app, 1)
		if err != nil {
			// A read error must not block trigger — surface a warning and
			// proceed. The pipeline (task 3.6) will still record this attempt.
			logAutoUpdate(autoUpdateLogFields{
				App:        app,
				InstanceID: w.instanceID,
				Message:    fmt.Sprintf("list records failed: %v", err),
			})
		} else if len(recs) > 0 {
			cooldownMicros := int64(w.cooldown / time.Microsecond)
			if recs[0].StartedAt+cooldownMicros > nowMicros {
				w.publishFeed(AppUpdateFeedPayload{
					InstanceID: w.instanceID,
					App:        app,
					Stage:      string(db.StagePending),
					ErrorCode:  ErrSkippedCooldown,
					Message:    "cooldown active; skipping auto-update",
					At:         nowMicros,
				})
				w.observeAttempt(app, resultSkippedCooldown)
				return nil
			}
		}
	}

	// (c) Update_Window guard (R2.5, R6.1). bypassWindow=true is the manual
	// trigger path; it always proceeds.
	if !bypassWindow {
		spec := w.lookupWindowLabel(ctx, app)
		allowed, err := parseWindowFn(spec)
		if err != nil {
			// An invalid window spec must not block trigger; log and treat as
			// "any time".
			logAutoUpdate(autoUpdateLogFields{
				App:        app,
				InstanceID: w.instanceID,
				Message:    fmt.Sprintf("invalid window %q: %v", spec, err),
			})
		} else if allowed != nil && !allowed(now) {
			w.publishFeed(AppUpdateFeedPayload{
				InstanceID: w.instanceID,
				App:        app,
				Stage:      string(db.StagePending),
				ErrorCode:  ErrSkippedWindow,
				Message:    "outside update window; skipping auto-update",
				At:         nowMicros,
			})
			w.observeAttempt(app, resultSkippedWindow)
			return nil
		}
	}

	// (d) Resolve compose YAML for the app.
	if w.getCompose == nil {
		return fmt.Errorf("auto-update: getCompose not configured for app %q", app)
	}
	composeYAML, err := w.getCompose(app)
	if err != nil {
		logAutoUpdate(autoUpdateLogFields{
			App:        app,
			InstanceID: w.instanceID,
			Message:    fmt.Sprintf("resolve compose failed: %v", err),
		})
		return err
	}

	// (e) List the project containers so we can capture per-service previous
	// digests via ImageInspect.RepoDigests[0].
	projectFilter := "dockpal.project=" + app
	containers, err := w.client.ListContainersWithLabel(ctx, projectFilter)
	if err != nil {
		logAutoUpdate(autoUpdateLogFields{
			App:        app,
			InstanceID: w.instanceID,
			Message:    fmt.Sprintf("list project containers failed: %v", err),
		})
		return err
	}
	if len(containers) == 0 {
		logAutoUpdate(autoUpdateLogFields{
			App:        app,
			InstanceID: w.instanceID,
			Message:    "no project containers; skipping",
		})
		return nil
	}

	// (f) Parse the compose to enumerate services and their declared images.
	cf, err := ParseComposeFile(composeYAML)
	if err != nil {
		logAutoUpdate(autoUpdateLogFields{
			App:        app,
			InstanceID: w.instanceID,
			Message:    fmt.Sprintf("parse compose failed: %v", err),
		})
		return err
	}

	// (g) Capture previous digests per service. Walk container summaries,
	// match by `dockpal.service` label → service name. ImageInspect each
	// container Image to extract the digest portion of RepoDigests[0]. The
	// digest is later consumed by the rollback helper to pin the compose
	// YAML back to the previously-running image.
	previousDigests := make(map[string]string, len(cf.Services))
	for _, ctr := range containers {
		svcName := ctr.Labels["dockpal.service"]
		if svcName == "" {
			continue
		}
		if _, ok := cf.Services[svcName]; !ok {
			continue
		}
		if _, seen := previousDigests[svcName]; seen {
			continue
		}
		digest, ierr := w.client.InspectRepoDigest(ctx, ctr.Image)
		if ierr != nil {
			logAutoUpdate(autoUpdateLogFields{
				App:        app,
				InstanceID: w.instanceID,
				Service:    svcName,
				Image:      ctr.Image,
				Message:    fmt.Sprintf("inspect previous digest failed: %v", ierr),
			})
			// continue without a previous digest; rollback for this service
			// is skipped (rollback_no_previous_digest, per design.md notes).
		}
		previousDigests[svcName] = digest
	}

	// (h) Build the App_Update_Record. Services map covers every service
	// declared in the compose; per-service NewDigest is populated from the
	// monitor cache (Result.RemoteDigest). Services without an entry in the
	// cache simply leave NewDigest empty.
	attemptID := fmt.Sprintf("%d-%s", nowMicros, app)
	services := make(map[string]db.ServiceUpdateInfo, len(cf.Services))
	for svcName, svc := range cf.Services {
		info := db.ServiceUpdateInfo{
			Image:          svc.Image,
			PreviousDigest: previousDigests[svcName],
		}
		if w.monitor != nil {
			if status := w.monitor.GetStatus(svc.Image); status != nil && status.Result != nil {
				info.NewDigest = status.Result.RemoteDigest
			}
		}
		services[svcName] = info
	}

	rec := &db.AppUpdateRecord{
		AttemptID:   attemptID,
		InstanceID:  w.instanceID,
		App:         app,
		Services:    services,
		Stage:       db.StagePulling,
		TriggeredBy: triggeredBy,
		StartedAt:   nowMicros,
		UpdatedAt:   nowMicros,
	}

	// Stage 1: pulling.
	w.appendEvent(rec, db.StagePulling, "", "starting image pulls")
	pullStartedAt := time.Now()

	// (i) Pull each updated service. We only pull services whose monitor
	// cache entry reports HasUpdate=true. Without a monitor (tests) we
	// conservatively skip pulls and let DeployCompose's forcePull=true do
	// the work.
	for svcName, svc := range cf.Services {
		if !w.serviceHasUpdate(svc.Image) {
			continue
		}
		authHeader := ""
		if w.getAuth != nil {
			if h, aerr := w.getAuth(svc.Image); aerr == nil {
				authHeader = h
			}
		}
		if perr := w.client.ForcePullImage(ctx, svc.Image, authHeader); perr != nil {
			code := classifyPullError(perr)
			msg := fmt.Sprintf("pull failed for service %q image %q: %v", svcName, svc.Image, perr)
			w.observeStageDuration(app, string(db.StagePulling), time.Since(pullStartedAt).Seconds())
			w.appendEvent(rec, db.StageFailed, code, msg)
			w.observeAttempt(app, resultFailed)
			return nil
		}
	}
	w.observeStageDuration(app, string(db.StagePulling), time.Since(pullStartedAt).Seconds())

	// Stage 2: recreating.
	w.appendEvent(rec, db.StageRecreating, "", "redeploying compose project")
	recreateStartedAt := time.Now()

	if derr := w.client.DeployCompose(ctx, app, composeYAML, w.getAuth, true); derr != nil {
		msg := fmt.Sprintf("compose redeploy failed: %v", derr)
		w.observeStageDuration(app, string(db.StageRecreating), time.Since(recreateStartedAt).Seconds())
		// Attempt rollback by pinning each service to its previous digest.
		if rerr := w.rollback(ctx, app, composeYAML, previousDigests); rerr != nil {
			failMsg := fmt.Sprintf("%s; rollback also failed: %v", msg, rerr)
			w.appendEvent(rec, db.StageFailed, ErrRollbackFailed, failMsg)
			w.observeAttempt(app, resultFailed)
			w.notifyWebhooks(app, w.instanceID, string(db.StageFailed), ErrRollbackFailed, failMsg, attemptID)
			return nil
		}
		w.appendEvent(rec, db.StageRolledBack, ErrComposeError, msg)
		w.observeAttempt(app, resultRolledBack)
		w.notifyWebhooks(app, w.instanceID, string(db.StageRolledBack), ErrComposeError, msg, attemptID)
		return nil
	}
	w.observeStageDuration(app, string(db.StageRecreating), time.Since(recreateStartedAt).Seconds())

	// Stage 3: verifying.
	w.appendEvent(rec, db.StageVerifying, "", "running post-deploy health probe")
	verifyStartedAt := time.Now()

	if _, herr := w.client.HealthProbe(ctx, app, w.grace); herr != nil {
		msg := fmt.Sprintf("health probe failed: %v", herr)
		w.observeStageDuration(app, string(db.StageVerifying), time.Since(verifyStartedAt).Seconds())
		if rerr := w.rollback(ctx, app, composeYAML, previousDigests); rerr != nil {
			failMsg := fmt.Sprintf("%s; rollback also failed: %v", msg, rerr)
			w.appendEvent(rec, db.StageFailed, ErrRollbackFailed, failMsg)
			w.observeAttempt(app, resultFailed)
			w.notifyWebhooks(app, w.instanceID, string(db.StageFailed), ErrRollbackFailed, failMsg, attemptID)
			return nil
		}
		w.appendEvent(rec, db.StageRolledBack, ErrHealthProbeFailed, msg)
		w.observeAttempt(app, resultRolledBack)
		w.notifyWebhooks(app, w.instanceID, string(db.StageRolledBack), ErrHealthProbeFailed, msg, attemptID)
		return nil
	}
	w.observeStageDuration(app, string(db.StageVerifying), time.Since(verifyStartedAt).Seconds())

	// Stage 4: completed.
	completedAt := time.Now().UnixMicro()
	rec.CompletedAt = completedAt
	w.appendEvent(rec, db.StageCompleted, "", "auto-update completed successfully")
	w.observeAttempt(app, resultSuccess)
	return nil
}

// serviceHasUpdate consults the monitor cache to decide whether a service's
// image is known to have an update available. When the monitor is nil
// (used in some unit tests) the function returns false so the pull loop
// becomes a no-op and DeployCompose's forcePull=true does the heavy lifting.
func (w *AutoUpdateWorker) serviceHasUpdate(image string) bool {
	if w.monitor == nil {
		return false
	}
	status := w.monitor.GetStatus(image)
	if status == nil || status.Result == nil {
		return false
	}
	return status.Result.HasUpdate
}

// classifyPullError maps a ForcePullImage error to the persisted error_code
// value. Any error message hinting at registry authentication or
// authorization translates to ErrAuthMissing; anything else is the generic
// ErrPullError.
func classifyPullError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "authentication") ||
		strings.Contains(msg, "denied") ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "403") {
		return ErrAuthMissing
	}
	return ErrPullError
}

// inspectRepoDigest formerly lived on *AutoUpdateWorker; the equivalent
// behavior is now provided by (*Client).InspectRepoDigest so the worker can
// stay decoupled from the moby client surface (and so unit tests can stub
// the autoUpdaterClient interface without poking at private fields).

// appendEvent centralizes the stage-transition bookkeeping: it bumps the
// record's stage and metadata, appends the event, persists the record via
// the store, publishes a feed event, and writes a structured log line.
//
// The full record is rewritten through SaveAppUpdate (rather than
// AppendAppUpdateEvent) so a fresh record can transition from "no record"
// to its first stage in a single call without a prior save.
func (w *AutoUpdateWorker) appendEvent(rec *db.AppUpdateRecord, stage db.AppUpdateStage, errorCode, message string) {
	if rec == nil {
		return
	}
	now := time.Now().UnixMicro()
	ev := db.AppUpdateEvent{
		At:      now,
		Stage:   stage,
		Message: message,
	}
	rec.Stage = stage
	if errorCode != "" {
		rec.ErrorCode = errorCode
	}
	if message != "" {
		rec.Message = message
	}
	rec.UpdatedAt = now
	rec.Events = append(rec.Events, ev)

	if w.store != nil {
		if err := w.store.SaveAppUpdate(rec); err != nil {
			logAutoUpdate(autoUpdateLogFields{
				AttemptID:  rec.AttemptID,
				App:        rec.App,
				InstanceID: rec.InstanceID,
				Stage:      string(stage),
				ErrorCode:  errorCode,
				Message:    fmt.Sprintf("save record failed: %v", err),
			})
		}
	}

	w.publishFeed(AppUpdateFeedPayload{
		AttemptID:  rec.AttemptID,
		InstanceID: rec.InstanceID,
		App:        rec.App,
		Stage:      string(stage),
		ErrorCode:  errorCode,
		Message:    message,
		At:         now,
	})

	logAutoUpdate(autoUpdateLogFields{
		AttemptID:  rec.AttemptID,
		App:        rec.App,
		InstanceID: rec.InstanceID,
		Stage:      string(stage),
		ErrorCode:  errorCode,
		Message:    message,
	})
}

// rollback rewrites the compose YAML to pin every service whose previous
// digest was captured to `repo@<previousDigest>` and redeploys with
// forcePull=false so the local images are reused. Services without a
// previous digest are skipped with the rollback_no_previous_digest log
// code (per design.md notes).
//
// If none of the services has a previous digest available the rollback
// cannot make any progress; the function returns a rollback_no_previous_digest
// error so the caller marks the attempt as `failed` with `rollback_failed`
// (R3.5). The same applies if DeployCompose itself fails — the error is
// returned to the caller wrapped with the rollback_failed semantic so the
// pipeline can record it.
func (w *AutoUpdateWorker) rollback(ctx context.Context, app, composeYAML string, previousDigests map[string]string) error {
	cf, err := ParseComposeFile(composeYAML)
	if err != nil {
		return fmt.Errorf("rollback parse compose for app %q: %w", app, err)
	}

	rolled := composeYAML
	pinned := 0
	for svcName := range cf.Services {
		digest := previousDigests[svcName]
		if digest == "" {
			logAutoUpdate(autoUpdateLogFields{
				App:        app,
				InstanceID: w.instanceID,
				Service:    svcName,
				Stage:      "rollback",
				ErrorCode:  "rollback_no_previous_digest",
				Message:    "skipping digest pin",
			})
			continue
		}
		next, rerr := RewriteImageDigest(rolled, svcName, digest)
		if rerr != nil {
			return fmt.Errorf("rollback rewrite app=%q service=%q: %w", app, svcName, rerr)
		}
		rolled = next
		pinned++
	}

	if pinned == 0 {
		return fmt.Errorf("rollback_no_previous_digest: no previous digests captured for app %q", app)
	}

	if err := w.client.DeployCompose(ctx, app, rolled, w.getAuth, false); err != nil {
		return fmt.Errorf("rollback redeploy for app %q: %w", app, err)
	}
	logAutoUpdate(autoUpdateLogFields{
		App:        app,
		InstanceID: w.instanceID,
		Stage:      "rollback",
		Message:    fmt.Sprintf("rollback redeploy succeeded; pinned %d service(s)", pinned),
	})
	return nil
}

// publishFeed broadcasts an App_Update_Feed payload when a feed callback is
// configured. A nil callback is a no-op so tests and disabled deployments
// can omit the feed entirely.
func (w *AutoUpdateWorker) publishFeed(payload AppUpdateFeedPayload) {
	if w.feed == nil {
		return
	}
	w.feed(payload)
}

// lookupWindowLabel returns the value of the `dockpal.auto-update.window`
// label for any container belonging to the given compose project. It scans
// the opt-in container list (label `dockpal.auto-update=true`) filtered to
// the matching `dockpal.project=<app>`. The first non-empty match wins; an
// empty string means "any time" per the requirements glossary.
func (w *AutoUpdateWorker) lookupWindowLabel(ctx context.Context, app string) string {
	if w.client == nil {
		return ""
	}
	containers, err := w.client.ListContainersWithLabel(ctx, "dockpal.auto-update=true")
	if err != nil {
		logAutoUpdate(autoUpdateLogFields{
			App:        app,
			InstanceID: w.instanceID,
			Message:    fmt.Sprintf("window lookup failed: %v", err),
		})
		return ""
	}
	for _, ctr := range containers {
		if ctr.Labels["dockpal.project"] != app {
			continue
		}
		if v := ctr.Labels["dockpal.auto-update.window"]; v != "" {
			return v
		}
	}
	return ""
}

// windowSpecRE matches the strict `HH:MM-HH:MM` form (24-hour, two digits
// each side) used by the `dockpal.auto-update.window` label. Cron-style
// specs and other variants are out of scope for phase 1.
var windowSpecRE = regexp.MustCompile(`^(\d{2}):(\d{2})-(\d{2}):(\d{2})$`)

// parseWindow returns a predicate that decides whether a given time.Time
// falls inside the configured Update_Window (host local time per R2.5).
//
// Supported forms:
//
//   - "" (empty, possibly with surrounding whitespace) → any time, the
//     predicate always returns true.
//   - "HH:MM-HH:MM" (24-hour, two digits each side):
//   - When start < end (e.g. "09:00-17:00"), the predicate is true while
//     the wall-clock minute-of-day is in [start, end).
//   - When start > end (overnight, e.g. "22:00-04:00"), the predicate is
//     true while the wall-clock minute-of-day is >= start OR < end.
//
// Any other format returns an error wrapping "unsupported window spec".
// Out-of-range hour or minute components return "invalid time component
// in window spec". A zero-duration range (start == end) returns
// "window spec ... has zero duration"; callers that want "any time"
// should pass an empty spec instead.
func parseWindow(spec string) (func(time.Time) bool, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return func(time.Time) bool { return true }, nil
	}

	m := windowSpecRE.FindStringSubmatch(trimmed)
	if m == nil {
		return nil, fmt.Errorf("unsupported window spec %q", spec)
	}

	startH, _ := strconv.Atoi(m[1])
	startM, _ := strconv.Atoi(m[2])
	endH, _ := strconv.Atoi(m[3])
	endM, _ := strconv.Atoi(m[4])

	if startH > 23 || endH > 23 || startM > 59 || endM > 59 {
		return nil, fmt.Errorf("invalid time component in window spec %q", spec)
	}

	startMin := startH*60 + startM
	endMin := endH*60 + endM

	if startMin == endMin {
		return nil, fmt.Errorf("window spec %q has zero duration", spec)
	}

	if startMin < endMin {
		// Same-day window [start, end).
		return func(t time.Time) bool {
			local := t.Local()
			cur := local.Hour()*60 + local.Minute()
			return cur >= startMin && cur < endMin
		}, nil
	}

	// Overnight window: [start, 24:00) ∪ [00:00, end).
	return func(t time.Time) bool {
		local := t.Local()
		cur := local.Hour()*60 + local.Minute()
		return cur >= startMin || cur < endMin
	}, nil
}

// readAutoUpdateEnabled parses DOCKPAL_AUTO_UPDATE_ENABLED. Default true.
// "false" or "0" (case-insensitive, trimmed) disables the worker. Any other
// non-empty unrecognized value falls back to the default with a warning.
func readAutoUpdateEnabled() bool {
	raw, ok := os.LookupEnv(envAutoUpdateEnabled)
	if !ok {
		return defaultAutoUpdateEnabled
	}
	v := strings.TrimSpace(strings.ToLower(raw))
	switch v {
	case "", "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		logAutoUpdate(autoUpdateLogFields{
			Message: fmt.Sprintf("%s=%q not recognized; falling back to default %t",
				envAutoUpdateEnabled, raw, defaultAutoUpdateEnabled),
		})
		return defaultAutoUpdateEnabled
	}
}

// readAutoUpdateCooldown parses DOCKPAL_AUTO_UPDATE_COOLDOWN_MINUTES.
// Default 60 minutes; values below minAutoUpdateCooldownMinutes or unparsable
// fall back to the default with a warning.
func readAutoUpdateCooldown() time.Duration {
	raw, ok := os.LookupEnv(envAutoUpdateCooldownMinutes)
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultAutoUpdateCooldown
	}
	m, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || m < minAutoUpdateCooldownMinutes {
		logAutoUpdate(autoUpdateLogFields{
			Message: fmt.Sprintf("%s=%q invalid; falling back to default %s",
				envAutoUpdateCooldownMinutes, raw, defaultAutoUpdateCooldown),
		})
		return defaultAutoUpdateCooldown
	}
	return time.Duration(m) * time.Minute
}

// readAutoUpdateConcurrency parses DOCKPAL_AUTO_UPDATE_CONCURRENCY.
// Default 2; clamped to [minAutoUpdateConcurrency, maxAutoUpdateConcurrency].
// Out-of-range or unparsable values fall back to the default with a warning.
func readAutoUpdateConcurrency() int {
	raw, ok := os.LookupEnv(envAutoUpdateConcurrency)
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultAutoUpdateConcurrency
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < minAutoUpdateConcurrency || n > maxAutoUpdateConcurrency {
		logAutoUpdate(autoUpdateLogFields{
			Message: fmt.Sprintf("%s=%q invalid; falling back to default %d",
				envAutoUpdateConcurrency, raw, defaultAutoUpdateConcurrency),
		})
		return defaultAutoUpdateConcurrency
	}
	return n
}

// readAutoUpdateGrace parses DOCKPAL_AUTO_UPDATE_HEALTH_GRACE_SECONDS.
// Default 60 seconds; values below minAutoUpdateGraceSeconds or unparsable
// fall back to the default with a warning.
func readAutoUpdateGrace() time.Duration {
	raw, ok := os.LookupEnv(envAutoUpdateGraceSeconds)
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultAutoUpdateGrace
	}
	s, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || s < minAutoUpdateGraceSeconds {
		logAutoUpdate(autoUpdateLogFields{
			Message: fmt.Sprintf("%s=%q invalid; falling back to default %s",
				envAutoUpdateGraceSeconds, raw, defaultAutoUpdateGrace),
		})
		return defaultAutoUpdateGrace
	}
	return time.Duration(s) * time.Second
}
