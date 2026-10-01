package auth

// Unit tests for the auth HTTP handlers (audit-auth H3, T1, T2, T4).
// Pattern: temp BBolt DB + gin.TestMode + httptest, with a stub middleware
// that sets username/role context keys — no Docker client required.

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
	"golang.org/x/crypto/bcrypt"
)

const testSecret = "test-jwt-secret-handler"

func newHandlerTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func seedUser(t *testing.T, database *db.DB, username, password, role string) db.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u := db.User{
		ID:           "id-" + username,
		Username:     username,
		PasswordHash: string(hash),
		Role:         role,
		TokenVersion: 0,
		CreatedAt:    1700000000,
	}
	if err := database.CreateUser(u); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

// withIdentity returns middleware mimicking AuthMiddleware's context keys.
func withIdentity(username, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("username", username)
		c.Set("role", role)
		c.Next()
	}
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// --- HandleLogin ---

func TestHandleLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newHandlerTestDB(t)
	seedUser(t, database, "alice", "correct-horse", RoleViewer)

	setup := func() *gin.Engine {
		r := gin.New()
		r.POST("/login", func(c *gin.Context) { HandleLogin(c, testSecret, database) })
		return r
	}

	t.Run("bad request body → 400", func(t *testing.T) {
		w := doJSON(t, setup(), http.MethodPost, "/login", map[string]string{})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("unknown username → 401", func(t *testing.T) {
		w := doJSON(t, setup(), http.MethodPost, "/login", map[string]string{
			"username": "nobody", "password": "whatever123",
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("wrong password → 401", func(t *testing.T) {
		w := doJSON(t, setup(), http.MethodPost, "/login", map[string]string{
			"username": "alice", "password": "wrong-password",
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("correct credentials → 200 with token and role", func(t *testing.T) {
		w := doJSON(t, setup(), http.MethodPost, "/login", map[string]string{
			"username": "alice", "password": "correct-horse",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Role     string `json:"role"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Token == "" || resp.Username != "alice" || resp.Role != RoleViewer {
			t.Fatalf("unexpected login response: %+v", resp)
		}
		// Token must validate against the DB-backed version check.
		if _, err := ValidateJWTWithVersionCheck(resp.Token, testSecret, database); err != nil {
			t.Fatalf("issued token rejected: %v", err)
		}
	})
}

// --- HandleLogout + end-to-end revocation (T1 + T2) ---

func TestHandleLogout_RevokesTokenEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newHandlerTestDB(t)
	user := seedUser(t, database, "bob", "password-123", RoleOperator)

	// Issue a token at the CURRENT DB version (T1: never hardcode a version).
	token, err := GenerateJWT(user.ID, user.Username, testSecret, user.Role, user.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateJWTWithVersionCheck(token, testSecret, database); err != nil {
		t.Fatalf("precondition: token should be valid before logout: %v", err)
	}

	// Logout through the handler, carrying the identity like AuthMiddleware sets.
	r := gin.New()
	r.POST("/logout", withIdentity(user.Username, user.Role), func(c *gin.Context) {
		HandleLogout(c, database)
	})
	w := doJSON(t, r, http.MethodPost, "/logout", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("logout: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The same token must now be rejected (T2).
	if _, err := ValidateJWTWithVersionCheck(token, testSecret, database); err == nil {
		t.Fatal("token still accepted after logout — revocation did not take effect")
	}
}

// --- HandleResetPassword ---

func TestHandleResetPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)

	setup := func(database *db.DB, username string) *gin.Engine {
		r := gin.New()
		r.POST("/reset", withIdentity(username, RoleViewer), func(c *gin.Context) {
			HandleResetPassword(c, database)
		})
		return r
	}

	t.Run("too short → 400", func(t *testing.T) {
		database := newHandlerTestDB(t)
		seedUser(t, database, "carol", "old-password-1", RoleViewer)
		w := doJSON(t, setup(database, "carol"), http.MethodPost, "/reset", map[string]string{
			"new_password": "short",
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("over bcrypt 72-byte limit → 400 (not opaque 500)", func(t *testing.T) {
		database := newHandlerTestDB(t)
		seedUser(t, database, "carol", "old-password-1", RoleViewer)
		w := doJSON(t, setup(database, "carol"), http.MethodPost, "/reset", map[string]string{
			"new_password": strings.Repeat("a", 100),
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("success → hash changes, token version increments", func(t *testing.T) {
		database := newHandlerTestDB(t)
		before := seedUser(t, database, "carol", "old-password-1", RoleViewer)
		oldToken, err := GenerateJWT(before.ID, before.Username, testSecret, before.Role, before.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}

		w := doJSON(t, setup(database, "carol"), http.MethodPost, "/reset", map[string]string{
			"new_password": "new-password-456",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		after, err := database.GetUser("carol")
		if err != nil {
			t.Fatal(err)
		}
		if after.PasswordHash == before.PasswordHash {
			t.Fatal("password hash unchanged after reset")
		}
		if after.TokenVersion != before.TokenVersion+1 {
			t.Fatalf("token version = %d, want %d", after.TokenVersion, before.TokenVersion+1)
		}
		// Old token revoked.
		if _, err := ValidateJWTWithVersionCheck(oldToken, testSecret, database); err == nil {
			t.Fatal("old token still valid after password reset")
		}
		// New password works for login path.
		if err := bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte("new-password-456")); err != nil {
			t.Fatal("new password does not verify")
		}
	})
}

// --- HandleListUsers ---

func TestHandleListUsers_EmptyReturnsJSONArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newHandlerTestDB(t)

	r := gin.New()
	r.GET("/users", func(c *gin.Context) { HandleListUsers(c, database) })
	w := doJSON(t, r, http.MethodGet, "/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Must be a JSON array (never null) so the SPA's {#each} doesn't throw.
	if body := strings.TrimSpace(w.Body.String()); body != "[]" {
		t.Fatalf("expected [], got %q", body)
	}
}

// --- HandleUpdateUserRole (incl. T4: demotion revokes old admin token) ---

func TestHandleUpdateUserRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	setup := func(database *db.DB, caller string) *gin.Engine {
		r := gin.New()
		r.PUT("/users/:username/role", withIdentity(caller, RoleAdmin), func(c *gin.Context) {
			HandleUpdateUserRole(c, database)
		})
		return r
	}

	t.Run("self role change → 403", func(t *testing.T) {
		database := newHandlerTestDB(t)
		seedUser(t, database, "admin1", "password-12345", RoleAdmin)
		w := doJSON(t, setup(database, "admin1"), http.MethodPut, "/users/admin1/role", map[string]string{"role": RoleViewer})
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid role → 400", func(t *testing.T) {
		database := newHandlerTestDB(t)
		seedUser(t, database, "admin1", "password-12345", RoleAdmin)
		seedUser(t, database, "dave", "password-12345", RoleViewer)
		w := doJSON(t, setup(database, "admin1"), http.MethodPut, "/users/dave/role", map[string]string{"role": "superadmin"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("unknown target → 404", func(t *testing.T) {
		database := newHandlerTestDB(t)
		seedUser(t, database, "admin1", "password-12345", RoleAdmin)
		w := doJSON(t, setup(database, "admin1"), http.MethodPut, "/users/ghost/role", map[string]string{"role": RoleViewer})
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("demote admin → old token rejected, role persisted (T4)", func(t *testing.T) {
		database := newHandlerTestDB(t)
		seedUser(t, database, "admin1", "password-12345", RoleAdmin)
		target := seedUser(t, database, "erin", "password-12345", RoleAdmin)

		adminToken, err := GenerateJWT(target.ID, target.Username, testSecret, RoleAdmin, target.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}

		w := doJSON(t, setup(database, "admin1"), http.MethodPut, "/users/erin/role", map[string]string{"role": RoleViewer})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		after, err := database.GetUser("erin")
		if err != nil {
			t.Fatal(err)
		}
		if after.Role != RoleViewer {
			t.Fatalf("role = %q, want viewer", after.Role)
		}
		// (a) old admin token must be rejected (version bumped with the role write)
		if _, err := ValidateJWTWithVersionCheck(adminToken, testSecret, database); err == nil {
			t.Fatal("stale admin token still accepted after demotion")
		}
		// (b) a fresh token carries the NEW role
		freshToken, err := GenerateJWT(after.ID, after.Username, testSecret, after.Role, after.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := ValidateJWTWithVersionCheck(freshToken, testSecret, database)
		if err != nil {
			t.Fatalf("fresh token rejected: %v", err)
		}
		if claims.Role != RoleViewer {
			t.Fatalf("fresh token role = %q, want viewer", claims.Role)
		}
	})
}

// --- HandleChangePassword ---

func TestHandleChangePassword(t *testing.T) {
	gin.SetMode(gin.TestMode)

	setup := func(database *db.DB, username string) *gin.Engine {
		r := gin.New()
		r.PUT("/password", withIdentity(username, RoleViewer), func(c *gin.Context) {
			HandleChangePassword(c, database)
		})
		return r
	}

	t.Run("wrong current password → 401", func(t *testing.T) {
		database := newHandlerTestDB(t)
		seedUser(t, database, "frank", "current-pw-789", RoleViewer)
		w := doJSON(t, setup(database, "frank"), http.MethodPut, "/password", map[string]string{
			"current_password": "not-the-current",
			"new_password":     "brand-new-pw-1",
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("over bcrypt limit → 400", func(t *testing.T) {
		database := newHandlerTestDB(t)
		seedUser(t, database, "frank", "current-pw-789", RoleViewer)
		w := doJSON(t, setup(database, "frank"), http.MethodPut, "/password", map[string]string{
			"current_password": "current-pw-789",
			"new_password":     strings.Repeat("b", 80),
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("success → hash changes and old sessions revoked", func(t *testing.T) {
		database := newHandlerTestDB(t)
		before := seedUser(t, database, "frank", "current-pw-789", RoleViewer)
		w := doJSON(t, setup(database, "frank"), http.MethodPut, "/password", map[string]string{
			"current_password": "current-pw-789",
			"new_password":     "brand-new-pw-1",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		after, err := database.GetUser("frank")
		if err != nil {
			t.Fatal(err)
		}
		if after.PasswordHash == before.PasswordHash || after.TokenVersion != before.TokenVersion+1 {
			t.Fatalf("password/version not updated: before=%+v after=%+v", before, after)
		}
	})
}

// --- HandleGetProfile ---

func TestHandleGetProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newHandlerTestDB(t)
	seedUser(t, database, "gina", "password-12345", RoleOperator)

	r := gin.New()
	r.GET("/profile", withIdentity("gina", RoleOperator), func(c *gin.Context) {
		HandleGetProfile(c, database)
	})
	w := doJSON(t, r, http.MethodGet, "/profile", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["username"] != "gina" || resp["role"] != RoleOperator {
		t.Fatalf("unexpected profile: %v", resp)
	}
	if _, leaked := resp["password_hash"]; leaked {
		t.Fatal("profile response leaks password hash")
	}
}
