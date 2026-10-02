package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
)

// secTestInstance stores an instance with an encrypted password credential.
func secTestInstance(t *testing.T, database *db.DB, id string, withPassword bool) {
	t.Helper()
	inst := db.Instance{
		ID:          id,
		Name:        "srv-" + id,
		Host:        "127.0.0.1",
		Port:        9273,
		Mode:        "direct",
		Status:      "offline",
		SSHHost:     "127.0.0.1",
		SSHPort:     22,
		SSHUser:     "root",
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

func TestDetectSecurity_LocalInstanceRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:instance_id/security", handleDetectSecurity(newTestDB(t), "test-jwt-secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/instances/local/security", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for local instance, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("not managed over SSH")) {
		t.Errorf("unexpected error message: %s", w.Body.String())
	}
}

func TestDetectSecurity_MissingInstance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:instance_id/security", handleDetectSecurity(newTestDB(t), "test-jwt-secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/instances/inst-none/security", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDetectSecurity_NoStoredCredentials(t *testing.T) {
	database := newTestDB(t)
	secTestInstance(t, database, "inst-nosec", false)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:instance_id/security", handleDetectSecurity(database, "test-jwt-secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/instances/inst-nosec/security", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without stored credentials, got %d", w.Code)
	}
}

func TestApplySecurity_MissingFieldsRejected(t *testing.T) {
	database := newTestDB(t)
	secTestInstance(t, database, "inst-sec", true)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/instances/:instance_id/security", handleApplySecurity(database, "test-jwt-secret", NewInstallLogsManager(), &sync.Map{}))
	w := httptest.NewRecorder()
	// fail2ban missing — all three toggles are required (desired-state model).
	req := httptest.NewRequest(http.MethodPost, "/instances/inst-sec/security", bytes.NewBufferString(`{"password_auth":false,"root_login":true}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing toggles, got %d", w.Code)
	}
}

func TestApplySecurity_StartsJobAndPersistsNothingOnFailure(t *testing.T) {
	database := newTestDB(t)
	secTestInstance(t, database, "inst-apply", true)
	running := &sync.Map{}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/instances/:instance_id/security", handleApplySecurity(database, "test-jwt-secret", NewInstallLogsManager(), running))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/instances/inst-apply/security",
		bytes.NewBufferString(`{"password_auth":false,"root_login":true,"fail2ban":true}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	// The job dials 127.0.0.1:22 and fails fast in tests; wait for the slot.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, busy := running.Load("inst-apply"); !busy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, busy := running.Load("inst-apply"); busy {
		t.Fatal("security job did not release its in-flight slot")
	}

	// A failed run must not mark the instance hardened.
	inst, err := database.GetInstance("inst-apply")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if inst.SSHHardeningStatus != "" {
		t.Errorf("failed apply must not persist status, got %q", inst.SSHHardeningStatus)
	}

	logs, _, err := database.ListAuditLogs(10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	found := false
	for _, l := range logs {
		if l.Action == "instance.security" {
			found = true
		}
	}
	if !found {
		t.Error("instance.security audit entry missing")
	}
}

func TestApplySecurity_ConflictsWithRunningJob(t *testing.T) {
	database := newTestDB(t)
	secTestInstance(t, database, "inst-busy-sec", true)
	running := &sync.Map{}
	running.Store("inst-busy-sec", struct{}{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/instances/:instance_id/security", handleApplySecurity(database, "test-jwt-secret", NewInstallLogsManager(), running))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/instances/inst-busy-sec/security", bytes.NewBufferString(`{"password_auth":false,"root_login":false,"fail2ban":true}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 while another job runs, got %d", w.Code)
	}
}
