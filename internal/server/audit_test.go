package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
)

func TestPurgeAuditLogsOlderThan(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.New(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()

	now := time.Now()
	oldLog := db.AuditLog{ID: "old", Timestamp: now.Add(-48 * time.Hour).Unix(), Action: "old"}
	newLog := db.AuditLog{ID: "new", Timestamp: now.Unix(), Action: "new"}
	if err := database.SaveAuditLog(oldLog); err != nil {
		t.Fatalf("save old audit log: %v", err)
	}
	if err := database.SaveAuditLog(newLog); err != nil {
		t.Fatalf("save new audit log: %v", err)
	}

	deleted, err := database.PurgeAuditLogsOlderThan(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("purge audit logs: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	logs, total, err := database.ListAuditLogs(10, 0)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("remaining logs total=%d len=%d, want 1", total, len(logs))
	}
	if logs[0].ID != "new" {
		t.Fatalf("remaining log = %q, want new", logs[0].ID)
	}
}

func TestAuditLogFramework(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create test DB
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()

	// 1. Test LogAudit helper
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("username", "admin_user")
	c.Set("role", auth.RoleAdmin)
	c.Request = httptest.NewRequest("GET", "/test-endpoint", nil)
	c.Request.RemoteAddr = "192.168.1.100:12345"

	LogAudit(c, database, "container.start", "test-container", "success", "Container started successfully")

	// Verify log entry is saved
	logs, total, err := database.ListAuditLogs(10, 0)
	if err != nil {
		t.Fatalf("failed to list audit logs: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 log, got %d", total)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log entry returned, got %d", len(logs))
	}

	entry := logs[0]
	if entry.Username != "admin_user" {
		t.Errorf("expected username admin_user, got %s", entry.Username)
	}
	if entry.UserRole != auth.RoleAdmin {
		t.Errorf("expected role admin, got %s", entry.UserRole)
	}
	if entry.Action != "container.start" {
		t.Errorf("expected action container.start, got %s", entry.Action)
	}
	if entry.Resource != "test-container" {
		t.Errorf("expected resource test-container, got %s", entry.Resource)
	}
	if entry.Status != "success" {
		t.Errorf("expected status success, got %s", entry.Status)
	}
	if entry.Details != "Container started successfully" {
		t.Errorf("expected details, got %s", entry.Details)
	}
	if entry.IPAddress != "192.168.1.1" && entry.IPAddress != "127.0.0.1" && entry.IPAddress != "" {
		t.Logf("IP address is %s", entry.IPAddress)
	}

	// 2. Test handleListAuditLogs endpoint response
	r := gin.New()
	r.GET("/audit-logs", handleListAuditLogs(database))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/audit-logs?limit=5&offset=0", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var response struct {
		Logs   []db.AuditLog `json:"logs"`
		Total  int           `json:"total"`
		Limit  int           `json:"limit"`
		Offset int           `json:"offset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if response.Total != 1 {
		t.Errorf("expected total 1, got %d", response.Total)
	}
	if len(response.Logs) != 1 {
		t.Errorf("expected 1 log, got %d", len(response.Logs))
	}

	// 3. Test list in descending chronological order
	// Save second log entry
	time.Sleep(10 * time.Millisecond) // ensure distinct timestamps
	LogAudit(c, database, "container.stop", "test-container", "success", "Container stopped")

	logsDesc, _, _ := database.ListAuditLogs(10, 0)
	if len(logsDesc) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logsDesc))
	}
	// The first element should be the newest one (container.stop)
	if logsDesc[0].Action != "container.stop" {
		t.Errorf("expected newest log to be container.stop, got %s", logsDesc[0].Action)
	}
	if logsDesc[1].Action != "container.start" {
		t.Errorf("expected oldest log to be container.start, got %s", logsDesc[1].Action)
	}
}
