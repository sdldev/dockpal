package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
)

// LogAudit writes a new audit log entry to the database.
// It automatically extracts username, role, and IP address from the gin.Context if available.
func LogAudit(c *gin.Context, database *db.DB, action, resource, status, details string) {
	// Generate random 8-byte ID (hex encoded, 16 chars)
	randBytes := make([]byte, 8)
	var id string
	if _, err := rand.Read(randBytes); err != nil {
		log.Printf("Error generating ID for audit log: %v", err)
		id = fmt.Sprintf("audit-%d", time.Now().UnixNano())
	} else {
		id = fmt.Sprintf("audit-%s", hex.EncodeToString(randBytes))
	}

	username, _ := c.Get("username")
	usernameStr, ok := username.(string)
	if !ok {
		usernameStr = "system"
	}

	role, _ := c.Get("role")
	roleStr, ok := role.(string)
	if !ok {
		roleStr = "unknown"
	}

	ipAddress := c.ClientIP()

	logEntry := db.AuditLog{
		ID:        id,
		Timestamp: time.Now().Unix(),
		Username:  usernameStr,
		UserRole:  roleStr,
		Action:    action,
		Resource:  resource,
		Status:    status,
		Details:   details,
		IPAddress: ipAddress,
	}

	if err := database.SaveAuditLog(logEntry); err != nil {
		log.Printf("Error saving audit log: %v", err)
	}
}

// handleListAuditLogs returns a paginated list of audit logs.
func handleListAuditLogs(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var query struct {
			Limit  int `form:"limit,default=50"`
			Offset int `form:"offset,default=0"`
		}
		if err := c.ShouldBindQuery(&query); err != nil {
			c.JSON(400, gin.H{"error": "invalid query parameters"})
			return
		}
		if query.Limit <= 0 || query.Limit > 100 {
			query.Limit = 50
		}
		if query.Offset < 0 {
			query.Offset = 0
		}

		logs, total, err := database.ListAuditLogs(query.Limit, query.Offset)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to list audit logs"})
			return
		}

		// Ensure we return empty slice instead of null if logs is nil
		if logs == nil {
			logs = []db.AuditLog{}
		}

		c.JSON(200, gin.H{
			"logs":   logs,
			"total":  total,
			"limit":  query.Limit,
			"offset": query.Offset,
		})
	}
}

// AppUpdateAttemptResult is the categorical outcome of a manual app-update
// trigger captured in the audit log. The value is included verbatim in the
// `result` field of the JSON details payload written by LogAppUpdateAttempt.
type AppUpdateAttemptResult string

const (
	// AuditAppUpdateResultTriggered is recorded when the worker accepted
	// the trigger and the handler returned 202 (a record was persisted).
	AuditAppUpdateResultTriggered AppUpdateAttemptResult = "triggered"

	// AuditAppUpdateResultConflict is recorded when another update for
	// the same app was already running and the handler returned 409.
	AuditAppUpdateResultConflict AppUpdateAttemptResult = "conflict"

	// AuditAppUpdateResultFailed is recorded when the worker returned an
	// unexpected error or the request timed out before a record was
	// persisted (the handler returned 5xx).
	AuditAppUpdateResultFailed AppUpdateAttemptResult = "failed"
)

// AuditActionAppUpdateAttempted is the audit log `action` value written for
// every user-triggered TriggerApp call (R8.4). Auto-update cycles do not
// emit an audit entry by design — only operator-driven trigger requests
// produce one.
const AuditActionAppUpdateAttempted = "app_update_attempted"

// LogAppUpdateAttempt persists an `app_update_attempted` audit log entry
// (R8.4) for one user-triggered TriggerApp call.
//
// The entry's `Resource` is the app (compose project) name and `Status`
// is the categorical result. The `Details` field carries a JSON document
// of the form:
//
//	{"app":"<name>","image":"<comma-separated images>","result":"<triggered|conflict|failed>"}
//
// Username, role, and IP are taken from the gin context by the underlying
// LogAudit helper. Errors persisting the audit log are logged but never
// returned because the trigger response shape must not depend on whether
// audit logging succeeded.
//
// Image collection is best-effort: when a docker client is supplied the
// helper enumerates running containers labelled `dockpal.project=<name>`
// and joins their `Image` fields. If discovery fails (no docker client,
// daemon unreachable, no matching containers) the field is left empty
// rather than blocking the audit write.
func LogAppUpdateAttempt(c *gin.Context, database *db.DB, dockerClient *docker.Client, app string, result AppUpdateAttemptResult) {
	if database == nil || app == "" {
		return
	}

	image := collectAppImagesForAudit(c, dockerClient, app)

	payload := struct {
		App    string `json:"app"`
		Image  string `json:"image"`
		Result string `json:"result"`
	}{
		App:    app,
		Image:  image,
		Result: string(result),
	}
	details, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[audit] failed to marshal app_update_attempted details for %q: %v", app, err)
		return
	}

	LogAudit(c, database, AuditActionAppUpdateAttempted, app, string(result), string(details))
}

// collectAppImagesForAudit returns a comma-separated list of unique image
// references for every container labelled `dockpal.project=<app>` on the
// supplied docker client. Discovery is best-effort: any error returns an
// empty string so the audit entry can still be written.
//
// The lookup uses a 2-second timeout derived from the request context so
// a slow docker daemon never blocks the trigger response. ContainerInfo
// returns the image as plain text (e.g. "nginx:latest" or "repo@sha256:..."),
// which matches the wire format the audit log records.
func collectAppImagesForAudit(c *gin.Context, dockerClient *docker.Client, app string) string {
	if dockerClient == nil {
		return ""
	}

	parent := c.Request.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()

	containers, err := dockerClient.ListContainersWithLabel(ctx, "dockpal.project="+app)
	if err != nil || len(containers) == 0 {
		return ""
	}

	seen := make(map[string]struct{}, len(containers))
	images := make([]string, 0, len(containers))
	for _, ctr := range containers {
		img := ctr.Image
		if img == "" {
			continue
		}
		if _, ok := seen[img]; ok {
			continue
		}
		seen[img] = struct{}{}
		images = append(images, img)
	}

	return strings.Join(images, ",")
}

// auditAppUpdateResultFor maps the HTTP status code returned by the
// trigger handler to the audit `result` value. Any 2xx response is
// treated as a successful trigger; 409 maps to conflict; everything
// else maps to failed.
func auditAppUpdateResultFor(status int) AppUpdateAttemptResult {
	switch {
	case status == 409:
		return AuditAppUpdateResultConflict
	case status >= 200 && status < 300:
		return AuditAppUpdateResultTriggered
	default:
		return AuditAppUpdateResultFailed
	}
}
