//go:build integration
// +build integration

// Package integration provides end-to-end integration tests for dockpal.
// These tests exercise the full HTTP stack with a real database and router.
//
// Run with: go test -tags=integration -race ./internal/integration/...
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// testEnv holds the test environment state.
type testEnv struct {
	DB        *db.DB
	Router    *gin.Engine
	JWTSecret string
}

// setup creates a fresh test environment with a temporary database and router.
func setup(t *testing.T) *testEnv {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	jwtSecret := "integration-test-secret-key-32ch"

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	env := &testEnv{
		DB:        database,
		Router:    router,
		JWTSecret: jwtSecret,
	}

	// Register minimal routes for testing
	env.registerRoutes()

	return env
}

// registerRoutes sets up the minimal route set needed for integration tests.
func (e *testEnv) registerRoutes() {
	api := e.Router.Group("/api")

	// Public routes
	api.POST("/login", func(c *gin.Context) {
		auth.HandleLogin(c, e.JWTSecret, e.DB)
	})

	// Health check (public)
	e.Router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	e.Router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	})
	e.Router.GET("/health/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	// Authenticated routes (simplified middleware)
	authGroup := api.Group("")
	authGroup.Use(e.authMiddleware())
	{
		authGroup.GET("/auth/me", func(c *gin.Context) {
			username := c.GetString("username")
			role := c.GetString("role")
			c.JSON(http.StatusOK, gin.H{
				"username": username,
				"role":     role,
			})
		})
	}
}

// authMiddleware is a simplified JWT auth middleware for integration tests.
func (e *testEnv) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}
		// Strip "Bearer " prefix
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		claims, err := auth.ValidateJWTWithVersionCheck(token, e.JWTSecret, e.DB)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// createUser creates a test user in the database.
func (e *testEnv) createUser(t *testing.T, username, password, role string) {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("Failed to hash password: %v", err)
	}

	user := db.User{
		ID:           fmt.Sprintf("test-%s", username),
		Username:     username,
		PasswordHash: string(hash),
		Role:         role,
		TokenVersion: 0,
	}

	if err := e.DB.CreateUser(user); err != nil {
		t.Fatalf("Failed to create user %q: %v", username, err)
	}
}

// login performs a login request and returns the JWT token.
func (e *testEnv) login(t *testing.T, username, password string) string {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	e.Router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Login failed with status %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse login response: %v", err)
	}

	token, ok := resp["token"].(string)
	if !ok || token == "" {
		t.Fatalf("No token in login response: %v", resp)
	}

	return token
}

// doRequest performs an authenticated HTTP request.
func (e *testEnv) doRequest(t *testing.T, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rr := httptest.NewRecorder()
	e.Router.ServeHTTP(rr, req)
	return rr
}

// parseJSON parses a JSON response body into the given value.
func parseJSON(t *testing.T, rr *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("Failed to parse JSON response (status %d): %v\nBody: %s", rr.Code, err, rr.Body.String())
	}
}
