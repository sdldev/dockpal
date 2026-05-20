package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookCRUD(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_webhooks.db")

	database, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer database.Close()

	wh := Webhook{
		ID:         "test-webhook-id",
		InstanceID: "inst-1",
		Name:       "my-service",
		Repo:       "https://github.com/user/repo",
		Branch:     "main",
		Secret:     "secret-key",
		CreatedAt:  time.Now().Unix(),
	}

	// 1. Create
	err = database.CreateWebhook(wh)
	if err != nil {
		t.Fatalf("CreateWebhook failed: %v", err)
	}

	// 2. Get
	fetched, err := database.GetWebhook("test-webhook-id")
	if err != nil {
		t.Fatalf("GetWebhook failed: %v", err)
	}
	if fetched.Name != wh.Name || fetched.Repo != wh.Repo || fetched.Secret != wh.Secret {
		t.Errorf("fetched webhook doesn't match original")
	}

	// 3. List
	list, err := database.ListWebhooks()
	if err != nil {
		t.Fatalf("ListWebhooks failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(list))
	}

	// 4. Delete
	err = database.DeleteWebhook("test-webhook-id")
	if err != nil {
		t.Fatalf("DeleteWebhook failed: %v", err)
	}

	// 5. Get non-existent
	_, err = database.GetWebhook("test-webhook-id")
	if err == nil {
		t.Errorf("expected error fetching deleted webhook, got nil")
	}
}
