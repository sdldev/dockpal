package docker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sdldev/dockpal/internal/db"
	"pgregory.net/rapid"
)

// =============================================================================
// Property-based tests for AutoUpdateWorker.
//
// These cover Properties 1 and 2 from .kiro/specs/auto-image-update/design.md:
//
//   P1: For any random plan `[apps]`, after every TriggerApp returns the per-app
//       mutex held by the worker is released (no deadlock).
//   P2: For any random sequence of pipeline outcomes per app, the App_Update_Feed
//       events emitted for one attempt_id are an ordered subset of the canonical
//       state machine pulling → recreating → verifying → (completed|rolled_back|
//       failed). There is no backward transition.
//
// Fixtures (`fakeAutoUpdaterClient`, `fakeAppUpdateStore`, `newFakeStore`,
// `projectContainer`) are reused from auto_updater_test.go since this file
// lives in the same package.
// =============================================================================

// propAutoUpdateScenario enumerates the pipeline outcomes that the property
// tests randomly choose from. Each scenario exercises a different terminal
// stage so the state-machine invariants are checked across the full graph.
const (
	propScenarioHappy        = "happy"
	propScenarioPullFail     = "pull_fail"
	propScenarioComposeFail  = "compose_fail"
	propScenarioHealthFail   = "health_fail"
	propScenarioRollbackFail = "rollback_fail"
)

var propAutoUpdateScenarios = []string{
	propScenarioHappy,
	propScenarioPullFail,
	propScenarioComposeFail,
	propScenarioHealthFail,
	propScenarioRollbackFail,
}

// propComposeYAML returns a minimal compose YAML for the given app. Mirrors
// stableComposeYAML in auto_updater_test.go but parameterizes the project
// label so each generated app has a distinct shape. ParseComposeFile only
// requires a `services` mapping with at least one service that has an
// `image:` key, both of which are satisfied here.
func propComposeYAML(app string) string {
	return fmt.Sprintf("services:\n  web:\n    image: nginx:1.25\n    labels:\n      dockpal.project: %s\n      dockpal.service: web\n", app)
}

// propClientForScenario builds a fakeAutoUpdaterClient for one app whose
// scripted behavior matches the chosen failure scenario. A fixed previous
// digest keeps the rollback paths reachable so compose_fail / health_fail /
// rollback_fail can all reach DeployCompose for the rollback redeploy.
func propClientForScenario(app, scenario string) *fakeAutoUpdaterClient {
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {projectContainer(app, "web", "nginx:1.25")},
			"dockpal.project=" + app:   {projectContainer(app, "web", "nginx:1.25")},
		},
		inspectDigest: map[string]string{"nginx:1.25": "sha256:prev"},
	}
	switch scenario {
	case propScenarioPullFail:
		fc.pullErr = errors.New("network timeout reaching registry")
	case propScenarioComposeFail:
		// First deploy (pipeline) fails; second (rollback) succeeds.
		fc.deployErrSeq = []error{
			errors.New("container create failed"),
			nil,
		}
	case propScenarioHealthFail:
		fc.healthErr = errors.New("web container reported unhealthy")
	case propScenarioRollbackFail:
		// Both deploys fail: pipeline redeploy then rollback redeploy.
		fc.deployErrSeq = []error{
			errors.New("redeploy failed"),
			errors.New("rollback redeploy also failed"),
		}
	}
	return fc
}

// propMonitor returns a minimal ImageUpdateMonitor whose cache reports that
// nginx:1.25 has an update available. The pull loop only invokes
// ForcePullImage when serviceHasUpdate returns true, so without a monitor
// the pull_fail scenario would silently skip. This monitor is shared across
// all per-app workers in the property tests; for non-pull-failing scenarios
// pullErr is nil so the pull is a no-op success.
func propMonitor() *ImageUpdateMonitor {
	return &ImageUpdateMonitor{
		cache: map[string]*ImageUpdateStatus{
			"nginx:1.25": {
				ImageRef: "nginx:1.25",
				Result: &ImageUpdateResult{
					HasUpdate:    true,
					LocalDigest:  "sha256:prev",
					RemoteDigest: "sha256:new",
				},
			},
		},
	}
}

// propBuildWorker constructs an AutoUpdateWorker wired with the supplied
// fake client, shared store + monitor, and an aggregating feed callback
// that appends every published payload into the shared events slice
// (guarded by mu). Returning the worker rather than calling newWorker lets
// the property tests share one feed sink across many per-app workers.
func propBuildWorker(
	fc *fakeAutoUpdaterClient,
	store db.AppUpdateStore,
	monitor *ImageUpdateMonitor,
	composeYAML string,
	events *[]AppUpdateFeedPayload,
	mu *sync.Mutex,
) *AutoUpdateWorker {
	feed := func(ev AppUpdateFeedPayload) {
		mu.Lock()
		*events = append(*events, ev)
		mu.Unlock()
	}
	return &AutoUpdateWorker{
		client:      fc,
		monitor:     monitor,
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
}

// TestProperty_NoMutexHeldAfterPlan exercises Property 1 from design.md:
// for any random plan of N apps with mixed pipeline outcomes, every per-app
// mutex held inside the worker is released by the time TriggerApp returns,
// so a subsequent TryLock succeeds. Triggers run sequentially — the mutex
// invariant still holds, and the test stays deterministic.
//
// **Validates: Property 1 (Requirements R2.6, R6.2).**
func TestProperty_NoMutexHeldAfterPlan(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(rt, "numApps")

		store := newFakeStore()
		monitor := propMonitor()
		var feedMu sync.Mutex
		var feedEvents []AppUpdateFeedPayload

		type appPlan struct {
			app    string
			worker *AutoUpdateWorker
		}
		plans := make([]appPlan, 0, n)
		for i := 0; i < n; i++ {
			scenario := rapid.SampledFrom(propAutoUpdateScenarios).Draw(rt, "scenario")
			app := fmt.Sprintf("rapid-app-%d", i)
			fc := propClientForScenario(app, scenario)
			w := propBuildWorker(fc, store, monitor, propComposeYAML(app), &feedEvents, &feedMu)
			plans = append(plans, appPlan{app: app, worker: w})
		}

		// Drive the plan sequentially. bypassCooldown=true and bypassWindow=true
		// keep this property focused on the pipeline mutex, not on the gates.
		for _, p := range plans {
			if err := p.worker.TriggerApp(context.Background(), p.app, true, true, "manual"); err != nil {
				rt.Fatalf("TriggerApp(%s) returned error: %v", p.app, err)
			}
		}

		// Invariant: the per-app mutex stored under perAppMu[app] must be
		// re-acquirable. TriggerApp acquires it via TryLock and releases it
		// via `defer mu.Unlock()`, so a held mutex here means a leak.
		for _, p := range plans {
			muAny, ok := p.worker.perAppMu.Load(p.app)
			if !ok {
				rt.Fatalf("expected per-app mutex entry for %q after TriggerApp", p.app)
			}
			mu := muAny.(*sync.Mutex)
			if !mu.TryLock() {
				rt.Fatalf("per-app mutex for %q still held after TriggerApp returned", p.app)
			}
			mu.Unlock()
		}
	})
}

// TestProperty_FeedEventsAreOrderedSubset exercises Property 2 from design.md:
// the events emitted for one attempt_id form an ordered subset of the
// canonical state machine pulling → recreating → verifying →
// (completed|rolled_back|failed). There is no backward transition (e.g.
// verifying → pulling). Pending events (skipped_cooldown / skipped_window)
// are out of scope here because they don't carry an attempt_id and they
// don't apply to the bypass-flagged trigger calls used below.
//
// **Validates: Property 2 (Requirements R3.1-R3.5, R4.4).**
func TestProperty_FeedEventsAreOrderedSubset(t *testing.T) {
	// Canonical stage ordering. Terminal stages all share index 4 because
	// the pipeline can finish with any of them and they never precede an
	// earlier stage in a valid sequence.
	stagesOrder := map[string]int{
		string(db.StagePulling):    1,
		string(db.StageRecreating): 2,
		string(db.StageVerifying):  3,
		string(db.StageCompleted):  4,
		string(db.StageFailed):     4,
		string(db.StageRolledBack): 4,
	}

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(rt, "numApps")

		store := newFakeStore()
		monitor := propMonitor()
		var feedMu sync.Mutex
		var feedEvents []AppUpdateFeedPayload

		for i := 0; i < n; i++ {
			scenario := rapid.SampledFrom(propAutoUpdateScenarios).Draw(rt, "scenario")
			app := fmt.Sprintf("rapid-app-%d", i)
			fc := propClientForScenario(app, scenario)
			w := propBuildWorker(fc, store, monitor, propComposeYAML(app), &feedEvents, &feedMu)
			if err := w.TriggerApp(context.Background(), app, true, true, "manual"); err != nil {
				rt.Fatalf("TriggerApp(%s) returned error: %v", app, err)
			}
		}

		// Snapshot the captured events under the lock to avoid a race with any
		// async publish (the pipeline is sequential here, but defensive copy
		// is cheap and protects against future changes).
		feedMu.Lock()
		events := append([]AppUpdateFeedPayload(nil), feedEvents...)
		feedMu.Unlock()

		// Group events by attempt_id, skipping pending events that never
		// carry an attempt_id (skipped_cooldown / skipped_window). With
		// bypass flags set we shouldn't see any pending events, but the
		// filter keeps this test robust against future additions.
		byAttempt := make(map[string][]AppUpdateFeedPayload)
		for _, ev := range events {
			if ev.Stage == string(db.StagePending) {
				continue
			}
			if ev.AttemptID == "" {
				continue
			}
			byAttempt[ev.AttemptID] = append(byAttempt[ev.AttemptID], ev)
		}

		if len(byAttempt) == 0 {
			rt.Fatalf("expected at least one attempt with events, got none")
		}

		for attemptID, seq := range byAttempt {
			if len(seq) == 0 {
				continue
			}
			// Every attempt must begin with `pulling` per the design's
			// state machine — appendEvent is invoked with StagePulling as
			// the first stage in TriggerApp before any I/O can fail.
			if seq[0].Stage != string(db.StagePulling) {
				rt.Fatalf("attempt %s: first event must be %q, got %q",
					attemptID, db.StagePulling, seq[0].Stage)
			}

			prev := 0
			for j, ev := range seq {
				cur, known := stagesOrder[ev.Stage]
				if !known {
					rt.Fatalf("attempt %s: event %d has unknown stage %q",
						attemptID, j, ev.Stage)
				}
				if cur < prev {
					rt.Fatalf("attempt %s: event %d stage %q (order %d) precedes previous order %d",
						attemptID, j, ev.Stage, cur, prev)
				}
				prev = cur
			}
		}
	})
}
