package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// =============================================================================
// fakeAutoUpdaterClient: scripted implementation of the autoUpdaterClient
// interface used by the AutoUpdateWorker. Each method records its calls and
// consults a per-method scripted result so individual tests can plug different
// behaviors without touching unrelated paths.
// =============================================================================

type fakeAutoUpdaterClient struct {
	mu sync.Mutex

	// listContainers
	listByLabel map[string][]ContainerInfo

	// pull
	pullErr     error
	pullErrByImage map[string]error
	pullCalls   atomic.Int32
	pullImages  []string

	// deploy
	deployErr  error
	// deployErrSeq lets a test return different errors per DeployCompose call
	// (call #1 = pipeline redeploy, call #2 = rollback redeploy).
	deployErrSeq []error
	// deployGate, when non-nil, blocks each DeployCompose call until the
	// channel is closed. Used by the per-app mutex test to keep two
	// goroutines racing inside TriggerApp simultaneously.
	deployGate chan struct{}
	deployCalls []deployCall

	// health probe
	healthErr   error
	healthCalls atomic.Int32

	// inspect
	inspectDigest map[string]string // image -> digest portion
	inspectErr    error
	inspectCalls  atomic.Int32
}

type deployCall struct {
	project     string
	composeYAML string
	forcePull   bool
}

func (f *fakeAutoUpdaterClient) ListContainersWithLabel(_ context.Context, label string) ([]ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listByLabel == nil {
		return nil, nil
	}
	// Return a copy so the worker cannot mutate the scripted slice.
	src := f.listByLabel[label]
	out := make([]ContainerInfo, len(src))
	copy(out, src)
	return out, nil
}

func (f *fakeAutoUpdaterClient) ForcePullImage(_ context.Context, image, _ string) error {
	f.pullCalls.Add(1)
	f.mu.Lock()
	f.pullImages = append(f.pullImages, image)
	per := f.pullErrByImage[image]
	f.mu.Unlock()
	if per != nil {
		return per
	}
	return f.pullErr
}

func (f *fakeAutoUpdaterClient) DeployCompose(_ context.Context, project, composeYAML string, _ AuthHeaderFunc, forcePull bool) error {
	// Record the call before blocking on the gate so the test can observe
	// concurrent entry into DeployCompose for the per-app mutex test.
	f.mu.Lock()
	idx := len(f.deployCalls)
	f.deployCalls = append(f.deployCalls, deployCall{
		project:     project,
		composeYAML: composeYAML,
		forcePull:   forcePull,
	})
	gate := f.deployGate
	var perCall error
	if idx < len(f.deployErrSeq) {
		perCall = f.deployErrSeq[idx]
	}
	f.mu.Unlock()

	if gate != nil {
		<-gate
	}

	if perCall != nil {
		return perCall
	}
	return f.deployErr
}

func (f *fakeAutoUpdaterClient) HealthProbe(_ context.Context, _ string, _ time.Duration) ([]HealthProbeResult, error) {
	f.healthCalls.Add(1)
	if f.healthErr != nil {
		return nil, f.healthErr
	}
	return nil, nil
}

func (f *fakeAutoUpdaterClient) InspectRepoDigest(_ context.Context, image string) (string, error) {
	f.inspectCalls.Add(1)
	if f.inspectErr != nil {
		return "", f.inspectErr
	}
	if f.inspectDigest == nil {
		return "", nil
	}
	return f.inspectDigest[image], nil
}

func (f *fakeAutoUpdaterClient) deploys() []deployCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]deployCall, len(f.deployCalls))
	copy(out, f.deployCalls)
	return out
}

// =============================================================================
// fakeAppUpdateStore: in-memory implementation of db.AppUpdateStore for tests.
// Only the methods the worker actually invokes need rich behavior; the rest
// are stubs that satisfy the interface.
// =============================================================================

type fakeAppUpdateStore struct {
	mu      sync.Mutex
	byID    map[string]*db.AppUpdateRecord
	byApp   map[string][]*db.AppUpdateRecord // newest-last; ListAppUpdates reverses
	saveErr error
}

func newFakeStore() *fakeAppUpdateStore {
	return &fakeAppUpdateStore{
		byID:  make(map[string]*db.AppUpdateRecord),
		byApp: make(map[string][]*db.AppUpdateRecord),
	}
}

func (s *fakeAppUpdateStore) SaveAppUpdate(rec *db.AppUpdateRecord) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Deep-ish copy: the worker mutates the record across stages, so we
	// snapshot Events to keep historical saves observable in tests.
	cp := *rec
	cp.Events = append([]db.AppUpdateEvent(nil), rec.Events...)
	if existing, ok := s.byID[rec.AttemptID]; ok {
		// Replace the record in byApp with the newer copy.
		for i, r := range s.byApp[rec.App] {
			if r == existing {
				s.byApp[rec.App][i] = &cp
				break
			}
		}
	} else {
		s.byApp[rec.App] = append(s.byApp[rec.App], &cp)
	}
	s.byID[rec.AttemptID] = &cp
	return nil
}

func (s *fakeAppUpdateStore) AppendAppUpdateEvent(attemptID string, ev db.AppUpdateEvent, stage db.AppUpdateStage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[attemptID]
	if !ok {
		return db.ErrAppUpdateNotFound
	}
	rec.Events = append(rec.Events, ev)
	rec.Stage = stage
	if ev.At > rec.UpdatedAt {
		rec.UpdatedAt = ev.At
	}
	return nil
}

func (s *fakeAppUpdateStore) ListAppUpdates(app string, limit int) ([]db.AppUpdateRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.byApp[app]
	out := make([]db.AppUpdateRecord, 0, len(src))
	// Newest-first by StartedAt: walk from the end. Ties keep insertion order.
	for i := len(src) - 1; i >= 0; i-- {
		out = append(out, *src[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *fakeAppUpdateStore) ListAllAppUpdates(_ string, _ int) ([]db.AppUpdateRecord, error) {
	return nil, nil
}

func (s *fakeAppUpdateStore) GetAppUpdate(attemptID string) (*db.AppUpdateRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[attemptID]
	if !ok {
		return nil, nil
	}
	cp := *rec
	cp.Events = append([]db.AppUpdateEvent(nil), rec.Events...)
	return &cp, nil
}

func (s *fakeAppUpdateStore) PurgeOlderThan(_, _ int) (int, error) {
	return 0, nil
}

// recordsForApp returns the in-memory records for an app (newest-first).
func (s *fakeAppUpdateStore) recordsForApp(app string) []db.AppUpdateRecord {
	out, _ := s.ListAppUpdates(app, 0)
	return out
}

// =============================================================================
// Test helpers.
// =============================================================================

// newWorker builds an AutoUpdateWorker without going through the env-var
// reading constructor so tests stay deterministic and free of process-wide
// side effects. The returned worker has no monitor (so the pull loop relies
// on serviceHasUpdate=false unless overridden), a feed callback that
// captures events, and a per-app mutex map ready for use.
func newWorker(t *testing.T, fc *fakeAutoUpdaterClient, store db.AppUpdateStore, composeYAML string) (*AutoUpdateWorker, *[]AppUpdateFeedPayload) {
	t.Helper()
	var feedMu sync.Mutex
	var feedEvents []AppUpdateFeedPayload
	feed := func(ev AppUpdateFeedPayload) {
		feedMu.Lock()
		feedEvents = append(feedEvents, ev)
		feedMu.Unlock()
	}

	w := &AutoUpdateWorker{
		client:      fc,
		monitor:     nil,
		store:       store,
		feed:        feed,
		getAuth:     func(string) (string, error) { return "", nil },
		getCompose:  func(string) (string, error) { return composeYAML, nil },
		cooldown:    time.Hour,
		grace:       5 * time.Second,
		concurrency: 2,
		enabled:     true,
		sem:         make(chan struct{}, 2),
		instanceID:  "test-instance",
	}

	// Tests construct their own context per call; nothing else to wire.
	return w, &feedEvents
}

// stableComposeYAML is a minimal compose with one service used by most tests.
// Using nginx:1.25 keeps the image string stable so digest map keys match.
const stableComposeYAML = `services:
  web:
    image: nginx:1.25
    labels:
      dockpal.project: app-a
      dockpal.service: web
`

// projectContainer builds a ContainerInfo describing a single container that
// belongs to compose project `app` with service label `svc` and image `image`.
func projectContainer(app, svc, image string) ContainerInfo {
	return ContainerInfo{
		ID:    fmt.Sprintf("ctr-%s-%s", app, svc),
		Name:  fmt.Sprintf("%s-%s", app, svc),
		Image: image,
		Labels: map[string]string{
			"dockpal.project":     app,
			"dockpal.service":     svc,
			"dockpal.auto-update": "true",
		},
	}
}

// withParseWindowFn temporarily swaps the package-level parseWindowFn used by
// TriggerApp's window guard. Returned closure restores the previous binding.
func withParseWindowFn(t *testing.T, fn func(spec string) (func(time.Time) bool, error)) {
	t.Helper()
	prev := parseWindowFn
	parseWindowFn = fn
	t.Cleanup(func() { parseWindowFn = prev })
}

// =============================================================================
// Tests.
// =============================================================================

// TestTriggerApp_OptInFilter verifies the worker exits cleanly when a project
// has no opt-in containers (none with the dockpal.project=<app> label match).
// Validates: Requirements R1.1, R1.2.
func TestTriggerApp_OptInFilter(t *testing.T) {
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			// Project filter returns no rows: the app the worker is asked to
			// trigger is not present in the project label set.
			"dockpal.project=other-app": nil,
			// The window-lookup label call returns an unrelated project.
			"dockpal.auto-update=true": {
				projectContainer("unrelated", "svc", "redis:7"),
			},
		},
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)

	// Bypass cooldown/window so the trigger reaches the project-container
	// listing — that is the gate this test exercises.
	if err := w.TriggerApp(context.Background(), "other-app", true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: unexpected error: %v", err)
	}

	if got := store.recordsForApp("other-app"); len(got) != 0 {
		t.Fatalf("expected no records when project has no opt-in containers, got %d", len(got))
	}
	if fc.pullCalls.Load() != 0 {
		t.Fatalf("expected no pulls for skipped project, got %d", fc.pullCalls.Load())
	}
	if got := fc.deploys(); len(got) != 0 {
		t.Fatalf("expected no deploys for skipped project, got %d", len(got))
	}
}

// TestTriggerApp_CooldownEnforcement verifies the cooldown gate short-circuits
// the trigger when the most recent record's StartedAt is within the cooldown
// window, and that bypassCooldown=true bypasses the gate.
// Validates: Requirements R2.4, R6.1.
func TestTriggerApp_CooldownEnforcement(t *testing.T) {
	app := "app-a"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true":      {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:        {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:old"},
	}
	store := newFakeStore()

	// Pre-populate a recent record so cooldown should fire.
	now := time.Now()
	recent := &db.AppUpdateRecord{
		AttemptID:  "prev",
		InstanceID: "test-instance",
		App:        app,
		Stage:      db.StageCompleted,
		StartedAt:  now.Add(-30 * time.Minute).UnixMicro(),
		UpdatedAt:  now.Add(-30 * time.Minute).UnixMicro(),
	}
	if err := store.SaveAppUpdate(recent); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	w, feedRef := newWorker(t, fc, store, stableComposeYAML)
	w.cooldown = time.Hour

	// Cooldown active: trigger should return nil without writing a new record.
	if err := w.TriggerApp(context.Background(), app, false, true, "auto"); err != nil {
		t.Fatalf("TriggerApp (cooldown): %v", err)
	}
	recs := store.recordsForApp(app)
	if len(recs) != 1 || recs[0].AttemptID != "prev" {
		t.Fatalf("expected cooldown to leave only the seeded record, got %v", recs)
	}
	if fc.pullCalls.Load() != 0 {
		t.Fatalf("cooldown skip should not trigger a pull, got %d", fc.pullCalls.Load())
	}

	// Verify a feed event with ErrSkippedCooldown was published.
	sawSkipped := false
	for _, ev := range *feedRef {
		if ev.ErrorCode == ErrSkippedCooldown {
			sawSkipped = true
			break
		}
	}
	if !sawSkipped {
		t.Fatalf("expected feed event with ErrSkippedCooldown, got %+v", *feedRef)
	}

	// Bypass cooldown: pipeline must proceed past the gate. With monitor=nil
	// the worker skips pulls and lets DeployCompose with forcePull=true do
	// the pull; we just need to see DeployCompose called and a new record.
	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp (bypass): %v", err)
	}
	if got := fc.deploys(); len(got) == 0 {
		t.Fatalf("bypassCooldown should drive the pipeline past the gate; deploys=0")
	}
	if got := store.recordsForApp(app); len(got) < 2 {
		t.Fatalf("expected a new record after bypass; got %d", len(got))
	}
}

// TestTriggerApp_WindowEnforcement verifies the Update_Window guard. We
// override parseWindowFn to deny the current time, then verify the trigger
// returns nil with a skipped_window feed event and no record, and that
// bypassWindow=true bypasses the gate.
// Validates: Requirements R2.5, R6.1.
func TestTriggerApp_WindowEnforcement(t *testing.T) {
	app := "app-a"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {
				func() ContainerInfo {
					ci := projectContainer(app, "web", "nginx:1.25")
					ci.Labels["dockpal.auto-update.window"] = "00:00-00:01"
					return ci
				}(),
			},
			"dockpal.project=" + app: {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:old"},
	}
	store := newFakeStore()

	// Force the parseWindowFn to always deny so this test doesn't depend on
	// the host clock.
	withParseWindowFn(t, func(spec string) (func(time.Time) bool, error) {
		return func(time.Time) bool { return false }, nil
	})

	w, feedRef := newWorker(t, fc, store, stableComposeYAML)

	// Window denied: trigger must return nil with no record written.
	if err := w.TriggerApp(context.Background(), app, true, false, "auto"); err != nil {
		t.Fatalf("TriggerApp (window): %v", err)
	}
	if got := store.recordsForApp(app); len(got) != 0 {
		t.Fatalf("window skip must not write a record, got %d", len(got))
	}

	sawSkipped := false
	for _, ev := range *feedRef {
		if ev.ErrorCode == ErrSkippedWindow {
			sawSkipped = true
			break
		}
	}
	if !sawSkipped {
		t.Fatalf("expected feed event with ErrSkippedWindow, got %+v", *feedRef)
	}

	// Bypass window: pipeline proceeds. Reset feed for clarity isn't
	// necessary; we just check a deploy now occurs.
	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp (bypass window): %v", err)
	}
	if got := fc.deploys(); len(got) == 0 {
		t.Fatalf("bypassWindow should drive the pipeline past the gate; deploys=0")
	}
}

// TestTriggerApp_HappyPath drives the full pull → recreate → verify path with
// every step succeeding. Final stage must be StageCompleted.
// Validates: Requirements R3.1, R3.2, R3.3.
func TestTriggerApp_HappyPath(t *testing.T) {
	app := "app-a"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:old"},
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	if got := fc.deploys(); len(got) != 1 {
		t.Fatalf("expected exactly 1 deploy on happy path, got %d", len(got))
	}
	if !fc.deploys()[0].forcePull {
		t.Fatalf("expected DeployCompose forcePull=true on happy path")
	}
	if fc.healthCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 health probe call, got %d", fc.healthCalls.Load())
	}

	recs := store.recordsForApp(app)
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 record, got %d", len(recs))
	}
	if recs[0].Stage != db.StageCompleted {
		t.Fatalf("expected final stage StageCompleted, got %q", recs[0].Stage)
	}
	if recs[0].ErrorCode != "" {
		t.Fatalf("expected empty error code on happy path, got %q", recs[0].ErrorCode)
	}
}

// TestTriggerApp_PullFailure_NoRollback verifies that a non-auth pull error
// records ErrPullError and never calls deploy/health/rollback.
// Validates: Requirements R3.1.
func TestTriggerApp_PullFailure_NoRollback(t *testing.T) {
	app := "app-a"

	// Use a monitor stub by stamping HasUpdate=true via a real
	// ImageUpdateMonitor cache so serviceHasUpdate returns true and the pull
	// loop actually invokes ForcePullImage. Construct manually to avoid the
	// background goroutine.
	monitor := &ImageUpdateMonitor{
		cache: map[string]*ImageUpdateStatus{
			"nginx:1.25": {
				ImageRef: "nginx:1.25",
				Result: &ImageUpdateResult{
					HasUpdate:    true,
					LocalDigest:  "sha256:old",
					RemoteDigest: "sha256:new",
				},
			},
		},
	}

	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:old"},
		pullErr:       errors.New("network timeout reaching registry"),
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)
	w.monitor = monitor

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	if fc.pullCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 pull attempt, got %d", fc.pullCalls.Load())
	}
	if got := fc.deploys(); len(got) != 0 {
		t.Fatalf("pull failure must not call DeployCompose; got %d", len(got))
	}
	if fc.healthCalls.Load() != 0 {
		t.Fatalf("pull failure must not call HealthProbe; got %d", fc.healthCalls.Load())
	}

	recs := store.recordsForApp(app)
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 record, got %d", len(recs))
	}
	if recs[0].Stage != db.StageFailed {
		t.Fatalf("expected stage StageFailed, got %q", recs[0].Stage)
	}
	if recs[0].ErrorCode != ErrPullError {
		t.Fatalf("expected error code %q, got %q", ErrPullError, recs[0].ErrorCode)
	}
}

// TestTriggerApp_PullFailure_AuthMissing verifies an auth-flavored pull error
// is classified as ErrAuthMissing.
// Validates: Requirements R3.1.
func TestTriggerApp_PullFailure_AuthMissing(t *testing.T) {
	app := "app-a"
	monitor := &ImageUpdateMonitor{
		cache: map[string]*ImageUpdateStatus{
			"nginx:1.25": {ImageRef: "nginx:1.25", Result: &ImageUpdateResult{HasUpdate: true}},
		},
	}
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		pullErr: errors.New("unauthorized: bad credentials"),
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)
	w.monitor = monitor

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	recs := store.recordsForApp(app)
	if len(recs) != 1 || recs[0].ErrorCode != ErrAuthMissing {
		t.Fatalf("expected ErrAuthMissing, got %+v", recs)
	}
}

// TestTriggerApp_ComposeFailure_RollbackCalled verifies that a DeployCompose
// failure triggers a rollback redeploy (forcePull=false) using the captured
// previous digest.
// Validates: Requirements R3.3.
func TestTriggerApp_ComposeFailure_RollbackCalled(t *testing.T) {
	app := "app-a"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:abc"},
		// First DeployCompose (the redeploy) fails; second (rollback) succeeds.
		deployErrSeq: []error{errors.New("container create failed"), nil},
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	deploys := fc.deploys()
	if len(deploys) != 2 {
		t.Fatalf("expected 2 deploy calls (redeploy + rollback), got %d", len(deploys))
	}
	if deploys[0].forcePull != true {
		t.Fatalf("first deploy should be forcePull=true, got %v", deploys[0].forcePull)
	}
	if deploys[1].forcePull != false {
		t.Fatalf("rollback deploy should be forcePull=false, got %v", deploys[1].forcePull)
	}

	recs := store.recordsForApp(app)
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 record, got %d", len(recs))
	}
	if recs[0].Stage != db.StageRolledBack {
		t.Fatalf("expected StageRolledBack, got %q", recs[0].Stage)
	}
	if recs[0].ErrorCode != ErrComposeError {
		t.Fatalf("expected ErrComposeError, got %q", recs[0].ErrorCode)
	}
}

// TestTriggerApp_HealthProbeFailure_RollbackCalled verifies that a failed
// health probe triggers rollback and records ErrHealthProbeFailed.
// Validates: Requirements R3.2, R3.3.
func TestTriggerApp_HealthProbeFailure_RollbackCalled(t *testing.T) {
	app := "app-a"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:abc"},
		healthErr:     errors.New("web container unhealthy"),
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	deploys := fc.deploys()
	if len(deploys) != 2 {
		t.Fatalf("expected 2 deploy calls (redeploy + rollback), got %d", len(deploys))
	}
	if deploys[1].forcePull != false {
		t.Fatalf("rollback deploy should be forcePull=false")
	}
	if fc.healthCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 health probe call, got %d", fc.healthCalls.Load())
	}

	recs := store.recordsForApp(app)
	if len(recs) != 1 || recs[0].Stage != db.StageRolledBack {
		t.Fatalf("expected StageRolledBack, got %+v", recs)
	}
	if recs[0].ErrorCode != ErrHealthProbeFailed {
		t.Fatalf("expected ErrHealthProbeFailed, got %q", recs[0].ErrorCode)
	}
}

// TestTriggerApp_RollbackFailure verifies that when the rollback's
// DeployCompose itself fails, the record stage is StageFailed with
// ErrRollbackFailed.
// Validates: Requirements R3.5.
func TestTriggerApp_RollbackFailure(t *testing.T) {
	app := "app-a"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:abc"},
		// Both calls fail: pipeline redeploy and rollback redeploy.
		deployErrSeq: []error{
			errors.New("redeploy failed"),
			errors.New("rollback redeploy also failed"),
		},
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	if got := len(fc.deploys()); got != 2 {
		t.Fatalf("expected 2 deploy attempts (redeploy + rollback), got %d", got)
	}

	recs := store.recordsForApp(app)
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 record, got %d", len(recs))
	}
	if recs[0].Stage != db.StageFailed {
		t.Fatalf("expected StageFailed, got %q", recs[0].Stage)
	}
	if recs[0].ErrorCode != ErrRollbackFailed {
		t.Fatalf("expected ErrRollbackFailed, got %q", recs[0].ErrorCode)
	}
}

// TestTriggerApp_PerAppMutexBlocksConcurrent verifies the per-app sync.Mutex
// rejects a second concurrent trigger with ErrUpdateAlreadyRunning while the
// first is still inside DeployCompose.
// Validates: Requirements R6.2.
func TestTriggerApp_PerAppMutexBlocksConcurrent(t *testing.T) {
	app := "app-a"
	gate := make(chan struct{})
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:abc"},
		deployGate:    gate,
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)

	type result struct {
		err error
	}
	results := make(chan result, 2)

	// Goroutine A: enters DeployCompose and parks on the gate, holding the mutex.
	go func() {
		results <- result{err: w.TriggerApp(context.Background(), app, true, true, "manual")}
	}()

	// Wait for goroutine A to be inside DeployCompose so we know the mutex
	// is held. Poll up to 2s.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(fc.deploys()) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(fc.deploys()) < 1 {
		close(gate)
		<-results
		t.Fatalf("goroutine A never entered DeployCompose")
	}

	// Goroutine B: tries to trigger while A holds the mutex.
	go func() {
		results <- result{err: w.TriggerApp(context.Background(), app, true, true, "manual")}
	}()

	// Wait briefly for B to fail-fast on the mutex.
	var rB result
	select {
	case rB = <-results:
	case <-time.After(2 * time.Second):
		close(gate)
		<-results
		<-results
		t.Fatalf("goroutine B did not return promptly; expected ErrUpdateAlreadyRunning")
	}

	// Release A.
	close(gate)
	rA := <-results

	// Exactly one nil and one ErrUpdateAlreadyRunning.
	switch {
	case rB.err == nil && rA.err != nil:
		// B got the mutex first (race-acceptable). A should be the rejected one.
		if !strings.Contains(rA.err.Error(), ErrUpdateAlreadyRunning) {
			t.Fatalf("expected error to contain %q, got %v", ErrUpdateAlreadyRunning, rA.err)
		}
	case rA.err == nil && rB.err != nil:
		if !strings.Contains(rB.err.Error(), ErrUpdateAlreadyRunning) {
			t.Fatalf("expected error to contain %q, got %v", ErrUpdateAlreadyRunning, rB.err)
		}
	default:
		t.Fatalf("expected exactly one nil and one ErrUpdateAlreadyRunning; A=%v B=%v", rA.err, rB.err)
	}
}
