package server

import (
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/registry"
)

// checkOrigin validates WebSocket upgrade requests by comparing
// the Origin header's host against the request's Host header.
// Rejects empty/missing origins and mismatched hosts.
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}

	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}

	return u.Host == r.Host
}

var upgrader = websocket.Upgrader{
	CheckOrigin: checkOrigin,
}

var githubHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	},
}

// wsTextWriter wraps a WebSocket connection as an io.Writer,
// sending each write as a TextMessage. Used to stream demultiplexed
// container logs (stdout + stderr) over a single WebSocket.
type wsTextWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsTextWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.TextMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// streamContainerLogs demultiplexes Docker's stdcopy stream and sends
// plain text over the WebSocket. It blocks until the client disconnects.
func streamContainerLogs(conn *websocket.Conn, reader io.ReadCloser) {
	defer conn.Close()
	defer reader.Close()

	w := &wsTextWriter{conn: conn}

	go func() {
		_, _ = stdcopy.StdCopy(w, w, reader)
		reader.Close()
		conn.Close()
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// globalDeployManager is the shared deploy session manager used by both
// the legacy routes and instance-scoped routes + WebSocket handlers.
var globalDeployManager = docker.NewDeployManager()

// globalAppUpdateFeed is the shared App_Update_Feed broadcaster wired by
// RegisterRoutes (task 5.2). Handler tasks 5.3 (SSE stream) and the agent
// client local impl (task 6.2) read this reference to subscribe.
//
// It is reassigned on every RegisterRoutes call so test setups that build
// fresh routers each iteration always see a fresh feed; the previous feed
// becomes garbage once nothing references it. A nil value means
// RegisterRoutes has not run yet — callers should treat that as a fatal
// configuration error rather than a transient state.
var globalAppUpdateFeed *AppUpdateFeed

// globalAutoUpdateWorker is the shared AutoUpdateWorker wired by
// RegisterRoutes (task 5.2). Handler tasks 5.3 (POST /apps/:name/update,
// PATCH /apps/:name/auto-update) and the agent client local impl (task
// 6.2) call into the worker via this reference.
//
// As with globalAppUpdateFeed it is reassigned on every RegisterRoutes
// call. nil means the worker is not configured for the current process
// (for example because the env-var DOCKPAL_AUTO_UPDATE_ENABLED is false);
// callers should fall back gracefully in that case.
var globalAutoUpdateWorker *docker.AutoUpdateWorker

// globalDockerClient and globalImageUpdateMonitor are shared references
// wired by RegisterRoutes so the instance-scoped handlers (task 5.4) can
// reuse the in-process docker layer when the request targets the "local"
// instance. They are reassigned on every RegisterRoutes call to keep test
// setups isolated.
var (
	globalDockerClient        *docker.Client
	globalImageUpdateMonitor  *docker.ImageUpdateMonitor
	globalRegistryManager     *registry.Manager
)

// SystemInfo contains host hardware metrics and Docker version information.
type SystemInfo struct {
	Hostname      string  `json:"hostname"`
	OS            string  `json:"os"`
	CPUCores      int     `json:"cpu_cores"`
	CPUPercent    float64 `json:"cpu_percent"`
	TotalRAM      uint64  `json:"total_ram"`
	UsedRAM       uint64  `json:"used_ram"`
	TotalDisk     uint64  `json:"total_disk"`
	UsedDisk      uint64  `json:"used_disk"`
	DockerVersion string  `json:"docker_version"`
}

// sanitizeFilename removes CR/LF and quotes from a filename to prevent
// Content-Disposition header injection.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, `"`, "'")
	return name
}

// generateID creates a prefixed, unpredictable ID using crypto/rand.
func generateID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%x", prefix, b)
}

// internalError returns a generic error message to the client while logging
// the real error. This prevents leaking internal details (file paths, DB
// errors, etc.) in API responses.
func internalError(c *gin.Context, err error) {
	log.Printf("[ERROR] %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

// extractFirstPort parses the compose YAML and extracts the first container port
// from the first service that has port bindings. Returns 80 as default if no ports found.
func extractFirstPort(composeYAML string) int {
	cf, err := docker.ParseComposeFile(composeYAML)
	if err != nil {
		return 80
	}
	for _, svc := range cf.Services {
		for _, portSpec := range svc.Ports {
			pb, err := docker.ParsePort(portSpec)
			if err == nil {
				return pb.ContainerPort
			}
		}
	}
	return 80
}

// getRegistryAuths extracts registry authentication headers from the registry manager.
// Returns a map of registry domain to auth header string.
func getRegistryAuths(registryMgr *registry.Manager, composeYAML string) map[string]string {
	if registryMgr == nil {
		return nil
	}
	auths := make(map[string]string)
	for _, domain := range extractDomainsFromCompose(composeYAML) {
		authHeader, err := registryMgr.GetAuthHeader(domain + "/image:latest")
		if err == nil && authHeader != "" {
			auths[domain] = authHeader
		}
	}
	if len(auths) == 0 {
		return nil
	}
	return auths
}

// handleDeployStreamWS authenticates via query param token, validates origin,
// and streams deploy session events over WebSocket.
func handleDeployStreamWS(jwtSecret string, database *db.DB, deployManager *docker.DeployManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Query("token")
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := auth.ValidateJWTWithVersionCheck(token, jwtSecret, database)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if !auth.HasRole(claims.Role, auth.RoleViewer) {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}
		if !checkOrigin(c.Request) {
			c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
			return
		}

		session := deployManager.GetSession(c.Param("id"))
		if session == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "deploy session not found"})
			return
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			select {
			case event, ok := <-session.Events:
				if !ok {
					return
				}
				if err := conn.WriteJSON(event); err != nil {
					return
				}
			case <-session.Done:
				for {
					select {
					case event := <-session.Events:
						conn.WriteJSON(event)
					default:
						return
					}
				}
			}
		}
	}
}
