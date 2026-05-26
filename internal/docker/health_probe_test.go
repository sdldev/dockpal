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

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// fakeProbeClient implements containerProbeClient with canned list +
// scripted inspect responses. Each call to ContainerInspect for a given
// container ID returns the next entry from inspectScripts[id]; once the
// script is exhausted the last entry is returned indefinitely.
type fakeProbeClient struct {
	listResult client.ContainerListResult
	listErr    error

	mu             sync.Mutex
	inspectScripts map[string][]client.ContainerInspectResult
	inspectErrs    map[string]error
	callIdx        map[string]int
	inspectCount   atomic.Int64
}

func (f *fakeProbeClient) ContainerList(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
	return f.listResult, f.listErr
}

func (f *fakeProbeClient) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCount.Add(1)
	if err, ok := f.inspectErrs[id]; ok {
		return client.ContainerInspectResult{}, err
	}
	seq := f.inspectScripts[id]
	if len(seq) == 0 {
		return client.ContainerInspectResult{}, fmt.Errorf("fakeProbeClient: no script for container %q", id)
	}
	if f.callIdx == nil {
		f.callIdx = make(map[string]int)
	}
	i := f.callIdx[id]
	if i >= len(seq) {
		i = len(seq) - 1
	} else {
		f.callIdx[id] = i + 1
	}
	return seq[i], nil
}

// summary returns a container.Summary suitable for ContainerListResult.Items.
// The container name is wrapped in a leading "/" to mimic the docker API.
func summary(id, name string) container.Summary {
	return container.Summary{
		ID:    id,
		Names: []string{"/" + name},
		State: container.StateRunning,
	}
}

func inspectRunningNoHealth(id, name string) client.ContainerInspectResult {
	return client.ContainerInspectResult{
		Container: container.InspectResponse{
			ID:   id,
			Name: "/" + name,
			State: &container.State{
				Status:  container.StateRunning,
				Running: true,
			},
		},
	}
}

func inspectRunningWithHealth(id, name string, status container.HealthStatus) client.ContainerInspectResult {
	return client.ContainerInspectResult{
		Container: container.InspectResponse{
			ID:   id,
			Name: "/" + name,
			State: &container.State{
				Status:  container.StateRunning,
				Running: true,
				Health:  &container.Health{Status: status},
			},
		},
	}
}

func inspectCreated(id, name string) client.ContainerInspectResult {
	return client.ContainerInspectResult{
		Container: container.InspectResponse{
			ID:   id,
			Name: "/" + name,
			State: &container.State{
				Status: container.StateCreated,
			},
		},
	}
}

func inspectExited(id, name string, code int) client.ContainerInspectResult {
	return client.ContainerInspectResult{
		Container: container.InspectResponse{
			ID:   id,
			Name: "/" + name,
			State: &container.State{
				Status:     container.StateExited,
				ExitCode:   code,
				Running:    false,
				Restarting: false,
			},
		},
	}
}

// findResult returns the HealthProbeResult whose Name matches `name`, or
// fails the test if no match is found.
func findResult(t *testing.T, results []HealthProbeResult, name string) HealthProbeResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no result for container %q in %+v", name, results)
	return HealthProbeResult{}
}

// TestHealthProbe_ProjectNameRequired ensures the function rejects an empty
// project name before reaching out to the docker daemon.
func TestHealthProbe_ProjectNameRequired(t *testing.T) {
	fake := &fakeProbeClient{}
	_, err := healthProbe(context.Background(), fake, "", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "project name is required") {
		t.Fatalf("expected project name required error, got %v", err)
	}
}

// TestHealthProbe_EmptyProject covers the "no containers found" branch when
// the label filter matches no containers.
func TestHealthProbe_EmptyProject(t *testing.T) {
	fake := &fakeProbeClient{
		listResult: client.ContainerListResult{Items: nil},
	}
	_, err := healthProbe(context.Background(), fake, "demo", 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected error on empty container list")
	}
	if !strings.Contains(err.Error(), `no containers found for project "demo"`) {
		t.Fatalf("expected 'no containers found for project' error, got %v", err)
	}
}

// TestHealthProbe_AllRunningNoHealthcheck covers the happy path where
// every container is in state "running" and no HEALTHCHECK is configured.
func TestHealthProbe_AllRunningNoHealthcheck(t *testing.T) {
	fake := &fakeProbeClient{
		listResult: client.ContainerListResult{
			Items: []container.Summary{
				summary("c1", "demo-app-1"),
				summary("c2", "demo-worker-1"),
			},
		},
		inspectScripts: map[string][]client.ContainerInspectResult{
			"c1": {inspectRunningNoHealth("c1", "demo-app-1")},
			"c2": {inspectRunningNoHealth("c2", "demo-worker-1")},
		},
	}

	results, err := healthProbe(context.Background(), fake, "demo", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d (%+v)", len(results), results)
	}
	for _, r := range results {
		if !r.Healthy {
			t.Errorf("expected %s healthy, got %+v", r.Name, r)
		}
		if r.State != string(container.StateRunning) {
			t.Errorf("%s: expected state running, got %q", r.Name, r.State)
		}
	}
}

// TestHealthProbe_AllHealthcheckReportsHealthy covers running containers
// whose HEALTHCHECK explicitly reports healthy.
func TestHealthProbe_AllHealthcheckReportsHealthy(t *testing.T) {
	fake := &fakeProbeClient{
		listResult: client.ContainerListResult{
			Items: []container.Summary{summary("c1", "api-1")},
		},
		inspectScripts: map[string][]client.ContainerInspectResult{
			"c1": {inspectRunningWithHealth("c1", "api-1", container.Healthy)},
		},
	}
	results, err := healthProbe(context.Background(), fake, "demo", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !results[0].Healthy {
		t.Fatalf("expected healthy, got %+v", results[0])
	}
}

// TestHealthProbe_HealthcheckUnhealthy covers a running container whose
// HEALTHCHECK reports unhealthy. The aggregate error should reference it.
func TestHealthProbe_HealthcheckUnhealthy(t *testing.T) {
	fake := &fakeProbeClient{
		listResult: client.ContainerListResult{
			Items: []container.Summary{summary("c1", "api-1")},
		},
		inspectScripts: map[string][]client.ContainerInspectResult{
			"c1": {inspectRunningWithHealth("c1", "api-1", container.Unhealthy)},
		},
	}
	results, err := healthProbe(context.Background(), fake, "demo", 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected error from unhealthy container, got nil")
	}
	if !strings.Contains(err.Error(), "healthcheck reported unhealthy") {
		t.Fatalf("expected unhealthy reason in error, got %v", err)
	}
	if results[0].Healthy {
		t.Fatalf("expected unhealthy result, got %+v", results[0])
	}
}

// TestHealthProbe_ContainerExitsNonZeroFailsFast verifies the fail-fast
// path: a container with State.ExitCode != 0 and !State.Restarting must be
// marked unhealthy on the first inspect, without any further polling.
func TestHealthProbe_ContainerExitsNonZeroFailsFast(t *testing.T) {
	fake := &fakeProbeClient{
		listResult: client.ContainerListResult{
			Items: []container.Summary{
				summary("c1", "api-1"),
				summary("c2", "worker-1"),
			},
		},
		inspectScripts: map[string][]client.ContainerInspectResult{
			"c1": {inspectExited("c1", "api-1", 137)},
			"c2": {inspectRunningNoHealth("c2", "worker-1")},
		},
	}

	start := time.Now()
	// Use a 5s grace so a slow path would be obviously detectable.
	results, err := healthProbe(context.Background(), fake, "demo", 5*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected aggregate error from non-zero exit, got nil")
	}
	// Fail-fast invariant: returned well below the grace.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected fail-fast (no polling), but probe took %s", elapsed)
	}
	if !strings.Contains(err.Error(), "api-1") {
		t.Fatalf("expected error to reference exited container 'api-1', got %v", err)
	}
	if !strings.Contains(err.Error(), "exited with code 137") {
		t.Fatalf("expected error to mention exit code 137, got %v", err)
	}

	api := findResult(t, results, "api-1")
	if api.Healthy {
		t.Fatalf("expected api-1 unhealthy, got %+v", api)
	}
	if api.ExitCode != 137 {
		t.Errorf("expected ExitCode 137 captured on result, got %d", api.ExitCode)
	}
	if api.State != string(container.StateExited) {
		t.Errorf("expected state %q, got %q", container.StateExited, api.State)
	}

	worker := findResult(t, results, "worker-1")
	if !worker.Healthy {
		t.Errorf("expected worker-1 healthy, got %+v", worker)
	}

	// Each container should have been inspected exactly once (no polling).
	if got := fake.inspectCount.Load(); got != 2 {
		t.Errorf("expected exactly 2 inspect calls, got %d", got)
	}
}

// TestHealthProbe_EventuallyRunning verifies that a container which is in
// state "created" on the first poll and "running" on the second is
// reported healthy.
func TestHealthProbe_EventuallyRunning(t *testing.T) {
	fake := &fakeProbeClient{
		listResult: client.ContainerListResult{
			Items: []container.Summary{summary("c1", "api-1")},
		},
		inspectScripts: map[string][]client.ContainerInspectResult{
			"c1": {
				inspectCreated("c1", "api-1"),
				inspectRunningNoHealth("c1", "api-1"),
			},
		},
	}

	// Grace large enough that the implementation polls a second time:
	// healthProbe sleeps min(pollInterval, remainingGrace) between polls.
	results, err := healthProbe(context.Background(), fake, "demo", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("expected no error after container becomes running, got %v", err)
	}
	if !results[0].Healthy {
		t.Fatalf("expected healthy after second poll, got %+v", results[0])
	}
	if got := fake.inspectCount.Load(); got < 2 {
		t.Errorf("expected at least 2 inspect calls (created → running), got %d", got)
	}
}

// TestHealthProbe_TimeoutContainerNeverRuns verifies the deadline path:
// a container stuck in "created" for the entire grace must be reported as
// unhealthy with the deadline duration referenced in the aggregate error.
func TestHealthProbe_TimeoutContainerNeverRuns(t *testing.T) {
	fake := &fakeProbeClient{
		listResult: client.ContainerListResult{
			Items: []container.Summary{summary("c1", "api-1")},
		},
		inspectScripts: map[string][]client.ContainerInspectResult{
			"c1": {inspectCreated("c1", "api-1")},
		},
	}

	grace := 50 * time.Millisecond
	start := time.Now()
	results, err := healthProbe(context.Background(), fake, "demo", grace)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if elapsed < grace {
		t.Fatalf("expected probe to wait at least %s, got %s", grace, elapsed)
	}
	if !strings.Contains(err.Error(), grace.String()) {
		t.Fatalf("expected aggregate error to mention grace %s, got %v", grace, err)
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Fatalf("expected timeout reason in error, got %v", err)
	}
	if results[0].Healthy {
		t.Fatalf("expected unhealthy after timeout, got %+v", results[0])
	}
	if results[0].State != string(container.StateCreated) {
		t.Errorf("expected last seen state %q, got %q", container.StateCreated, results[0].State)
	}
}

// TestHealthProbe_ContextCancellation verifies that cancelling the context
// while healthProbe is sleeping in its select returns an error that wraps
// context.Canceled and finalizes pending entries as unhealthy.
func TestHealthProbe_ContextCancellation(t *testing.T) {
	fake := &fakeProbeClient{
		listResult: client.ContainerListResult{
			Items: []container.Summary{summary("c1", "api-1")},
		},
		inspectScripts: map[string][]client.ContainerInspectResult{
			"c1": {inspectCreated("c1", "api-1")},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after the first poll has executed so the goroutine is parked
	// in time.After. Use a long grace so the cancel path wins over the
	// deadline path.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	results, err := healthProbe(ctx, fake, "demo", 5*time.Second)
	if err == nil {
		t.Fatalf("expected error after ctx cancellation, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected joined error to wrap context.Canceled, got %v", err)
	}
	if results[0].Healthy {
		t.Fatalf("expected unhealthy after cancellation, got %+v", results[0])
	}
}

// TestHealthProbe_ListError surfaces ContainerList errors verbatim wrapped
// in the standard "list project containers" prefix.
func TestHealthProbe_ListError(t *testing.T) {
	wantErr := errors.New("docker daemon unreachable")
	fake := &fakeProbeClient{listErr: wantErr}
	_, err := healthProbe(context.Background(), fake, "demo", 50*time.Millisecond)
	if err == nil {
		t.Fatalf("expected error from listing, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped %v, got %v", wantErr, err)
	}
	if !strings.Contains(err.Error(), "list project containers") {
		t.Fatalf("expected wrap prefix in error, got %v", err)
	}
}
