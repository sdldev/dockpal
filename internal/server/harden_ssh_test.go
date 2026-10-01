package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
)

// hardenTestInstance stores an instance with an encrypted password so the
// hardening handler has a resolvable credential.
func hardenTestInstance(t *testing.T, database *db.DB, id string, withPassword bool) {
	t.Helper()
	inst := db.Instance{
		ID:        id,
		Name:      "srv-" + id,
		Host:      "127.0.0.1",
		Port:      9273,
		Mode:      "direct",
		Status:    "offline",
		SSHHost:   "127.0.0.1",
		SSHPort:   22,
		SSHUser:   "root",
		SSHAuthType: "password",
	}
	if withPassword {
		cryptoKey, err := registry.DeriveKey("test-jwt-secret")
		if err != nil {
			t.Fatalf("derive key: %v", err)
		}
		enc, err := registry.Encrypt([]byte("hunter2"), cryptoKey)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		inst.SSHPasswordEncrypted = enc
	}
	if err := database.SaveInstance(inst); err != nil {
		t.Fatalf("save instance: %v", err)
	}
}

func hardenRequest(t *testing.T, database *db.DB, running *sync.Map, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/instances/:instance_id/harden", handleHardenSSH(database, "test-jwt-secret", NewInstallLogsManager(), running))
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHardenSSH_LocalInstanceRejected(t *testing.T) {
	database := newTestDB(t)
	w := hardenRequest(t, database, &sync.Map{}, http.MethodPost, "/instances/local/harden", "{}")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for local instance, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not managed over SSH") {
		t.Errorf("unexpected error message: %s", w.Body.String())
	}
}

func TestHardenSSH_MissingInstance(t *testing.T) {
	database := newTestDB(t)
	w := hardenRequest(t, database, &sync.Map{}, http.MethodPost, "/instances/inst-none/harden", "{}")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHardenSSH_NoStoredCredentials(t *testing.T) {
	database := newTestDB(t)
	hardenTestInstance(t, database, "inst-nocreds", false)
	w := hardenRequest(t, database, &sync.Map{}, http.MethodPost, "/instances/inst-nocreds/harden", "{}")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no SSH credentials stored") {
		t.Errorf("unexpected error message: %s", w.Body.String())
	}
}

func TestHardenSSH_ConcurrentRunRejected(t *testing.T) {
	database := newTestDB(t)
	hardenTestInstance(t, database, "inst-busy", true)
	running := &sync.Map{}
	running.Store("inst-busy", struct{}{})
	w := hardenRequest(t, database, running, http.MethodPost, "/instances/inst-busy/harden", "{}")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a second concurrent harden, got %d", w.Code)
	}
}

func TestHardenSSH_StartsAndWritesAudit(t *testing.T) {
	database := newTestDB(t)
	hardenTestInstance(t, database, "inst-harden", true)
	running := &sync.Map{}

	w := hardenRequest(t, database, running, http.MethodPost, "/instances/inst-harden/harden", "{}")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"session":"harden-inst-harden"`) {
		t.Errorf("expected namespaced session key in response: %s", w.Body.String())
	}

	// The job runs against 127.0.0.1:22 in the test env and fails fast; wait
	// for it to release the in-flight slot, proving the goroutine completed.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, busy := running.Load("inst-harden"); !busy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, busy := running.Load("inst-harden"); busy {
		t.Fatal("harden job did not release its in-flight slot")
	}

	// Failure must NOT mark the instance hardened or touch its credentials.
	inst, err := database.GetInstance("inst-harden")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if inst.SSHHardeningStatus != "" {
		t.Errorf("failed hardening must not persist status, got %q", inst.SSHHardeningStatus)
	}
	if len(inst.SSHPasswordEncrypted) == 0 {
		t.Error("failed hardening must keep the stored password")
	}

	// The audit trail records the attempt.
	logs, _, err := database.ListAuditLogs(10, 0)
	if err != nil {
		t.Fatalf("get audit logs: %v", err)
	}
	found := false
	for _, l := range logs {
		if l.Action == "instance.harden_ssh" {
			found = true
		}
	}
	if !found {
		t.Error("instance.harden_ssh audit entry missing")
	}
}

func TestListInstances_IncludesHardeningState(t *testing.T) {
	database := newTestDB(t)
	hardenTestInstance(t, database, "inst-list", true)
	inst, _ := database.GetInstance("inst-list")
	inst.SSHHardeningStatus = "hardened"
	inst.SSHHardenedAt = 1700000000
	if err := database.SaveInstance(*inst); err != nil {
		t.Fatalf("save: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances", handleListInstances(database))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/instances", nil))

	body := w.Body.String()
	for _, want := range []string{`"ssh_auth_type":"password"`, `"ssh_hardening_status":"hardened"`, `"ssh_hardened_at":1700000000`} {
		if !strings.Contains(body, want) {
			t.Errorf("list response missing %s: %s", want, body)
		}
	}
}
