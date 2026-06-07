package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockDB implements DBPinger for tests.
type mockDB struct {
	err error
}

func (m *mockDB) Ping() error { return m.err }

func okDB() DBPinger  { return &mockDB{} }
func badDB() DBPinger { return &mockDB{err: errors.New("db error")} }

func TestChecker_CheckHealth(t *testing.T) {
	checker := NewChecker(okDB(), "", "/", nil, "v0.9.0-test")

	response := checker.CheckHealth(context.Background())

	if len(response.Checks) == 0 {
		t.Error("Expected health checks to be performed")
	}
	if _, exists := response.Checks["disk_root"]; !exists {
		t.Error("Expected disk_root check to exist")
	}
	if _, exists := response.Checks["disk_data"]; !exists {
		t.Error("Expected disk_data check to exist")
	}
	if response.Status == "" {
		t.Error("Expected status to be set")
	}
	if response.Timestamp == "" {
		t.Error("Expected timestamp to be set")
	}
	if response.Version != "v0.9.0-test" {
		t.Errorf("Expected version v0.9.0-test, got %s", response.Version)
	}

	t.Logf("Health status: %s", response.Status)
	t.Logf("Checks performed: %v", response.Checks)
}

func TestChecker_CheckLiveness(t *testing.T) {
	checker := NewChecker(okDB(), "", "/", nil, "v0.9.0-test")

	response := checker.CheckLiveness(context.Background())

	if response.Status != StatusHealthy {
		t.Errorf("Expected liveness to be healthy, got %s", response.Status)
	}
	if len(response.Checks) != 1 {
		t.Errorf("Expected 1 check for liveness, got %d", len(response.Checks))
	}

	processCheck, exists := response.Checks["process"]
	if !exists {
		t.Error("Expected process check to exist")
	}
	if processCheck.Status != CheckPass {
		t.Errorf("Expected process check to pass, got %s", processCheck.Status)
	}
}

func TestChecker_CheckReadiness(t *testing.T) {
	checker := NewChecker(okDB(), "", "/", nil, "v0.9.0-test")

	response := checker.CheckReadiness(context.Background())

	if len(response.Checks) != 2 {
		t.Errorf("Expected 2 checks for readiness, got %d", len(response.Checks))
	}
	if _, exists := response.Checks["database"]; !exists {
		t.Error("Expected database check to exist")
	}
	dockerCheck, exists := response.Checks["docker"]
	if !exists {
		t.Error("Expected docker check to exist")
	} else if dockerCheck.Status != CheckFail {
		t.Errorf("Expected docker check to fail with nil client, got %s", dockerCheck.Status)
	}

	t.Logf("Readiness status: %s", response.Status)
}

func TestChecker_CheckDatabase_Pass(t *testing.T) {
	checker := NewChecker(okDB(), "", "/", nil, "v0.9.0-test")

	result := checker.checkDatabase(context.Background())

	if result.Status != CheckPass {
		t.Errorf("Expected database check to pass, got %s: %s", result.Status, result.Description)
	}
	if result.Duration == "" {
		t.Error("Expected duration to be set")
	}
}

func TestChecker_CheckDatabase_Fail(t *testing.T) {
	checker := NewChecker(badDB(), "", "/", nil, "v0.9.0-test")

	result := checker.checkDatabase(context.Background())

	if result.Status != CheckFail {
		t.Errorf("Expected database check to fail, got %s", result.Status)
	}
	if result.Description == "" {
		t.Error("Expected error description to be set")
	}
}

func TestChecker_CheckDatabase_Nil(t *testing.T) {
	checker := NewChecker(nil, "", "/", nil, "v0.9.0-test")

	result := checker.checkDatabase(context.Background())

	if result.Status != CheckFail {
		t.Errorf("Expected database check to fail with nil db, got %s", result.Status)
	}
}

func TestChecker_CheckDiskSpace(t *testing.T) {
	checker := NewChecker(okDB(), "", "/", nil, "v0.9.0-test")

	result := checker.checkDiskSpace(context.Background(), "/")

	if result.Status == "" {
		t.Error("Expected disk check status to be set")
	}
	if result.Duration == "" {
		t.Error("Expected duration to be set")
	}
	if result.Details == nil {
		t.Error("Expected disk details to be set")
	}

	t.Logf("Disk check status: %s", result.Status)
}

func TestChecker_CheckMemory(t *testing.T) {
	checker := NewChecker(okDB(), "", "/", nil, "v0.9.0-test")

	result := checker.checkMemory(context.Background())

	if result.Status == "" {
		t.Error("Expected memory check status to be set")
	}
	if result.Duration == "" {
		t.Error("Expected duration to be set")
	}
	if result.Details == nil {
		t.Error("Expected memory details to be set")
	}
	if details, ok := result.Details.(map[string]interface{}); ok {
		if _, exists := details["available_mb"]; !exists {
			t.Error("Expected available_mb in memory details")
		}
		if _, exists := details["allocated_mb"]; !exists {
			t.Error("Expected allocated_mb in memory details")
		}
	}

	t.Logf("Memory check status: %s", result.Status)
}

func TestChecker_GetHTTPStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected int
	}{
		{"healthy", StatusHealthy, 200},
		{"degraded", StatusDegraded, 200},
		{"unhealthy", StatusUnhealthy, 503},
		{"unknown", Status("unknown"), 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := &HealthResponse{
				Status:    tt.status,
				Timestamp: time.Now().Format(time.RFC3339),
				Uptime:    "1s",
				Version:   "v0.9.0-test",
				Checks:    make(map[string]CheckResult),
			}

			httpStatus := response.GetHTTPStatus()
			if httpStatus != tt.expected {
				t.Errorf("Expected HTTP status %d for %s, got %d", tt.expected, tt.status, httpStatus)
			}
		})
	}
}

func TestHealthResponse_ToJSON(t *testing.T) {
	response := &HealthResponse{
		Status:    StatusHealthy,
		Timestamp: "2023-01-01T00:00:00Z",
		Uptime:    "1s",
		Version:   "v0.9.0-test",
		Checks: map[string]CheckResult{
			"test": {
				Status:      CheckPass,
				Description: "Test check",
				Duration:    "1ms",
			},
		},
	}

	jsonBytes, err := response.ToJSON()
	if err != nil {
		t.Fatalf("Failed to convert to JSON: %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Error("Expected non-empty JSON output")
	}

	jsonStr := string(jsonBytes)
	t.Logf("JSON output: %s", jsonStr)

	if !contains(jsonStr, "healthy") {
		t.Error("Expected JSON to contain 'healthy'")
	}
	if !contains(jsonStr, "v0.9.0-test") {
		t.Error("Expected JSON to contain version")
	}
}

// Helper function
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
