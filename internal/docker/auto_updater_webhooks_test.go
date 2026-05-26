package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// TestNotifyWebhooks_BestEffort verifies that webhook delivery errors do not
// propagate to the caller and that the main pipeline is not blocked.
func TestNotifyWebhooks_BestEffort(t *testing.T) {
	var called atomic.Int32

	// Start a test server that always returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := &AutoUpdateWorker{
		instanceID: "test-instance",
		listWebhooks: func() ([]db.NotificationWebhook, error) {
			return []db.NotificationWebhook{
				{ID: "wh-1", Name: "failing-hook", URL: srv.URL},
			}, nil
		},
	}

	// notifyWebhooks must not panic or block even when the webhook fails.
	w.notifyWebhooks("myapp", "test-instance", "failed", "rollback_failed", "test message", "attempt-1")

	// Give the goroutine time to complete.
	time.Sleep(100 * time.Millisecond)

	if called.Load() != 1 {
		t.Errorf("expected webhook to be called once, got %d", called.Load())
	}
}

// TestNotifyWebhooks_PayloadFormat verifies the JSON body sent to webhooks
// matches the spec: {type, app, instance_id, stage, error_code, message, attempt_id}.
func TestNotifyWebhooks_PayloadFormat(t *testing.T) {
	var receivedBody []byte
	var mu sync.Mutex
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		receivedBody = buf[:n]

		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", r.Header.Get("Content-Type"))
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		close(done)
	}))
	defer srv.Close()

	w := &AutoUpdateWorker{
		instanceID: "local",
		listWebhooks: func() ([]db.NotificationWebhook, error) {
			return []db.NotificationWebhook{
				{ID: "wh-1", Name: "test-hook", URL: srv.URL},
			}, nil
		},
	}

	w.notifyWebhooks("webapp", "local", "rolled_back", "health_probe_failed", "container exited", "attempt-123")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not called within timeout")
	}

	mu.Lock()
	defer mu.Unlock()

	var payload webhookPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal webhook payload: %v", err)
	}

	if payload.Type != "app_update" {
		t.Errorf("expected type=app_update, got %q", payload.Type)
	}
	if payload.App != "webapp" {
		t.Errorf("expected app=webapp, got %q", payload.App)
	}
	if payload.InstanceID != "local" {
		t.Errorf("expected instance_id=local, got %q", payload.InstanceID)
	}
	if payload.Stage != "rolled_back" {
		t.Errorf("expected stage=rolled_back, got %q", payload.Stage)
	}
	if payload.ErrorCode != "health_probe_failed" {
		t.Errorf("expected error_code=health_probe_failed, got %q", payload.ErrorCode)
	}
	if payload.Message != "container exited" {
		t.Errorf("expected message='container exited', got %q", payload.Message)
	}
	if payload.AttemptID != "attempt-123" {
		t.Errorf("expected attempt_id=attempt-123, got %q", payload.AttemptID)
	}
}

// TestNotifyWebhooks_NilLister verifies that a nil listWebhooks is a no-op.
func TestNotifyWebhooks_NilLister(t *testing.T) {
	w := &AutoUpdateWorker{
		instanceID:   "local",
		listWebhooks: nil,
	}

	// Must not panic.
	w.notifyWebhooks("app", "local", "failed", "rollback_failed", "msg", "attempt-1")
}

// TestNotifyWebhooks_EmptyList verifies that an empty webhook list is a no-op.
func TestNotifyWebhooks_EmptyList(t *testing.T) {
	w := &AutoUpdateWorker{
		instanceID: "local",
		listWebhooks: func() ([]db.NotificationWebhook, error) {
			return nil, nil
		},
	}

	// Must not panic.
	w.notifyWebhooks("app", "local", "failed", "rollback_failed", "msg", "attempt-1")
}

// TestNotifyWebhooks_MultipleWebhooks verifies all configured webhooks are called.
func TestNotifyWebhooks_MultipleWebhooks(t *testing.T) {
	var called atomic.Int32

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	w := &AutoUpdateWorker{
		instanceID: "local",
		listWebhooks: func() ([]db.NotificationWebhook, error) {
			return []db.NotificationWebhook{
				{ID: "wh-1", Name: "hook-1", URL: srv1.URL},
				{ID: "wh-2", Name: "hook-2", URL: srv2.URL},
			}, nil
		},
	}

	w.notifyWebhooks("app", "local", "rolled_back", "health_probe_failed", "msg", "attempt-1")

	// Give goroutines time to complete.
	time.Sleep(200 * time.Millisecond)

	if called.Load() != 2 {
		t.Errorf("expected both webhooks to be called, got %d calls", called.Load())
	}
}

// TestNotifyWebhooks_NonBlocking verifies that webhook delivery does not
// block the caller even when the endpoint is slow.
func TestNotifyWebhooks_NonBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // Simulate a very slow endpoint.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := &AutoUpdateWorker{
		instanceID: "local",
		listWebhooks: func() ([]db.NotificationWebhook, error) {
			return []db.NotificationWebhook{
				{ID: "wh-1", Name: "slow-hook", URL: srv.URL},
			}, nil
		},
	}

	start := time.Now()
	w.notifyWebhooks("app", "local", "failed", "rollback_failed", "msg", "attempt-1")
	elapsed := time.Since(start)

	// notifyWebhooks should return almost immediately (< 100ms) because
	// it fires webhooks in goroutines.
	if elapsed > 100*time.Millisecond {
		t.Errorf("notifyWebhooks blocked for %v; expected non-blocking", elapsed)
	}
}

// TestTriggerApp_WebhookOnRolledBack verifies that webhooks are called when
// the pipeline reaches the rolled_back stage (health probe failed, rollback ok).
func TestTriggerApp_WebhookOnRolledBack(t *testing.T) {
	var webhookCalled atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookCalled.Add(1)
		var payload webhookPayload
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.Stage != "rolled_back" {
			t.Errorf("expected stage=rolled_back in webhook, got %q", payload.Stage)
		}
		if payload.ErrorCode != "health_probe_failed" {
			t.Errorf("expected error_code=health_probe_failed, got %q", payload.ErrorCode)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newFakeStore()
	client := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.project=myapp": {
				{Image: "nginx:latest", Labels: map[string]string{"dockpal.service": "web", "dockpal.project": "myapp"}},
			},
		},
		inspectDigest: map[string]string{"nginx:latest": "sha256:abc123"},
		healthErr:     context.DeadlineExceeded,
	}

	worker := &AutoUpdateWorker{
		client:     client,
		store:      store,
		instanceID: "local",
		cooldown:   time.Hour,
		grace:      5 * time.Second,
		getCompose: func(project string) (string, error) {
			return "version: '3'\nservices:\n  web:\n    image: nginx:latest\n", nil
		},
		listWebhooks: func() ([]db.NotificationWebhook, error) {
			return []db.NotificationWebhook{
				{ID: "wh-1", Name: "test-hook", URL: srv.URL},
			}, nil
		},
	}

	err := worker.TriggerApp(context.Background(), "myapp", true, true, "auto")
	if err != nil {
		t.Fatalf("TriggerApp returned error: %v", err)
	}

	// Give webhook goroutine time to complete.
	time.Sleep(200 * time.Millisecond)

	if webhookCalled.Load() != 1 {
		t.Errorf("expected webhook to be called once on rolled_back, got %d", webhookCalled.Load())
	}
}

// TestTriggerApp_WebhookOnRollbackFailed verifies that webhooks are called
// when the pipeline reaches the failed stage after a rollback failure.
func TestTriggerApp_WebhookOnRollbackFailed(t *testing.T) {
	var webhookCalled atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookCalled.Add(1)
		var payload webhookPayload
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.Stage != "failed" {
			t.Errorf("expected stage=failed in webhook, got %q", payload.Stage)
		}
		if payload.ErrorCode != "rollback_failed" {
			t.Errorf("expected error_code=rollback_failed, got %q", payload.ErrorCode)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newFakeStore()
	// deployErrSeq: first call (forcePull=true) succeeds, second call
	// (rollback, forcePull=false) fails.
	client := &fakeAutoUpdaterClient{
		listByLabel: map[string][]ContainerInfo{
			"dockpal.project=myapp": {
				{Image: "nginx:latest", Labels: map[string]string{"dockpal.service": "web", "dockpal.project": "myapp"}},
			},
		},
		inspectDigest: map[string]string{"nginx:latest": "sha256:abc123"},
		healthErr:     context.DeadlineExceeded,
		deployErrSeq:  []error{nil, context.DeadlineExceeded}, // rollback deploy fails
	}

	worker := &AutoUpdateWorker{
		client:     client,
		store:      store,
		instanceID: "local",
		cooldown:   time.Hour,
		grace:      5 * time.Second,
		getCompose: func(project string) (string, error) {
			return "version: '3'\nservices:\n  web:\n    image: nginx:latest\n", nil
		},
		listWebhooks: func() ([]db.NotificationWebhook, error) {
			return []db.NotificationWebhook{
				{ID: "wh-1", Name: "test-hook", URL: srv.URL},
			}, nil
		},
	}

	err := worker.TriggerApp(context.Background(), "myapp", true, true, "auto")
	if err != nil {
		t.Fatalf("TriggerApp returned error: %v", err)
	}

	// Give webhook goroutine time to complete.
	time.Sleep(200 * time.Millisecond)

	if webhookCalled.Load() != 1 {
		t.Errorf("expected webhook to be called once on rollback_failed, got %d", webhookCalled.Load())
	}
}
