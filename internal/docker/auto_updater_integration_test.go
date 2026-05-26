//go:build integration

package docker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// =============================================================================
// Integration test for the AutoUpdateWorker end-to-end pipeline.
//
// This file is gated behind the `integration` build tag so it does not run
// in normal CI. It exercises the full TriggerApp flow:
//   detect update → pull → recreate → verify health → completed
//
// Because a real Docker daemon may not be available in all environments, the
// test uses the same mock patterns from auto_updater_test.go but wires them
// into a more realistic multi-service scenario that validates the atomic
// recreate behavior described in R2.3 and the stage transitions in R2.6.
// =============================================================================

// integrationClient extends fakeAutoUpdaterClient with richer tracking for
// the integration scenario: it records per-service pull order, tracks
// container state transitions (simulating recreate), and validates that
// DeployCompose is called exactly once for the entire project.
type integrationClient struct {
	mu sync.Mutex

	// containers simulates the running containers grouped by label filter.
	containers map[string][]ContainerInfo

	// pullOrder records the sequence of images pulled.
	pullOrder []string

	// deployCalls records each DeployCompose invocation.
	deployCalls []integrationDeployCall

	// inspectDigests maps image → previous digest (before update).
	inspectDigests map[string]string

	// postDeployContainers is the container state after DeployCompose.
	// Used by HealthProbe to simulate the new container state.
	postDeployContainers []ContainerInfo

	// healthResult controls what HealthProbe returns.
	healthResults []HealthProbeResult
	healthErr     error
}

type integrationDeployCall struct {
	project     string
	composeYAML string
	forcePull   bool
}

func (c *integrationClient) ListContainersWithLabel(_ context.Context, label string) ([]ContainerInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.containers == nil {
		return nil, nil
	}
	src := c.containers[label]
	out := make([]ContainerInfo, len(src))
	copy(out, src)
	return out, nil
}

func (c *integrationClient) ForcePullImage(_ context.Context, image, _ string) error {
	c.mu.Lock()
	c.pullOrder = append(c.pullOrder, image)
	c.mu.Unlock()
	return nil
}

func (c *integrationClient) DeployCompose(_ context.Context, project, composeYAML string, _ AuthHeaderFunc, forcePull bool) error {
	c.mu.Lock()
	c.deployCalls = append(c.deployCalls, integrationDeployCall{
		project:     project,
		composeYAML: composeYAML,
		forcePull:   forcePull,
	})
	c.mu.Unlock()
	return nil
}

func (c *integrationClient) HealthProbe(_ context.Context, _ string, _ time.Duration) ([]HealthProbeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.healthErr != nil {
		return nil, c.healthErr
	}
	return c.healthResults, nil
}

func (c *integrationClient) InspectRepoDigest(_ context.Context, image string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inspectDigests == nil {
		return "", nil
	}
	return c.inspectDigests[image], nil
}

// =============================================================================
// TestIntegration_TriggerApp_MultiServiceAtomicRecreate exercises the full
// end-to-end pipeline for a compose project with multiple services where
// several have has_update=true. It validates:
//   - All changed images are pulled (R2.3)
//   - DeployCompose is called exactly once with forcePull=true (R2.3)
//   - The record transitions through pulling → recreating → verifying →
//     completed (R2.6)
//   - Feed events are emitted for each stage transition
// =============================================================================

func TestIntegration_TriggerApp_MultiServiceAtomicRecreate(t *testing.T) {
	const app = "nginx-stack"

	// Compose YAML with two services: web (nginx) and cache (redis).
	// Both have the auto-update label.
	composeYAML := `services:
  web:
    image: nginx:1.25
    labels:
      dockpal.project: nginx-stack
      dockpal.service: web
      dockpal.auto-update: "true"
  cache:
    image: redis:7.2
    labels:
      dockpal.project: nginx-stack
      dockpal.service: cache
      dockpal.auto-update: "true"
`

	// Build containers that simulate the running state.
	webContainer := ContainerInfo{
		ID:    "ctr-nginx-stack-web",
		Name:  "nginx-stack-web",
		Image: "nginx:1.25",
		State: "running",
		Labels: map[string]string{
			"dockpal.project":     app,
			"dockpal.service":     "web",
			"dockpal.auto-update": "true",
		},
	}
	cacheContainer := ContainerInfo{
		ID:    "ctr-nginx-stack-cache",
		Name:  "nginx-stack-cache",
		Image: "redis:7.2",
		State: "running",
		Labels: map[string]string{
			"dockpal.project":     app,
			"dockpal.service":     "cache",
			"dockpal.auto-update": "true",
		},
	}

	allContainers := []ContainerInfo{webContainer, cacheContainer}

	// Set up the integration client with containers and previous digests.
	ic := &integrationClient{
		containers: map[string][]ContainerInfo{
			"dockpal.auto-update=true":   allContainers,
			"dockpal.project=" + app:     allContainers,
		},
		inspectDigests: map[string]string{
			"nginx:1.25": "sha256:oldnginxdigest1234",
			"redis:7.2":  "sha256:oldredisdigest5678",
		},
		healthResults: []HealthProbeResult{
			{ContainerID: "ctr-nginx-stack-web-new", Name: "nginx-stack-web", Healthy: true, State: "running"},
			{ContainerID: "ctr-nginx-stack-cache-new", Name: "nginx-stack-cache", Healthy: true, State: "running"},
		},
	}

	// Mock the ImageUpdateMonitor cache to report has_update=true for both
	// images. This simulates the registry check having detected new digests.
	monitor := &ImageUpdateMonitor{
		cache: map[string]*ImageUpdateStatus{
			"nginx:1.25": {
				ImageRef: "nginx:1.25",
				Result: &ImageUpdateResult{
					HasUpdate:    true,
					LocalDigest:  "sha256:oldnginxdigest1234",
					RemoteDigest: "sha256:newnginxdigest9999",
				},
			},
			"redis:7.2": {
				ImageRef: "redis:7.2",
				Result: &ImageUpdateResult{
					HasUpdate:    true,
					LocalDigest:  "sha256:oldredisdigest5678",
					RemoteDigest: "sha256:newredisdigest8888",
				},
			},
		},
	}

	// Set up the store and feed capture.
	store := newFakeStore()
	var feedMu sync.Mutex
	var feedEvents []AppUpdateFeedPayload
	feed := func(ev AppUpdateFeedPayload) {
		feedMu.Lock()
		feedEvents = append(feedEvents, ev)
		feedMu.Unlock()
	}

	// Build the worker with the integration client and monitor.
	w := &AutoUpdateWorker{
		client:      ic,
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
		instanceID:  "integration-test",
	}

	// Execute the pipeline via TriggerApp (manual trigger, bypass cooldown
	// and window).
	ctx := context.Background()
	err := w.TriggerApp(ctx, app, true, true, "integration-test")
	if err != nil {
		t.Fatalf("TriggerApp failed: %v", err)
	}

	// --- Assertions ---

	// 1. Both images must have been pulled (R2.3: pull all changed images).
	ic.mu.Lock()
	pulledImages := make([]string, len(ic.pullOrder))
	copy(pulledImages, ic.pullOrder)
	ic.mu.Unlock()

	if len(pulledImages) != 2 {
		t.Fatalf("expected 2 images pulled, got %d: %v", len(pulledImages), pulledImages)
	}
	pulledSet := map[string]bool{}
	for _, img := range pulledImages {
		pulledSet[img] = true
	}
	if !pulledSet["nginx:1.25"] {
		t.Errorf("expected nginx:1.25 to be pulled, pulled: %v", pulledImages)
	}
	if !pulledSet["redis:7.2"] {
		t.Errorf("expected redis:7.2 to be pulled, pulled: %v", pulledImages)
	}

	// 2. DeployCompose called exactly once with forcePull=true (R2.3: atomic).
	ic.mu.Lock()
	deploys := make([]integrationDeployCall, len(ic.deployCalls))
	copy(deploys, ic.deployCalls)
	ic.mu.Unlock()

	if len(deploys) != 1 {
		t.Fatalf("expected exactly 1 DeployCompose call (atomic recreate), got %d", len(deploys))
	}
	if deploys[0].project != app {
		t.Errorf("DeployCompose project = %q, want %q", deploys[0].project, app)
	}
	if !deploys[0].forcePull {
		t.Errorf("DeployCompose forcePull = false, want true")
	}

	// 3. Record transitions through the expected stages (R2.6).
	recs := store.recordsForApp(app)
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 AppUpdateRecord, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Stage != db.StageCompleted {
		t.Errorf("final stage = %q, want %q", rec.Stage, db.StageCompleted)
	}
	if rec.ErrorCode != "" {
		t.Errorf("error_code = %q, want empty on success", rec.ErrorCode)
	}
	if rec.TriggeredBy != "integration-test" {
		t.Errorf("triggered_by = %q, want %q", rec.TriggeredBy, "integration-test")
	}
	if rec.InstanceID != "integration-test" {
		t.Errorf("instance_id = %q, want %q", rec.InstanceID, "integration-test")
	}

	// Verify the record captured service info for both services.
	if len(rec.Services) != 2 {
		t.Errorf("expected 2 services in record, got %d", len(rec.Services))
	}
	if svc, ok := rec.Services["web"]; ok {
		if svc.Image != "nginx:1.25" {
			t.Errorf("web service image = %q, want %q", svc.Image, "nginx:1.25")
		}
		if svc.PreviousDigest != "sha256:oldnginxdigest1234" {
			t.Errorf("web previous_digest = %q, want %q", svc.PreviousDigest, "sha256:oldnginxdigest1234")
		}
		if svc.NewDigest != "sha256:newnginxdigest9999" {
			t.Errorf("web new_digest = %q, want %q", svc.NewDigest, "sha256:newnginxdigest9999")
		}
	} else {
		t.Errorf("expected 'web' service in record.Services")
	}

	if svc, ok := rec.Services["cache"]; ok {
		if svc.Image != "redis:7.2" {
			t.Errorf("cache service image = %q, want %q", svc.Image, "redis:7.2")
		}
		if svc.PreviousDigest != "sha256:oldredisdigest5678" {
			t.Errorf("cache previous_digest = %q, want %q", svc.PreviousDigest, "sha256:oldredisdigest5678")
		}
		if svc.NewDigest != "sha256:newredisdigest8888" {
			t.Errorf("cache new_digest = %q, want %q", svc.NewDigest, "sha256:newredisdigest8888")
		}
	} else {
		t.Errorf("expected 'cache' service in record.Services")
	}

	// 4. Verify stage transitions in the event log (R2.6).
	expectedStages := []db.AppUpdateStage{
		db.StagePulling,
		db.StageRecreating,
		db.StageVerifying,
		db.StageCompleted,
	}
	if len(rec.Events) < len(expectedStages) {
		t.Fatalf("expected at least %d events, got %d: %+v",
			len(expectedStages), len(rec.Events), rec.Events)
	}
	for i, want := range expectedStages {
		if rec.Events[i].Stage != want {
			t.Errorf("event[%d].Stage = %q, want %q", i, rec.Events[i].Stage, want)
		}
	}

	// 5. Verify feed events were emitted for each stage.
	feedMu.Lock()
	capturedFeed := make([]AppUpdateFeedPayload, len(feedEvents))
	copy(capturedFeed, feedEvents)
	feedMu.Unlock()

	if len(capturedFeed) < 4 {
		t.Fatalf("expected at least 4 feed events, got %d: %+v",
			len(capturedFeed), capturedFeed)
	}

	feedStages := make([]string, len(capturedFeed))
	for i, ev := range capturedFeed {
		feedStages[i] = ev.Stage
	}

	// The feed must contain pulling, recreating, verifying, completed in order.
	wantFeedStages := []string{
		string(db.StagePulling),
		string(db.StageRecreating),
		string(db.StageVerifying),
		string(db.StageCompleted),
	}
	idx := 0
	for _, fs := range feedStages {
		if idx < len(wantFeedStages) && fs == wantFeedStages[idx] {
			idx++
		}
	}
	if idx != len(wantFeedStages) {
		t.Errorf("feed stages do not contain expected ordered subset %v; got %v",
			wantFeedStages, feedStages)
	}

	// 6. Verify CompletedAt is set.
	if rec.CompletedAt == 0 {
		t.Errorf("CompletedAt should be non-zero on completed record")
	}
}

// =============================================================================
// TestIntegration_TriggerApp_HealthFailure_Rollback exercises the rollback
// path: after a successful pull and recreate, the health probe fails and the
// worker rolls back to the previous digest. Validates R2.6 stage transitions
// and R3.3 rollback behavior.
// =============================================================================

func TestIntegration_TriggerApp_HealthFailure_Rollback(t *testing.T) {
	const app = "nginx-app"

	composeYAML := `services:
  web:
    image: nginx:1.25
    labels:
      dockpal.project: nginx-app
      dockpal.service: web
      dockpal.auto-update: "true"
`

	webContainer := ContainerInfo{
		ID:    "ctr-nginx-app-web",
		Name:  "nginx-app-web",
		Image: "nginx:1.25",
		State: "running",
		Labels: map[string]string{
			"dockpal.project":     app,
			"dockpal.service":     "web",
			"dockpal.auto-update": "true",
		},
	}

	ic := &integrationClient{
		containers: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {webContainer},
			"dockpal.project=" + app:   {webContainer},
		},
		inspectDigests: map[string]string{
			"nginx:1.25": "sha256:previousdigest1234",
		},
		// Health probe fails: container exited with non-zero code.
		healthErr: fmt.Errorf("container nginx-app-web unhealthy: exit code 1"),
	}

	monitor := &ImageUpdateMonitor{
		cache: map[string]*ImageUpdateStatus{
			"nginx:1.25": {
				ImageRef: "nginx:1.25",
				Result: &ImageUpdateResult{
					HasUpdate:    true,
					LocalDigest:  "sha256:previousdigest1234",
					RemoteDigest: "sha256:newdigest5678",
				},
			},
		},
	}

	store := newFakeStore()
	var feedMu sync.Mutex
	var feedEvents []AppUpdateFeedPayload
	feed := func(ev AppUpdateFeedPayload) {
		feedMu.Lock()
		feedEvents = append(feedEvents, ev)
		feedMu.Unlock()
	}

	w := &AutoUpdateWorker{
		client:      ic,
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
		instanceID:  "integration-test",
	}

	ctx := context.Background()
	err := w.TriggerApp(ctx, app, true, true, "integration-test")
	if err != nil {
		t.Fatalf("TriggerApp failed: %v", err)
	}

	// --- Assertions ---

	// 1. Image was pulled.
	ic.mu.Lock()
	pullCount := len(ic.pullOrder)
	ic.mu.Unlock()
	if pullCount != 1 {
		t.Fatalf("expected 1 image pulled, got %d", pullCount)
	}

	// 2. DeployCompose called twice: once for recreate (forcePull=true),
	//    once for rollback (forcePull=false).
	ic.mu.Lock()
	deploys := make([]integrationDeployCall, len(ic.deployCalls))
	copy(deploys, ic.deployCalls)
	ic.mu.Unlock()

	if len(deploys) != 2 {
		t.Fatalf("expected 2 DeployCompose calls (recreate + rollback), got %d", len(deploys))
	}
	if !deploys[0].forcePull {
		t.Errorf("first deploy (recreate) forcePull = false, want true")
	}
	if deploys[1].forcePull {
		t.Errorf("second deploy (rollback) forcePull = true, want false")
	}

	// 3. Final record stage is rolled_back with health_probe_failed error.
	recs := store.recordsForApp(app)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Stage != db.StageRolledBack {
		t.Errorf("final stage = %q, want %q", rec.Stage, db.StageRolledBack)
	}
	if rec.ErrorCode != ErrHealthProbeFailed {
		t.Errorf("error_code = %q, want %q", rec.ErrorCode, ErrHealthProbeFailed)
	}

	// 4. Event log shows the full stage progression including rolled_back.
	expectedStages := []db.AppUpdateStage{
		db.StagePulling,
		db.StageRecreating,
		db.StageVerifying,
		db.StageRolledBack,
	}
	if len(rec.Events) < len(expectedStages) {
		t.Fatalf("expected at least %d events, got %d: %+v",
			len(expectedStages), len(rec.Events), rec.Events)
	}
	for i, want := range expectedStages {
		if rec.Events[i].Stage != want {
			t.Errorf("event[%d].Stage = %q, want %q", i, rec.Events[i].Stage, want)
		}
	}

	// 5. Feed events include rolled_back.
	feedMu.Lock()
	capturedFeed := make([]AppUpdateFeedPayload, len(feedEvents))
	copy(capturedFeed, feedEvents)
	feedMu.Unlock()

	sawRolledBack := false
	for _, ev := range capturedFeed {
		if ev.Stage == string(db.StageRolledBack) {
			sawRolledBack = true
			if ev.ErrorCode != ErrHealthProbeFailed {
				t.Errorf("rolled_back feed event error_code = %q, want %q",
					ev.ErrorCode, ErrHealthProbeFailed)
			}
			break
		}
	}
	if !sawRolledBack {
		t.Errorf("expected a feed event with stage=rolled_back, got: %+v", capturedFeed)
	}
}
