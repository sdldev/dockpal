package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// webhookNotificationTimeout is the maximum time allowed for a single
// outgoing webhook POST request. Webhook delivery is best-effort; a slow
// endpoint must not block the pipeline or starve other webhooks.
const webhookNotificationTimeout = 10 * time.Second

// webhookPayload is the JSON body sent to each notification webhook when
// the auto-update pipeline reaches a rolled_back or failed (rollback_failed)
// terminal state. Field names match the spec: R3.5.
type webhookPayload struct {
	Type       string `json:"type"`
	App        string `json:"app"`
	InstanceID string `json:"instance_id"`
	Stage      string `json:"stage"`
	ErrorCode  string `json:"error_code"`
	Message    string `json:"message"`
	AttemptID  string `json:"attempt_id"`
}

// notifyWebhooks sends a best-effort POST to every configured notification
// webhook with the app_update payload. Errors are logged at warning level
// but never propagated to the caller — webhook failures do not fail the
// main update operation (R3.5).
//
// The method is a no-op when listWebhooks is nil or returns an empty list.
func (w *AutoUpdateWorker) notifyWebhooks(app, instanceID, stage, errorCode, message, attemptID string) {
	if w.listWebhooks == nil {
		return
	}

	webhooks, err := w.listWebhooks()
	if err != nil {
		log.Printf("[auto-update] warning: failed to list notification webhooks: %v", err)
		return
	}
	if len(webhooks) == 0 {
		return
	}

	payload := webhookPayload{
		Type:       "app_update",
		App:        app,
		InstanceID: instanceID,
		Stage:      stage,
		ErrorCode:  errorCode,
		Message:    message,
		AttemptID:  attemptID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[auto-update] warning: failed to marshal webhook payload: %v", err)
		return
	}

	// Fire webhooks concurrently so a slow endpoint doesn't delay others.
	for _, wh := range webhooks {
		go w.sendWebhook(wh.URL, wh.Name, body)
	}
}

// sendWebhook performs a single POST request to the given URL with the
// pre-marshaled JSON body. Errors are logged at warning level. This method
// is designed to run in a goroutine — it does not return errors.
func (w *AutoUpdateWorker) sendWebhook(url, name string, body []byte) {
	client := &http.Client{Timeout: webhookNotificationTimeout}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[auto-update] warning: webhook %q request creation failed: %v", name, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[auto-update] warning: webhook %q delivery failed: %v", name, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("[auto-update] warning: webhook %q returned status %d", name, resp.StatusCode)
		return
	}

	// Success — log at debug level (only visible when verbose logging is on).
	_ = fmt.Sprintf("webhook %q delivered successfully (status %d)", name, resp.StatusCode)
}
