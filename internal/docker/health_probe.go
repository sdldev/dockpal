package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// healthProbePollInterval is the cadence of HealthProbe inspect polls.
// It is unexported so tests using a small `grace` rely on fail-fast detection
// instead of waiting for the full polling cadence.
const healthProbePollInterval = 2 * time.Second

// HealthProbeResult describes one container's outcome after a redeploy.
type HealthProbeResult struct {
	ContainerID string `json:"container_id"`
	Name        string `json:"name"`
	Healthy     bool   `json:"healthy"`
	State       string `json:"state"`
	ExitCode    int    `json:"exit_code"`
	Reason      string `json:"reason,omitempty"`
}

// containerProbeClient is the subset of the moby client used by HealthProbe.
// Defining it as an interface keeps HealthProbe testable: the unit tests for
// task 2.4 can substitute a fake that returns scripted inspect results
// without spinning up a real Docker daemon.
type containerProbeClient interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(ctx context.Context, containerID string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
}

// probeEntry tracks the state of a single container while HealthProbe polls.
type probeEntry struct {
	result  HealthProbeResult
	decided bool
}

// HealthProbe waits up to `grace` for every container of `project` to reach
// state "running" and (when a HEALTHCHECK is defined) Health.Status == healthy.
// It polls every healthProbePollInterval. A container with State.ExitCode != 0
// && !State.Restarting is considered a hard failure and is marked unhealthy
// without further polling. The function returns one HealthProbeResult per
// container, plus a non-nil aggregate error when any container is unhealthy at
// the time the function returns.
func (c *Client) HealthProbe(ctx context.Context, project string, grace time.Duration) ([]HealthProbeResult, error) {
	return healthProbe(ctx, c.cli, project, grace)
}

// healthProbe is the implementation behind (*Client).HealthProbe. It is
// parameterised on containerProbeClient so unit tests can drive scripted
// inspect responses without a real Docker daemon.
func healthProbe(ctx context.Context, cli containerProbeClient, project string, grace time.Duration) ([]HealthProbeResult, error) {
	if project == "" {
		return nil, fmt.Errorf("project name is required")
	}

	filters := make(client.Filters)
	filters = filters.Add("label", fmt.Sprintf("dockpal.project=%s", project))
	listed, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("list project containers: %w", err)
	}
	if len(listed.Items) == 0 {
		return nil, fmt.Errorf("no containers found for project %q", project)
	}

	entries := make([]*probeEntry, len(listed.Items))
	for i, ctr := range listed.Items {
		name := ""
		if len(ctr.Names) > 0 {
			name = trimContainerName(ctr.Names[0])
		}
		entries[i] = &probeEntry{
			result: HealthProbeResult{
				ContainerID: ctr.ID,
				Name:        name,
				State:       string(ctr.State),
			},
		}
	}

	deadline := time.Now().Add(grace)

	for {
		allDone := true
		for _, e := range entries {
			if e.decided {
				continue
			}
			if !evaluateEntry(ctx, cli, e) {
				allDone = false
			}
		}

		if allDone {
			return collectResults(entries), aggregateUnhealthyError(entries)
		}

		// Honour the grace deadline before sleeping again.
		now := time.Now()
		if !now.Before(deadline) {
			finalizePending(entries, fmt.Sprintf("did not become healthy within %s", grace))
			return collectResults(entries), aggregateUnhealthyError(entries)
		}

		// Sleep for the polling interval, but no longer than the remaining grace.
		wait := healthProbePollInterval
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}

		select {
		case <-ctx.Done():
			finalizePending(entries, fmt.Sprintf("context cancelled: %v", ctx.Err()))
			return collectResults(entries), errors.Join(ctx.Err(), aggregateUnhealthyError(entries))
		case <-time.After(wait):
		}
	}
}

// evaluateEntry inspects one container and updates the probe entry. It
// returns true when a final decision was reached for the entry (healthy or
// terminal failure) and false when the caller should keep polling.
func evaluateEntry(ctx context.Context, cli containerProbeClient, e *probeEntry) bool {
	inspect, ierr := cli.ContainerInspect(ctx, e.result.ContainerID, client.ContainerInspectOptions{})
	if ierr != nil {
		e.result.Healthy = false
		e.result.Reason = fmt.Sprintf("inspect failed: %v", ierr)
		e.decided = true
		return true
	}

	ctr := inspect.Container
	if ctr.Name != "" {
		e.result.Name = trimContainerName(ctr.Name)
	}
	if ctr.State == nil {
		return false // State unavailable yet; keep polling.
	}

	e.result.State = string(ctr.State.Status)
	e.result.ExitCode = ctr.State.ExitCode

	// Fail fast: non-zero exit code and not restarting is terminal.
	if ctr.State.ExitCode != 0 && !ctr.State.Restarting {
		e.result.Healthy = false
		e.result.Reason = fmt.Sprintf("exited with code %d (state %s)", ctr.State.ExitCode, ctr.State.Status)
		e.decided = true
		return true
	}

	if !isRunning(ctr.State.Status) {
		// Still creating, restarting, or otherwise transitional.
		return false
	}

	// Running. If a HEALTHCHECK is configured, defer to it.
	if ctr.State.Health != nil {
		switch ctr.State.Health.Status {
		case container.Healthy:
			e.result.Healthy = true
			e.decided = true
			return true
		case container.Unhealthy:
			e.result.Healthy = false
			e.result.Reason = "healthcheck reported unhealthy"
			e.decided = true
			return true
		default:
			// container.Starting or any unknown transitional value.
			return false
		}
	}

	// No HEALTHCHECK declared: running is sufficient.
	e.result.Healthy = true
	e.decided = true
	return true
}

// isRunning compares a ContainerState to the running constant. Status values
// are already lowercase, but ToLower keeps the check robust against future
// changes upstream.
func isRunning(s container.ContainerState) bool {
	return strings.ToLower(string(s)) == string(container.StateRunning)
}

// finalizePending marks every undecided entry unhealthy with the provided
// reason. Used when the grace deadline elapses or ctx is cancelled.
func finalizePending(entries []*probeEntry, reason string) {
	for _, e := range entries {
		if e.decided {
			continue
		}
		e.result.Healthy = false
		if e.result.Reason == "" {
			e.result.Reason = fmt.Sprintf("%s (state %s)", reason, e.result.State)
		}
		e.decided = true
	}
}

// collectResults extracts the HealthProbeResult slice from the internal
// entries.
func collectResults(entries []*probeEntry) []HealthProbeResult {
	out := make([]HealthProbeResult, len(entries))
	for i, e := range entries {
		out[i] = e.result
	}
	return out
}

// aggregateUnhealthyError returns a joined error listing every unhealthy
// container, or nil when all containers are healthy.
func aggregateUnhealthyError(entries []*probeEntry) error {
	var errs []error
	for _, e := range entries {
		if e.result.Healthy {
			continue
		}
		name := e.result.Name
		if name == "" {
			name = e.result.ContainerID
		}
		reason := e.result.Reason
		if reason == "" {
			reason = "unhealthy"
		}
		errs = append(errs, fmt.Errorf("%s: %s", name, reason))
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
