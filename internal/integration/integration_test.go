//go:build integration
// +build integration

package integration

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// TestHealthEndpoints verifies all health check endpoints return 200.
func TestHealthEndpoints(t *testing.T) {
	t.Parallel()
	env := setup(t)

	endpoints := []struct {
		path   string
		status int
	}{
		{"/health", http.StatusOK},
		{"/health/live", http.StatusOK},
		{"/health/ready", http.StatusOK},
	}

	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			rr := env.doRequest(t, http.MethodGet, ep.path, "", nil)
			if rr.Code != ep.status {
				t.Errorf("Expected %d, got %d. Body: %s", ep.status, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestAuthLogin validates the full login flow.
func TestAuthLogin(t *testing.T) {
	t.Parallel()
	env := setup(t)

	env.createUser(t, "admin", "TestPass123!", "admin")

	token := env.login(t, "admin", "TestPass123!")
	if token == "" {
		t.Fatal("Expected non-empty JWT token")
	}

	// Verify we can use the token
	rr := env.doRequest(t, http.MethodGet, "/api/auth/me", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	parseJSON(t, rr, &resp)

	if resp["username"] != "admin" {
		t.Errorf("Expected username 'admin', got %v", resp["username"])
	}
	if resp["role"] != "admin" {
		t.Errorf("Expected role 'admin', got %v", resp["role"])
	}
}

// TestAuthLoginInvalidCredentials verifies that wrong credentials are rejected.
func TestAuthLoginInvalidCredentials(t *testing.T) {
	t.Parallel()
	env := setup(t)

	env.createUser(t, "admin", "TestPass123!", "admin")

	tests := []struct {
		name     string
		username string
		password string
		status   int
	}{
		{"wrong password", "admin", "wrong", http.StatusUnauthorized},
		{"wrong username", "nonexistent", "TestPass123!", http.StatusUnauthorized},
		{"empty password", "admin", "", http.StatusBadRequest},
		{"empty username", "", "TestPass123!", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"username":"` + tc.username + `","password":"` + tc.password + `"}`)
			rr := env.doRequest(t, http.MethodPost, "/api/login", "", body)
			if rr.Code != tc.status {
				t.Errorf("Expected %d, got %d. Body: %s", tc.status, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestAuthNoToken verifies that protected endpoints reject unauthenticated requests.
func TestAuthNoToken(t *testing.T) {
	t.Parallel()
	env := setup(t)

	rr := env.doRequest(t, http.MethodGet, "/api/auth/me", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d. Body: %s", rr.Code, rr.Body.String())
	}
}

// TestConcurrentHealthRequests validates that the server handles concurrent
// requests without data races.
func TestConcurrentHealthRequests(t *testing.T) {
	t.Parallel()
	env := setup(t)

	const numGoroutines = 50
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := env.doRequest(t, http.MethodGet, "/health", "", nil)
			if rr.Code != http.StatusOK {
				errs <- fmt.Errorf("expected 200, got %d", rr.Code)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// TestConcurrentAuthRequests validates concurrent authenticated requests.
func TestConcurrentAuthRequests(t *testing.T) {
	t.Parallel()
	env := setup(t)

	env.createUser(t, "admin", "TestPass123!", "admin")
	token := env.login(t, "admin", "TestPass123!")

	const numGoroutines = 30
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := env.doRequest(t, http.MethodGet, "/api/auth/me", token, nil)
			if rr.Code != http.StatusOK {
				errs <- fmt.Errorf("expected 200, got %d", rr.Code)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// TestMultipleUsersConcurrent validates concurrent access with multiple users.
func TestMultipleUsersConcurrent(t *testing.T) {
	t.Parallel()
	env := setup(t)

	// Create users with different roles
	env.createUser(t, "admin", "AdminPass123!", "admin")
	env.createUser(t, "operator", "OperatorPass123!", "operator")
	env.createUser(t, "viewer", "ViewerPass123!", "viewer")

	adminToken := env.login(t, "admin", "AdminPass123!")
	opToken := env.login(t, "operator", "OperatorPass123!")
	viewerToken := env.login(t, "viewer", "ViewerPass123!")

	tokens := []string{adminToken, opToken, viewerToken}

	const numGoroutines = 30
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			token := tokens[idx%len(tokens)]
			rr := env.doRequest(t, http.MethodGet, "/api/auth/me", token, nil)
			if rr.Code != http.StatusOK {
				errs <- fmt.Errorf("goroutine %d: expected 200, got %d", idx, rr.Code)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
