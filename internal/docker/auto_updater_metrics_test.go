package docker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// recordingMetricsHooks captures every hook invocation so a scripted
// pipeline through the AutoUpdateWorker can be asserted at the boundary
// between the worker and internal/metrics. The fields mirror the three
// AutoUpdateMetricsHooks callbacks (counter, histogram, gauge).
//
// All slices are guarded by mu because the worker uses goroutines for the
// fan-out path; sequential callers can also read fields without locking via
// the snapshot helpers.
type recordingMetricsHooks struct {
	mu       sync.Mutex
	attempts []recordedAttempt
	stages   []recordedStage
	pending  []int
}

type recordedAttempt struct {
	instance string
	app      string
	result   string
}

type recordedStage struct {
	instance string
	app      string
	stage    string
	seconds  float64
}

func (r *recordingMetricsHooks) hooks() AutoUpdateMetricsHooks {
	return AutoUpdateMetricsHooks{
		Attempt: func(instance, app, result string) {
			r.mu.Lock()
			r.attempts = append(r.attempts, recordedAttempt{instance, app, result})
			r.mu.Unlock()
		},
		Duration: func(instance, app, stage string, seconds float64) {
			r.mu.Lock()
			r.stages = append(r.stages, recordedStage{instance, app, stage, seconds})
			r.mu.Unlock()
		},
		PendingUpdate: func(n int) {
			r.mu.Lock()
			r.pending = append(r.pending, n)
			r.mu.Unlock()
		},
	}
}

func (r *recordingMetricsHooks) snapshotAttempts() []recordedAttempt {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedAttempt, len(r.attempts))
	copy(out, r.attempts)
	return out
}

func (r *recordingMetricsHooks) snapshotStages() []recordedStage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedStage, len(r.stages))
	copy(out, r.stages)
	return out
}

func (r *recordingMetricsHooks) snapshotPending() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int, len(r.pending))
	copy(out, r.pending)
	return out
}

// stageSeen is a small predicate helper used by the assertion blocks.
func stageSeen(stages []recordedStage, app, stage string) bool {
	for _, s := range stages {
		if s.app == app && s.stage == stage {
			return true
		}
	}
	return false
}

// =============================================================================
// Tests
// =============================================================================

// TestMetricsHooks_HappyPath_RecordsCounterAndHistogram drives a scripted
// happy-path update through TriggerApp and verifies that:
//   - the attempts counter is incremented with result="success"
//   - the duration histogram receives an observation for each of the three
//     pipeline stages: pulling, recreating, verifying
//
// Validates: Requirements R10.1, R10.2.
func TestMetricsHooks_HappyPath_RecordsCounterAndHistogram(t *testing.T) {
	app := "app-metrics-happy"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:old"},
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)
	rec := &recordingMetricsHooks{}
	w.SetMetricsHooks(rec.hooks())

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	attempts := rec.snapshotAttempts()
	if len(attempts) != 1 {
		t.Fatalf("expected exactly 1 attempt observation, got %d: %+v", len(attempts), attempts)
	}
	if attempts[0].result != "success" {
		t.Errorf("attempt result: got %q, want success", attempts[0].result)
	}
	if attempts[0].app != app {
		t.Errorf("attempt app: got %q, want %q", attempts[0].app, app)
	}
	if attempts[0].instance != "test-instance" {
		t.Errorf("attempt instance: got %q, want test-instance", attempts[0].instance)
	}

	stages := rec.snapshotStages()
	for _, want := range []string{"pulling", "recreating", "verifying"} {
		if !stageSeen(stages, app, want) {
			t.Errorf("expected stage observation for %q; got %+v", want, stages)
		}
	}
	for _, s := range stages {
		if s.seconds < 0 {
			t.Errorf("negative duration observed for stage %q: %v", s.stage, s.seconds)
		}
	}
}

// TestMetricsHooks_PullFailure_RecordsFailedResult verifies that a pull
// error records an attempts counter with result="failed" and a stage
// observation for "pulling" (so the histogram captures the time spent before
// the failure).
//
// Validates: Requirements R10.1, R10.2.
func TestMetricsHooks_PullFailure_RecordsFailedResult(t *testing.T) {
	app := "app-metrics-pullfail"

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
		pullErr: errors.New("network timeout"),
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)
	w.monitor = monitor
	rec := &recordingMetricsHooks{}
	w.SetMetricsHooks(rec.hooks())

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	attempts := rec.snapshotAttempts()
	if len(attempts) != 1 || attempts[0].result != "failed" {
		t.Fatalf("expected one failed attempt, got %+v", attempts)
	}

	stages := rec.snapshotStages()
	if !stageSeen(stages, app, "pulling") {
		t.Errorf("expected pulling stage to be observed even on pull failure; got %+v", stages)
	}
	if stageSeen(stages, app, "recreating") || stageSeen(stages, app, "verifying") {
		t.Errorf("pull failure should not record recreating/verifying stages; got %+v", stages)
	}
}

// TestMetricsHooks_HealthProbeFailure_RecordsRolledBack verifies that a
// failed health probe records result="rolled_back" and stage observations
// for all three stages (pulling counts as 0-time when no service has an
// update; recreating and verifying both run before rollback).
//
// Validates: Requirements R10.1, R10.2.
func TestMetricsHooks_HealthProbeFailure_RecordsRolledBack(t *testing.T) {
	app := "app-metrics-rollback"
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
	rec := &recordingMetricsHooks{}
	w.SetMetricsHooks(rec.hooks())

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	attempts := rec.snapshotAttempts()
	if len(attempts) != 1 || attempts[0].result != "rolled_back" {
		t.Fatalf("expected one rolled_back attempt, got %+v", attempts)
	}

	stages := rec.snapshotStages()
	for _, want := range []string{"pulling", "recreating", "verifying"} {
		if !stageSeen(stages, app, want) {
			t.Errorf("expected stage observation for %q on rollback path; got %+v", want, stages)
		}
	}
}

// TestMetricsHooks_SkippedCooldown_RecordsResult verifies that a cooldown
// skip records result="skipped_cooldown" without firing any stage
// observation (the pipeline never enters pulling/recreating/verifying).
//
// Validates: Requirements R10.1.
func TestMetricsHooks_SkippedCooldown_RecordsResult(t *testing.T) {
	app := "app-metrics-cooldown"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
	}
	store := newFakeStore()

	// Seed a recent successful attempt so the cooldown gate fires.
	now := time.Now()
	recent := &db.AppUpdateRecord{
		AttemptID:  "prev",
		InstanceID: "test-instance",
		App:        app,
		Stage:      db.StageCompleted,
		StartedAt:  now.Add(-time.Minute).UnixMicro(), // well under cooldown=1h
		UpdatedAt:  now.Add(-time.Minute).UnixMicro(),
	}
	if err := store.SaveAppUpdate(recent); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w, _ := newWorker(t, fc, store, stableComposeYAML)
	rec := &recordingMetricsHooks{}
	w.SetMetricsHooks(rec.hooks())

	if err := w.TriggerApp(context.Background(), app, false, true, "auto"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	attempts := rec.snapshotAttempts()
	if len(attempts) != 1 || attempts[0].result != "skipped_cooldown" {
		t.Fatalf("expected one skipped_cooldown attempt, got %+v", attempts)
	}
	if got := rec.snapshotStages(); len(got) != 0 {
		t.Errorf("cooldown skip should record no stage observations; got %+v", got)
	}
}

// TestMetricsHooks_SkippedWindow_RecordsResult verifies that a window skip
// records result="skipped_window" without firing any stage observation.
//
// Validates: Requirements R10.1.
func TestMetricsHooks_SkippedWindow_RecordsResult(t *testing.T) {
	app := "app-metrics-window"
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
	}
	store := newFakeStore()

	withParseWindowFn(t, func(spec string) (func(time.Time) bool, error) {
		return func(time.Time) bool { return false }, nil
	})

	w, _ := newWorker(t, fc, store, stableComposeYAML)
	rec := &recordingMetricsHooks{}
	w.SetMetricsHooks(rec.hooks())

	if err := w.TriggerApp(context.Background(), app, true, false, "auto"); err != nil {
		t.Fatalf("TriggerApp: %v", err)
	}

	attempts := rec.snapshotAttempts()
	if len(attempts) != 1 || attempts[0].result != "skipped_window" {
		t.Fatalf("expected one skipped_window attempt, got %+v", attempts)
	}
}

// TestMetricsHooks_OnCycle_GaugeUpdated verifies the cycle listener publishes
// the count of opt-in apps with at least one image waiting to be updated
// every time it runs, including the all-zero case.
//
// Validates: Requirements R10.3.
func TestMetricsHooks_OnCycle_GaugeUpdated(t *testing.T) {
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {
				projectContainer("app-a", "web", "nginx:1.25"),
				projectContainer("app-b", "api", "redis:7"),
			},
		},
	}
	w, _ := newWorker(t, fc, newFakeStore(), stableComposeYAML)
	w.ctx = context.Background()
	rec := &recordingMetricsHooks{}
	w.SetMetricsHooks(rec.hooks())

	// Cycle 1: two cache entries with HasUpdate=true → expect 2 pending.
	w.onCycle([]ImageUpdateStatus{
		{ImageRef: "nginx:1.25", Result: &ImageUpdateResult{HasUpdate: true}},
		{ImageRef: "redis:7", Result: &ImageUpdateResult{HasUpdate: true}},
	})

	// Cycle 2: no entries with HasUpdate=true → expect a 0 reading so the
	// gauge converges instead of holding the previous value.
	w.onCycle([]ImageUpdateStatus{
		{ImageRef: "nginx:1.25", Result: &ImageUpdateResult{HasUpdate: false}},
	})

	got := rec.snapshotPending()
	if len(got) < 2 {
		t.Fatalf("expected at least 2 pending observations, got %v", got)
	}
	if got[0] != 2 {
		t.Errorf("first cycle pending: got %d, want 2", got[0])
	}
	if got[len(got)-1] != 0 {
		t.Errorf("final cycle pending: got %d, want 0", got[len(got)-1])
	}
}

// TestMetricsHooks_PerAppMutexBlocks_RecordsAlreadyRunning verifies that a
// rejected concurrent trigger increments the attempts counter with
// result="update_already_running".
//
// Validates: Requirements R10.1.
func TestMetricsHooks_PerAppMutexBlocks_RecordsAlreadyRunning(t *testing.T) {
	app := "app-metrics-already-running"
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:abc"},
	}
	store := newFakeStore()
	w, _ := newWorker(t, fc, store, stableComposeYAML)
	rec := &recordingMetricsHooks{}
	w.SetMetricsHooks(rec.hooks())

	// Pre-acquire the per-app mutex so the next TriggerApp call is rejected
	// at the gate. We use the same sync.Map the worker uses to install a
	// locked mutex.
	mu := &sync.Mutex{}
	mu.Lock()
	w.perAppMu.Store(app, mu)
	defer mu.Unlock()

	if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err == nil {
		t.Fatalf("expected ErrUpdateAlreadyRunning, got nil")
	}

	attempts := rec.snapshotAttempts()
	if len(attempts) != 1 || attempts[0].result != "update_already_running" {
		t.Fatalf("expected one update_already_running attempt, got %+v", attempts)
	}
}
