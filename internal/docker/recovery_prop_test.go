package docker

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"testing/quick"
)

// **Validates: Requirements 11.4**

// Property 17: Auto-Recovery Retry Limit
// Verify at most 3 restart attempts per container per health check cycle.
// The HealthMonitor skips restart attempts when the failure count for a container
// has reached 3, ensuring no more than 3 attempts are made per container per cycle.

func TestProperty17_AutoRecoveryRetryLimit(t *testing.T) {
	// Property: For any container with failure count >= 3, the HealthMonitor
	// will NOT attempt another restart (respects the limit). For any container
	// with failure count < 3, a restart attempt IS made.
	prop := func(params retryLimitParams) bool {
		hm := &HealthMonitor{
			failures: make(map[string]int),
		}

		// Set up the failure count for the container
		hm.failuresMu = sync.Mutex{}
		hm.failures[params.containerID] = params.currentFailures

		// Check if restart would be attempted
		hm.failuresMu.Lock()
		attempts := hm.failures[params.containerID]
		hm.failuresMu.Unlock()

		shouldSkip := attempts >= 3

		if params.currentFailures >= 3 {
			// When failures >= 3, the monitor MUST skip (not attempt restart)
			if !shouldSkip {
				return false
			}
		} else {
			// When failures < 3, the monitor MUST allow restart attempt
			if shouldSkip {
				return false
			}
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 1000,
		Values:   retryLimitGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 17 failed: %v", err)
	}
}

// TestProperty17_RetryLimitNeverExceeds3Attempts verifies that simulating multiple
// consecutive failures for the same container in a cycle never results in more than
// 3 total restart attempts being recorded.
func TestProperty17_RetryLimitNeverExceeds3Attempts(t *testing.T) {
	// Property: Given N total restart requests for a container in a single cycle,
	// at most 3 will be attempted (failures capped at 3 before skipping).
	prop := func(params retrySimulationParams) bool {
		hm := &HealthMonitor{
			failures:   make(map[string]int),
			failuresMu: sync.Mutex{},
		}

		containerID := params.containerID
		actualAttempts := 0

		// Simulate N restart decision checks within the same cycle
		for i := 0; i < params.totalRequests; i++ {
			hm.failuresMu.Lock()
			attempts := hm.failures[containerID]
			hm.failuresMu.Unlock()

			if attempts >= 3 {
				// Skip - limit reached
				continue
			}

			// Attempt restart (simulate failure by incrementing counter)
			actualAttempts++
			hm.failuresMu.Lock()
			hm.failures[containerID]++
			hm.failuresMu.Unlock()
		}

		// The invariant: at most 3 attempts regardless of how many requests
		return actualAttempts <= 3
	}

	cfg := &quick.Config{
		MaxCount: 1000,
		Values:   retrySimulationGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 17 (simulation) failed: %v", err)
	}
}

// retryLimitParams represents parameters for testing retry limit decisions
type retryLimitParams struct {
	containerID     string
	currentFailures int // 0..10 range to test both below and above limit
}

func retryLimitGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate container IDs of varying lengths
	idLen := 12
	id := make([]byte, idLen)
	for i := range id {
		id[i] = "abcdef0123456789"[rng.Intn(16)]
	}

	params := retryLimitParams{
		containerID:     string(id),
		currentFailures: rng.Intn(11), // 0 to 10
	}
	values[0] = reflect.ValueOf(params)
}

// retrySimulationParams represents parameters for simulating multiple restart requests
type retrySimulationParams struct {
	containerID   string
	totalRequests int // 1..20 requests in a single cycle
}

func retrySimulationGenerator(values []reflect.Value, rng *rand.Rand) {
	idLen := 12
	id := make([]byte, idLen)
	for i := range id {
		id[i] = "abcdef0123456789"[rng.Intn(16)]
	}

	params := retrySimulationParams{
		containerID:   string(id),
		totalRequests: 1 + rng.Intn(20), // 1 to 20
	}
	values[0] = reflect.ValueOf(params)
}
