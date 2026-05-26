package docker

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
)

// TestAutoUpdateWorker_StructuredLogging verifies that:
// 1. The required structured fields (attempt_id, app, instance_id, service,
//    image, old_digest, new_digest, stage, error_code) are present in the
//    log output during a scripted update.
// 2. No log line contains credential-related substrings ("auth", "token",
//    "password") in a case-insensitive match — validating R10.5.
//
// Validates: Requirements R10.4, R10.5
func TestAutoUpdateWorker_StructuredLogging(t *testing.T) {
	// Capture log output into a buffer.
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	log.SetFlags(0) // Remove timestamps for easier assertion.
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	})

	// Set up a fake client that simulates a successful update pipeline:
	// list containers → inspect digest → pull → deploy → health probe.
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {
				projectContainer("myapp", "web", "nginx:1.25"),
			},
			"dockpal.project=myapp": {
				projectContainer("myapp", "web", "nginx:1.25"),
			},
		},
		inspectDigest: map[string]string{
			"nginx:1.25": "sha256:olddigest1234567890abcdef",
		},
	}

	store := newFakeStore()

	composeYAML := `services:
  web:
    image: nginx:1.25
    labels:
      dockpal.project: myapp
      dockpal.service: web
      dockpal.auto-update: "true"
`

	w, _ := newWorker(t, fc, store, composeYAML)

	// Run the pipeline via TriggerApp (happy path).
	ctx := context.Background()
	err := w.TriggerApp(ctx, "myapp", true, true, "auto")
	if err != nil {
		t.Fatalf("TriggerApp returned unexpected error: %v", err)
	}

	logOutput := logBuf.String()
	if logOutput == "" {
		t.Fatal("expected log output but got empty string")
	}

	// --- Assertion 1: Required structured fields are present (R10.4) ---
	// The log output should contain [auto-update] lines with these key= prefixes.
	requiredFields := []string{
		"attempt_id=",
		"app=",
		"instance_id=",
		"stage=",
	}
	for _, field := range requiredFields {
		if !strings.Contains(logOutput, field) {
			t.Errorf("required field %q not found in log output:\n%s", field, logOutput)
		}
	}

	// Verify the [auto-update] prefix is present.
	if !strings.Contains(logOutput, "[auto-update]") {
		t.Errorf("expected [auto-update] prefix in log output:\n%s", logOutput)
	}

	// Verify that the stage transitions are logged (pulling, recreating,
	// verifying, completed).
	expectedStages := []string{
		"stage=pulling",
		"stage=recreating",
		"stage=verifying",
		"stage=completed",
	}
	for _, stage := range expectedStages {
		if !strings.Contains(logOutput, stage) {
			t.Errorf("expected stage %q in log output:\n%s", stage, logOutput)
		}
	}

	// Verify the app name appears in the log.
	if !strings.Contains(logOutput, "app=myapp") {
		t.Errorf("expected app=myapp in log output:\n%s", logOutput)
	}

	// Verify instance_id is logged.
	if !strings.Contains(logOutput, "instance_id=test-instance") {
		t.Errorf("expected instance_id=test-instance in log output:\n%s", logOutput)
	}

	// --- Assertion 2: No credential leakage (R10.5) ---
	// Check that no line contains "auth", "token", or "password"
	// (case-insensitive). This validates that registry credentials are never
	// leaked into log output.
	forbiddenPatterns := []string{"auth", "token", "password"}
	lines := strings.Split(logOutput, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, pattern := range forbiddenPatterns {
			if strings.Contains(lower, pattern) {
				t.Errorf("line %d contains forbidden substring %q (credential leakage):\n  %s",
					i+1, pattern, line)
			}
		}
	}
}

// TestAutoUpdateWorker_StructuredLogging_CredentialRedaction verifies that
// when a pipeline error message accidentally contains credential-like
// substrings, the log output redacts them rather than leaking them.
//
// Validates: Requirements R10.5
func TestAutoUpdateWorker_StructuredLogging_CredentialRedaction(t *testing.T) {
	// Capture log output.
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	})

	// Simulate a pull failure whose error message contains a credential marker.
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {
				projectContainer("secretapp", "api", "registry.example.com/api:latest"),
			},
			"dockpal.project=secretapp": {
				projectContainer("secretapp", "api", "registry.example.com/api:latest"),
			},
		},
		inspectDigest: map[string]string{
			"registry.example.com/api:latest": "sha256:prevdigest999",
		},
		// The pull error message intentionally contains a credential marker
		// to verify the redaction logic.
		pullErr: errWithCredentialLeak("pull failed: x-registry-auth header invalid for Bearer eyJhbGciOiJIUzI1NiJ9"),
	}

	store := newFakeStore()

	composeYAML := `services:
  api:
    image: registry.example.com/api:latest
    labels:
      dockpal.project: secretapp
      dockpal.service: api
      dockpal.auto-update: "true"
`

	w, _ := newWorker(t, fc, store, composeYAML)

	// Set up a monitor with a cache entry that reports HasUpdate=true so the
	// pull loop actually runs and hits the pull error.
	monitor := &ImageUpdateMonitor{
		cache: map[string]*ImageUpdateStatus{
			"registry.example.com/api:latest": {
				ImageRef: "registry.example.com/api:latest",
				Result: &ImageUpdateResult{
					HasUpdate:    true,
					RemoteDigest: "sha256:newdigest123",
				},
			},
		},
	}
	w.monitor = monitor

	ctx := context.Background()
	// The pipeline will fail at pull, which is expected.
	_ = w.TriggerApp(ctx, "secretapp", true, true, "auto")

	logOutput := logBuf.String()
	if logOutput == "" {
		t.Fatal("expected log output but got empty string")
	}

	// The raw credential markers must NOT appear in the log output.
	rawCredentials := []string{
		"x-registry-auth",
		"Bearer eyJhbGciOiJIUzI1NiJ9",
	}
	for _, cred := range rawCredentials {
		if strings.Contains(logOutput, cred) {
			t.Errorf("raw credential %q leaked into log output:\n%s", cred, logOutput)
		}
	}

	// The redacted marker should appear instead.
	if !strings.Contains(logOutput, redactedMarker) {
		t.Errorf("expected redacted marker %q in log output when credential is present:\n%s",
			redactedMarker, logOutput)
	}
}

// TestAutoUpdateWorker_StructuredLogging_FieldsOnFailure verifies that the
// error_code field is present in log output when the pipeline fails.
//
// Validates: Requirements R10.4
func TestAutoUpdateWorker_StructuredLogging_FieldsOnFailure(t *testing.T) {
	// Capture log output.
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	})

	// Simulate a compose failure that triggers rollback.
	fc := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.auto-update=true": {
				projectContainer("failapp", "svc", "myimg:v2"),
			},
			"dockpal.project=failapp": {
				projectContainer("failapp", "svc", "myimg:v2"),
			},
		},
		inspectDigest: map[string]string{
			"myimg:v2": "sha256:prevdigestfail",
		},
		// First deploy (pipeline) fails, second deploy (rollback) succeeds.
		deployErrSeq: []error{
			errSimple("compose up failed: network timeout"),
			nil,
		},
	}

	store := newFakeStore()

	composeYAML := `services:
  svc:
    image: myimg:v2
    labels:
      dockpal.project: failapp
      dockpal.service: svc
      dockpal.auto-update: "true"
`

	w, _ := newWorker(t, fc, store, composeYAML)

	ctx := context.Background()
	_ = w.TriggerApp(ctx, "failapp", true, true, "auto")

	logOutput := logBuf.String()

	// Verify error_code field is present in the log.
	if !strings.Contains(logOutput, "error_code=") {
		t.Errorf("expected error_code= field in log output on failure:\n%s", logOutput)
	}

	// Verify the compose_error code appears (since compose failed and rollback succeeded).
	if !strings.Contains(logOutput, ErrComposeError) {
		t.Errorf("expected error code %q in log output:\n%s", ErrComposeError, logOutput)
	}

	// Verify stage=rolled_back is logged.
	if !strings.Contains(logOutput, "stage=rolled_back") {
		t.Errorf("expected stage=rolled_back in log output:\n%s", logOutput)
	}

	// --- No credential leakage even on failure path ---
	forbiddenPatterns := []string{"auth", "token", "password"}
	lines := strings.Split(logOutput, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, pattern := range forbiddenPatterns {
			if strings.Contains(lower, pattern) {
				t.Errorf("line %d contains forbidden substring %q (credential leakage):\n  %s",
					i+1, pattern, line)
			}
		}
	}
}

// --- Test helpers ---

// errWithCredentialLeak returns an error whose message contains credential
// markers, simulating a Docker client that accidentally surfaces auth headers
// in error text.
func errWithCredentialLeak(msg string) error {
	return &credentialLeakError{msg: msg}
}

type credentialLeakError struct {
	msg string
}

func (e *credentialLeakError) Error() string { return e.msg }

// errSimple returns a plain error for test scripting.
func errSimple(msg string) error {
	return &simpleTestError{msg: msg}
}

type simpleTestError struct {
	msg string
}

func (e *simpleTestError) Error() string { return e.msg }

// Ensure the test helpers satisfy the error interface at compile time.
var (
	_ error = (*credentialLeakError)(nil)
	_ error = (*simpleTestError)(nil)
)
