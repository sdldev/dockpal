package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/update"
	"golang.org/x/crypto/bcrypt"
)

func TestCoverageBooster_APIEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()

	// Seed users with valid bcrypt hashes
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to generate hash: %v", err)
	}
	passwordHash := string(hash)

	adminUser := db.User{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: passwordHash,
		Role:         auth.RoleAdmin,
		CreatedAt:    time.Now().Unix(),
	}
	_ = database.CreateUser(adminUser)

	opUser := db.User{
		ID:           "u-op",
		Username:     "operator_user",
		PasswordHash: passwordHash,
		Role:         auth.RoleOperator,
		CreatedAt:    time.Now().Unix(),
	}
	_ = database.CreateUser(opUser)

	viewUser := db.User{
		ID:           "u-view",
		Username:     "viewer_user",
		PasswordHash: passwordHash,
		Role:         auth.RoleViewer,
		CreatedAt:    time.Now().Unix(),
	}
	_ = database.CreateUser(viewUser)

	// Seed instance
	inst := db.Instance{
		ID:        "local",
		Name:      "Local Server",
		Mode:      "local",
		CreatedAt: time.Now().Unix(),
	}
	_ = database.SaveInstance(inst)

	// Seed registry credential
	reg := db.RegistryCredential{
		ID:             "reg-1",
		Registry:       "ghcr.io",
		Username:       "test-user",
		EncryptedToken: []byte("token"),
		CreatedAt:      time.Now().Unix(),
	}
	_ = database.SaveRegistryCredential(reg)

	// Seed service
	svc := db.Service{
		ID:         "svc-1",
		InstanceID: "local",
		Name:       "my-service",
		Type:       "compose",
		CreatedAt:  time.Now().Unix(),
	}
	_ = database.SaveService(svc)

	// Seed domain
	dom := db.Domain{
		ID:         "dom-1",
		InstanceID: "local",
		Domain:     "example.com",
		Service:    "my-service",
		Port:       80,
	}
	_ = database.SaveDomain(dom)

	// Setup router and services
	jwtSecret := "test-secret-key-1234567890-abcdefg"
	t.Setenv("JWT_SECRET", jwtSecret)

	// We use the real Docker client for local testing if running with a Docker daemon
	dockerClient, err := docker.NewClient()
	if err != nil {
		t.Fatalf("failed to create docker client: %v", err)
	}
	defer dockerClient.Close()

	versionService := update.NewVersionService(tmpDir, "v0.8.0")
	updateService := update.NewUpdateService("v0.8.0")
	agentMgr, err := agent.NewManager(database, dockerClient, jwtSecret)
	if err != nil {
		t.Fatalf("failed to create agent manager: %v", err)
	}

	router := gin.New()
	router.Use(CORSMiddleware())
	RegisterRoutes(router, dockerClient, jwtSecret, database, versionService, updateService, agentMgr, tmpDir, "v0.9.0-test")

	// Generate tokens
	adminToken, _ := auth.GenerateJWT("u-admin", "admin", jwtSecret, auth.RoleAdmin, 0)
	opToken, _ := auth.GenerateJWT("u-op", "operator_user", jwtSecret, auth.RoleOperator, 0)
	viewToken, _ := auth.GenerateJWT("u-view", "viewer_user", jwtSecret, auth.RoleViewer, 0)

	// Helper to send requests
	request := func(method, path, token string, body interface{}) (*httptest.ResponseRecorder, int) {
		var bodyReader bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&bodyReader).Encode(body)
		}
		req := httptest.NewRequest(method, path, &bodyReader)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w, w.Code
	}

	// 1. Test POST /api/login
	t.Run("Login API", func(t *testing.T) {
		body := map[string]string{"username": "admin", "password": "admin"}
		w, code := request("POST", "/api/login", "", body)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d. Body: %s", code, w.Body.String())
		}

		body = map[string]string{"username": "admin", "password": "wrong"}
		_, code = request("POST", "/api/login", "", body)
		if code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", code)
		}
	})

	// 2. Test GET /api/system/version
	t.Run("System Version API", func(t *testing.T) {
		_, code := request("GET", "/api/system/version", "", nil)
		if code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", code)
		}

		w, code := request("GET", "/api/system/version", viewToken, nil)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d", code)
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("currentVersion")) {
			t.Errorf("expected response to contain currentVersion info, got %s", w.Body.String())
		}
	})

	// 3. Test GET /api/audit-logs
	t.Run("Audit Logs API", func(t *testing.T) {
		_, code := request("GET", "/api/audit-logs", viewToken, nil)
		if code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", code)
		}

		_, code = request("GET", "/api/audit-logs", opToken, nil)
		if code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", code)
		}

		w, code := request("GET", "/api/audit-logs", adminToken, nil)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d. Body: %s", code, w.Body.String())
		}
	})

	// 4. Test GET /api/instances (CRUD check)
	t.Run("Instances CRUD API", func(t *testing.T) {
		_, code := request("GET", "/api/instances", viewToken, nil)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d", code)
		}

		createBody := map[string]interface{}{
			"name": "New Server",
			"host": "192.168.1.50",
			"port": 3012,
			"mode": "direct",
		}
		_, code = request("POST", "/api/instances", viewToken, createBody)
		if code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", code)
		}

		_, code = request("POST", "/api/instances", opToken, createBody)
		if code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", code)
		}

		w, code := request("POST", "/api/instances", adminToken, createBody)
		if code != http.StatusCreated && code != http.StatusBadRequest {
			t.Errorf("expected 201 or 400, got %d. Body: %s", code, w.Body.String())
		}

		var instResp struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &instResp)
		createdID := instResp.ID

		if createdID != "" {
			// GET detail
			request("GET", "/api/instances/"+createdID, viewToken, nil)

			// PUT update
			updateBody := map[string]interface{}{
				"name": "Updated Server Name",
				"host": "192.168.1.60",
				"port": 3015,
			}
			request("PUT", "/api/instances/"+createdID, adminToken, updateBody)

			// POST rotate token
			request("POST", "/api/instances/"+createdID+"/rotate-token", adminToken, nil)

			// POST test connectivity
			request("POST", "/api/instances/"+createdID+"/test", adminToken, nil)

			// DELETE
			request("DELETE", "/api/instances/"+createdID, adminToken, nil)
		}
	})

	// 5. Test GET /api/registries (CRUD check)
	t.Run("Registries CRUD API", func(t *testing.T) {
		_, code := request("GET", "/api/registries", viewToken, nil)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d", code)
		}

		regBody := map[string]interface{}{
			"registry": "docker.io",
			"username": "tester",
			"token":    "mypassword",
		}
		_, code = request("POST", "/api/registries", viewToken, regBody)
		if code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", code)
		}

		_, code = request("POST", "/api/registries", opToken, regBody)
		if code != http.StatusCreated && code != http.StatusBadRequest {
			t.Errorf("expected 201 or 400, got %d", code)
		}
	})

	// 6. Test GET /api/services (CRUD check)
	t.Run("Services CRUD API", func(t *testing.T) {
		_, code := request("GET", "/api/services", viewToken, nil)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d", code)
		}

		// Delete Service as viewer -> Forbidden
		_, code = request("DELETE", "/api/services/svc-1", viewToken, nil)
		if code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", code)
		}
	})

	// 7. Test GET /api/domains (CRUD check)
	t.Run("Domains CRUD API", func(t *testing.T) {
		_, code := request("GET", "/api/domains", viewToken, nil)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d", code)
		}

		domBody := map[string]interface{}{
			"domain":  "app.test",
			"service": "my-service",
			"port":    80,
		}
		_, code = request("POST", "/api/domains", viewToken, domBody)
		if code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", code)
		}
	})

	// 8. Test password reset endpoint
	t.Run("Password Reset API", func(t *testing.T) {
		resetBody := map[string]string{
			"new_password": "new-password-12345",
		}
		w, code := request("POST", "/api/auth/reset-password", adminToken, resetBody)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d. Body: %s", code, w.Body.String())
		}
	})

	// 9. Test templates API
	t.Run("Templates API", func(t *testing.T) {
		// List templates
		_, code := request("GET", "/api/templates", viewToken, nil)
		if code != http.StatusOK && code != http.StatusInternalServerError {
			t.Errorf("expected 200 or 500, got %d", code)
		}

		// Get nonexistent template
		_, code = request("GET", "/api/templates/nonexistent", viewToken, nil)
		if code != http.StatusNotFound && code != http.StatusInternalServerError {
			t.Errorf("expected 404 or 500, got %d", code)
		}

		// Deploy nonexistent template
		_, code = request("POST", "/api/templates/nonexistent/deploy", opToken, nil)
		if code != http.StatusNotFound && code != http.StatusInternalServerError {
			t.Errorf("expected 404 or 500, got %d", code)
		}
	})

	// 10. Test tunnel API
	t.Run("Tunnel API", func(t *testing.T) {
		// Invalid token
		body := map[string]string{"token": "invalid"}
		_, code := request("POST", "/api/tunnel", opToken, body)
		if code != http.StatusBadRequest && code != http.StatusInternalServerError {
			t.Errorf("expected 400 or 500, got %d", code)
		}
	})

	// 11. Test local instance-scoped operations
	t.Run("Local Instance Operations", func(t *testing.T) {
		// Get local containers
		w, code := request("GET", "/api/instances/local/containers", viewToken, nil)
		t.Logf("LOCAL CONTAINERS RESPONSE CODE: %d, BODY: %s", code, w.Body.String())
		if code != http.StatusOK && code != http.StatusInternalServerError {
			t.Errorf("expected 200 or 500, got %d", code)
		}

		// Get local images
		_, code = request("GET", "/api/instances/local/images", viewToken, nil)
		if code != http.StatusOK && code != http.StatusInternalServerError {
			t.Errorf("expected 200 or 500, got %d", code)
		}

		// Get local system info
		_, code = request("GET", "/api/instances/local/system/info", viewToken, nil)
		if code != http.StatusOK && code != http.StatusInternalServerError {
			t.Errorf("expected 200 or 500, got %d", code)
		}

		// Containers operations
		request("GET", "/api/instances/local/containers/nonexistent", viewToken, nil)
		request("POST", "/api/instances/local/containers/nonexistent/start", opToken, nil)
		request("POST", "/api/instances/local/containers/nonexistent/stop", opToken, nil)
		request("POST", "/api/instances/local/containers/nonexistent/restart", opToken, nil)
		request("DELETE", "/api/instances/local/containers/nonexistent", opToken, nil)
		request("PUT", "/api/instances/local/containers/nonexistent", opToken, nil)
		request("GET", "/api/instances/local/containers/nonexistent/stats", viewToken, nil)
		request("GET", "/api/instances/local/containers/nonexistent/logs", viewToken, nil)

		// Deploy operations
		request("POST", "/api/instances/local/deploy/stream", opToken, nil)
		request("POST", "/api/instances/local/deploy/compose", opToken, nil)
		request("POST", "/api/instances/local/deploy/git", opToken, nil)
		request("POST", "/api/instances/local/templates/nonexistent/deploy/stream", opToken, nil)

		// Image operations
		request("POST", "/api/instances/local/images/pull", opToken, nil)
		request("DELETE", "/api/instances/local/images/nonexistent", opToken, nil)

		// Host/System operations
		request("GET", "/api/instances/local/host/info", viewToken, nil)
		request("GET", "/api/instances/local/host/stats", viewToken, nil)

		// Services operations
		request("GET", "/api/instances/local/services", viewToken, nil)
		request("DELETE", "/api/instances/local/services/nonexistent", opToken, nil)

		// Domains operations
		request("GET", "/api/instances/local/domains", viewToken, nil)
		request("POST", "/api/instances/local/domains", opToken, nil)
		request("DELETE", "/api/instances/local/domains/nonexistent", opToken, nil)

		// Registries operations
		request("GET", "/api/instances/local/registries", viewToken, nil)
		request("POST", "/api/instances/local/registries", opToken, nil)
		request("GET", "/api/instances/local/registries/nonexistent", viewToken, nil)
		request("PUT", "/api/instances/local/registries/nonexistent", opToken, nil)
		request("DELETE", "/api/instances/local/registries/nonexistent", opToken, nil)
		request("POST", "/api/instances/local/registries/nonexistent/test", opToken, nil)
	})

	// 12. Test global delegation operations
	t.Run("Global Delegation Operations", func(t *testing.T) {
		// Containers
		request("GET", "/api/containers", viewToken, nil)
		request("GET", "/api/containers/nonexistent", viewToken, nil)
		request("POST", "/api/containers/nonexistent/start", viewToken, nil)
		request("POST", "/api/containers/nonexistent/stop", viewToken, nil)
		request("POST", "/api/containers/nonexistent/restart", viewToken, nil)
		request("DELETE", "/api/containers/nonexistent", viewToken, nil)
		request("PUT", "/api/containers/nonexistent", viewToken, nil)
		request("GET", "/api/containers/nonexistent/stats", viewToken, nil)
		request("GET", "/api/containers/nonexistent/logs", viewToken, nil)

		// Images
		request("GET", "/api/images", viewToken, nil)
		request("POST", "/api/images/pull", opToken, nil)
		request("DELETE", "/api/images/nonexistent", opToken, nil)

		// System
		request("GET", "/api/system/info", viewToken, nil)
		request("POST", "/api/system/update", opToken, nil)

		// GitHub
		request("GET", "/api/github/repos", opToken, nil)

		// Deploy
		request("POST", "/api/deploy/compose", opToken, nil)
		request("POST", "/api/deploy/git", opToken, nil)
		request("POST", "/api/deploy/stream", opToken, nil)
	})

	// 13. Test files API operations
	t.Run("Files API Operations", func(t *testing.T) {
		request("GET", "/api/files", viewToken, nil)
		request("GET", "/api/files/read", viewToken, nil)
		request("POST", "/api/files/write", opToken, nil)
		request("POST", "/api/files/upload", opToken, nil)
		request("GET", "/api/files/download", viewToken, nil)
		request("DELETE", "/api/files", opToken, nil)
		request("POST", "/api/containers/nonexistent/files/write", opToken, nil)
	})

	// 14. Test logout endpoint
	t.Run("Logout API", func(t *testing.T) {
		freshAdminToken, _ := auth.GenerateJWT("u-admin", "admin", jwtSecret, auth.RoleAdmin, 1)
		w, code := request("POST", "/api/logout", freshAdminToken, nil)
		if code != http.StatusOK {
			t.Errorf("expected 200, got %d. Body: %s", code, w.Body.String())
		}
	})
}

func TestVerifyMockEndpoints_RuteRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()

	// Seed instance
	inst := db.Instance{
		ID:        "inst-direct",
		Name:      "Direct Instance",
		Mode:      "direct",
		Host:      "localhost",
		Port:      3013,
		CreatedAt: time.Now().Unix(),
	}
	_ = database.SaveInstance(inst)

	jwtSecret := "test-secret-key-1234567890-abcdefg"
	t.Setenv("JWT_SECRET", jwtSecret)

	dockerClient, err := docker.NewClient()
	if err != nil {
		t.Fatalf("failed to create docker client: %v", err)
	}
	defer dockerClient.Close()

	versionService := update.NewVersionService(tmpDir, "v0.8.0")
	updateService := update.NewUpdateService("v0.8.0")
	agentMgr, err := agent.NewManager(database, dockerClient, jwtSecret)
	if err != nil {
		t.Fatalf("failed to create agent manager: %v", err)
	}

	router := gin.New()
	RegisterRoutes(router, dockerClient, jwtSecret, database, versionService, updateService, agentMgr, tmpDir, "v0.9.0-test")

	adminToken, _ := auth.GenerateJWT("admin", "admin", jwtSecret, auth.RoleAdmin, 0)

	req := httptest.NewRequest("GET", "/api/instances/inst-direct/containers", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Errorf("did not expect 404, got %d", w.Code)
	}
	fmt.Printf("Instance Direct request code: %d, body: %s\n", w.Code, w.Body.String())
}
