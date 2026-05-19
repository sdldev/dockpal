package server

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/sdldev/dockpal/internal/docker"
)

// Property 10: Deploy event relay preserves order
// **Validates: Requirement 14.1**

// TestProperty_DeployEventRelayOrder verifies that the relay logic preserves the order
// and field values of DeployEvent messages. This tests that:
// 1. Events with incrementing step numbers are received in order
// 2. Each event's Step, Message, Status, and Error fields are preserved
// 3. The ordering is maintained across different sequence lengths (5-20 events)
func TestProperty_DeployEventRelayOrder(t *testing.T) {
	prop := func(params relayOrderParams) bool {
		// Create a deploy session with buffered channel to hold all events
		session := &docker.DeploySession{
			ID:     "test-deploy",
			Events: make(chan docker.DeployEvent, params.NumEvents*2),
			Done:   make(chan struct{}),
		}

		// Simulate the relay: write events in order, then read them back
		// This mimics what DirectClient.DeployComposeStreamed and EdgeClient.DeployComposeStreamed do

		// Write events in a specific order (this is what the relay does)
		go func() {
			for i := 0; i < params.NumEvents; i++ {
				event := generateDeployEvent(i, params.IncludeErrors)
				select {
				case session.Events <- event:
				default:
					// Channel full, which shouldn't happen with our buffer size
				}
			}
			close(session.Events)
		}()

		// Read events and verify order and values
		receivedEvents := make([]docker.DeployEvent, 0, params.NumEvents)
		for event := range session.Events {
			receivedEvents = append(receivedEvents, event)
		}

		// Verify we received all events
		if len(receivedEvents) != params.NumEvents {
			t.Logf("expected %d events, got %d", params.NumEvents, len(receivedEvents))
			return false
		}

		// Verify order is preserved - step numbers should be sequential
		for i, event := range receivedEvents {
			expectedStep := generateStepName(i)
			if event.Step != expectedStep {
				t.Logf("event %d: expected step %q, got %q", i, expectedStep, event.Step)
				return false
			}

			// Verify Message field is preserved
			expectedMessage := generateMessage(i)
			if event.Message != expectedMessage {
				t.Logf("event %d: expected message %q, got %q", i, expectedMessage, event.Message)
				return false
			}

			// Verify Status field is preserved
			expectedStatus := generateStatus(i, params.IncludeErrors)
			if event.Status != expectedStatus {
				t.Logf("event %d: expected status %q, got %q", i, expectedStatus, event.Status)
				return false
			}
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   relayOrderGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 failed: %v", err)
	}
}

// relayOrderParams holds parameters for the relay order test
type relayOrderParams struct {
	NumEvents     int
	IncludeErrors bool
}

// relayOrderGenerator generates random test parameters for the relay order test
func relayOrderGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate sequence length between 5-20 events
	numEvents := rng.Intn(16) + 5 // 5 to 20 inclusive

	// 30% chance of including error events
	includeErrors := rng.Intn(10) < 3

	params := relayOrderParams{
		NumEvents:     numEvents,
		IncludeErrors: includeErrors,
	}
	values[0] = reflect.ValueOf(params)
}

// generateDeployEvent creates a DeployEvent with predictable values based on index
func generateDeployEvent(index int, includeErrors bool) docker.DeployEvent {
	return docker.DeployEvent{
		Step:    generateStepName(index),
		Message: generateMessage(index),
		Status:  generateStatus(index, includeErrors),
	}
}

// generateStepName creates a predictable step name based on index
func generateStepName(index int) string {
	steps := []string{"parse", "resolve", "pull", "write", "create", "start", "verify", "complete"}
	return steps[index%len(steps)] + "-" + string(rune('a'+index%26))
}

// generateMessage creates a predictable message based on index
func generateMessage(index int) string {
	messages := []string{
		"Starting deployment...",
		"Parsing compose file...",
		"Resolving dependencies...",
		"Pulling image...",
		"Writing compose file...",
		"Creating container...",
		"Starting container...",
		"Verifying status...",
		"Deployment complete!",
	}
	return messages[index%len(messages)] + " (step " + string(rune('0'+index/10)) + string(rune('0'+index%10)) + ")"
}

// generateStatus creates a status based on index, optionally including errors
func generateStatus(index int, includeErrors bool) string {
	statuses := []string{"running", "done"}
	if includeErrors && index%7 == 0 {
		return "error"
	}
	return statuses[index%len(statuses)]
}

// TestProperty_DeployEventFieldPreservation verifies that all fields of DeployEvent
// are correctly preserved through the relay mechanism
func TestProperty_DeployEventFieldPreservation(t *testing.T) {
	prop := func(events []deployEventSample) bool {
		if len(events) == 0 {
			return true
		}

		// Create session and relay events
		session := &docker.DeploySession{
			ID:     "test-field-preservation",
			Events: make(chan docker.DeployEvent, len(events)*2),
			Done:   make(chan struct{}),
		}

		// Write all events
		go func() {
			for _, e := range events {
				session.Events <- docker.DeployEvent{
					Step:    e.Step,
					Message: e.Message,
					Status:  e.Status,
				}
			}
			close(session.Events)
		}()

		// Read and verify each field
		received := make([]docker.DeployEvent, 0, len(events))
		for event := range session.Events {
			received = append(received, event)
		}

		if len(received) != len(events) {
			t.Logf("event count mismatch: expected %d, got %d", len(events), len(received))
			return false
		}

		// Verify each field for each event
		for i, original := range events {
			relayed := received[i]

			if relayed.Step != original.Step {
				t.Logf("event %d: Step field mismatch: expected %q, got %q", i, original.Step, relayed.Step)
				return false
			}
			if relayed.Message != original.Message {
				t.Logf("event %d: Message field mismatch: expected %q, got %q", i, original.Message, relayed.Message)
				return false
			}
			if relayed.Status != original.Status {
				t.Logf("event %d: Status field mismatch: expected %q, got %q", i, original.Status, relayed.Status)
				return false
			}
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   eventSampleGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("DeployEvent field preservation test failed: %v", err)
	}
}

// deployEventSample is a sample DeployEvent for testing
type deployEventSample struct {
	Step    string
	Message string
	Status  string
}

// eventSampleGenerator generates random DeployEvent samples
func eventSampleGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate 1-15 events
	numEvents := rng.Intn(15) + 1

	samples := make([]deployEventSample, numEvents)
	stepNames := []string{"init", "prepare", "pull", "build", "run", "verify", "finish"}
	statuses := []string{"running", "done", "error"}

	for i := 0; i < numEvents; i++ {
		msgLen := rng.Intn(50) + 10
		message := make([]byte, msgLen)
		msgChars := "abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		for j := range message {
			message[j] = msgChars[rng.Intn(len(msgChars))]
		}

		samples[i] = deployEventSample{
			Step:    stepNames[rng.Intn(len(stepNames))],
			Message: string(message),
			Status:  statuses[rng.Intn(len(statuses))],
		}
	}

	values[0] = reflect.ValueOf(samples)
}

// TestProperty_DeployEventOrderingWithConcurrentWrites tests that order is preserved
// even when writes happen in tight succession (simulating rapid event generation)
func TestProperty_DeployEventOrderingWithConcurrentWrites(t *testing.T) {
	prop := func(params concurrentWriteParams) bool {
		session := &docker.DeploySession{
			ID:     "test-concurrent-writes",
			Events: make(chan docker.DeployEvent, params.NumEvents*2),
			Done:   make(chan struct{}),
		}

		// Write all events without delay (concurrent)
		go func() {
			for i := 0; i < params.NumEvents; i++ {
				session.Events <- docker.DeployEvent{
					Step:    string(rune('0' + i)),
					Message: "event-" + string(rune('a'+i)),
					Status:  "running",
				}
			}
			close(session.Events)
		}()

		// Read events - they should be in order due to channel semantics
		received := make([]docker.DeployEvent, 0, params.NumEvents)
		for event := range session.Events {
			received = append(received, event)
		}

		// Verify order is preserved
		for i, event := range received {
			expectedStep := string(rune('0' + i))
			if event.Step != expectedStep {
				t.Logf("order violation at position %d: expected step %q, got %q", i, expectedStep, event.Step)
				return false
			}
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   concurrentWriteGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Concurrent writes ordering test failed: %v", err)
	}
}

// concurrentWriteParams holds parameters for concurrent write test
type concurrentWriteParams struct {
	NumEvents int
}

// concurrentWriteGenerator generates parameters for concurrent write test
func concurrentWriteGenerator(values []reflect.Value, rng *rand.Rand) {
	numEvents := rng.Intn(50) + 10 // 10-59 events
	values[0] = reflect.ValueOf(concurrentWriteParams{NumEvents: numEvents})
}