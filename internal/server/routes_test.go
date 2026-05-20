package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/update"
)

func TestCheckOrigin(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		host     string
		expected bool
	}{
		{
			name:     "matching origin and host",
			origin:   "https://example.com",
			host:     "example.com",
			expected: true,
		},
		{
			name:     "matching origin with port",
			origin:   "https://example.com:8080",
			host:     "example.com:8080",
			expected: true,
		},
		{
			name:     "mismatched origin host",
			origin:   "https://evil.com",
			host:     "example.com",
			expected: false,
		},
		{
			name:     "empty origin header",
			origin:   "",
			host:     "example.com",
			expected: false,
		},
		{
			name:     "origin without host portion",
			origin:   "not-a-url",
			host:     "example.com",
			expected: false,
		},
		{
			name:     "origin with different port",
			origin:   "https://example.com:9090",
			host:     "example.com:8080",
			expected: false,
		},
		{
			name:     "http scheme matching host",
			origin:   "http://localhost:3000",
			host:     "localhost:3000",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/ws", nil)
			r.Host = tt.host
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}

			result := checkOrigin(r)
			if result != tt.expected {
				t.Errorf("checkOrigin() = %v, want %v (origin=%q, host=%q)", result, tt.expected, tt.origin, tt.host)
			}
		})
	}
}

// Test_HandleUpdate_SSE verifies the refactored handler returns SSE frames (R7, R12).
func Test_HandleUpdate_SSE(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create test DB with admin user
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()
	if err := database.CreateUser(db.User{Username: "admin"}); err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}

	// Mock update service that emits and returns
	mockSvc := update.NewUpdateService("v0.1.0")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"downloadUrl": "https://github.com/example/asset"})
	c.Request, _ = http.NewRequest("POST", "/api/system/update", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("username", "admin")

	HandleUpdate(c, mockSvc, database)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "data: {") {
		t.Fatalf("expected SSE data frames with JSON, got: %s", bodyStr)
	}
}
