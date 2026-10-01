package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

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
	hardenTestInstance(t, database, "inst-nosec", false)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:instance_id/security", handleDetectSecurity(database, "test-jwt-secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/instances/inst-nosec/security", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without stored credentials, got %d", w.Code)
	}
}

func TestApplySecurity_InvalidControlRejected(t *testing.T) {
	database := newTestDB(t)
	hardenTestInstance(t, database, "inst-sec", true)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/instances/:instance_id/security", handleApplySecurity(database, "test-jwt-secret", NewInstallLogsManager(), &sync.Map{}))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/instances/inst-sec/security", bytes.NewBufferString(`{"control":"reboot","enabled":true}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown control, got %d", w.Code)
	}
}

func TestApplySecurity_EnabledRequired(t *testing.T) {
	database := newTestDB(t)
	hardenTestInstance(t, database, "inst-sec2", true)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/instances/:instance_id/security", handleApplySecurity(database, "test-jwt-secret", NewInstallLogsManager(), &sync.Map{}))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/instances/inst-sec2/security", bytes.NewBufferString(`{"control":"password_auth"}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without enabled, got %d", w.Code)
	}
}

func TestApplySecurity_ConflictsWithHardenJob(t *testing.T) {
	database := newTestDB(t)
	hardenTestInstance(t, database, "inst-busy-sec", true)
	running := &sync.Map{}
	running.Store("inst-busy-sec", struct{}{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/instances/:instance_id/security", handleApplySecurity(database, "test-jwt-secret", NewInstallLogsManager(), running))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/instances/inst-busy-sec/security", bytes.NewBufferString(`{"control":"fail2ban","enabled":true}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 while another job runs, got %d", w.Code)
	}
}
