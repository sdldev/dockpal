package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
)

func TestWebhookHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer database.Close()

	r := gin.New()

	r.GET("/api/webhooks", HandleListWebhooks(database))
	r.POST("/api/webhooks", HandleCreateWebhook(database))
	r.DELETE("/api/webhooks/:webhook_id", HandleDeleteWebhook(database))

	var createdWh db.Webhook

	t.Run("CreateWebhook", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]string{
			"instance_id":  "inst-123",
			"name":         "test-app",
			"repo":         "https://github.com/test/repo",
			"branch":       "main",
			"compose_file": "docker-compose.yml",
			"secret":       "my-webhook-secret",
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/webhooks", bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d. Body: %s", w.Code, w.Body.String())
		}

		if err := json.Unmarshal(w.Body.Bytes(), &createdWh); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if createdWh.ID == "" || createdWh.Secret != "my-webhook-secret" {
			t.Errorf("webhook not created properly: %+v", createdWh)
		}
	})

	t.Run("ListWebhooks", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/webhooks", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}

		var list []db.Webhook
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
			t.Fatalf("failed to parse list: %v", err)
		}

		if len(list) != 1 || list[0].ID != createdWh.ID {
			t.Errorf("expected list with 1 webhook, got %+v", list)
		}
	})

	t.Run("DeleteWebhook", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("DELETE", "/api/webhooks/"+createdWh.ID, nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}

		// Verify it is gone
		w2 := httptest.NewRecorder()
		req2, _ := http.NewRequest("GET", "/api/webhooks", nil)
		r.ServeHTTP(w2, req2)

		var list []db.Webhook
		json.Unmarshal(w2.Body.Bytes(), &list)
		if len(list) != 0 {
			t.Errorf("expected list to be empty after deletion, got %d items", len(list))
		}
	})
}

func TestWebhookDeploySignatureValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer database.Close()

	// Register a test webhook
	wh := db.Webhook{
		ID:         "test-wh",
		InstanceID: "inst-123",
		Secret:     "secret-key",
		Repo:       "https://github.com/test/repo",
		Name:       "test-proj",
	}
	database.CreateWebhook(wh)

	r := gin.New()
	r.POST("/api/webhooks/deploy/:webhook_id", HandleWebhookDeploy(database, nil, "jwt-secret"))

	payload := []byte(`{"ref": "refs/heads/main"}`)

	t.Run("MissingSignature", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/webhooks/deploy/test-wh", bytes.NewBuffer(payload))
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for missing signature, got %d", w.Code)
		}
	})

	t.Run("InvalidSignature", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/webhooks/deploy/test-wh", bytes.NewBuffer(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256=invalid-signature-hex")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for invalid signature, got %d", w.Code)
		}
	})

	t.Run("NonExistentWebhook", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/webhooks/deploy/non-existent", bytes.NewBuffer(payload))
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})
}
