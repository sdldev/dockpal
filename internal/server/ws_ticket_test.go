package server

// Tests for the single-use WS ticket flow (audit-auth L1).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
)

func TestWSTicketLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newTestDB(t)
	if err := database.CreateUser(db.User{ID: "user-1", Username: "alice", Role: "operator"}); err != nil {
		t.Fatal(err)
	}

	// Issue endpoint (identity injected as AuthMiddleware would).
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("username", "alice")
		c.Set("role", "operator")
		c.Next()
	})
	r.GET("/ws-ticket", handleWSTicket)

	req := httptest.NewRequest(http.MethodGet, "/ws-ticket", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Ticket == "" {
		t.Fatal("empty ticket")
	}

	// The ticket authenticates a WS-upgrade request through AuthMiddleware,
	// carrying the issuer's identity.
	protected := gin.New()
	protected.Use(AuthMiddleware("test-secret", database))
	protected.GET("/api/echo", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"username": c.GetString("username"),
			"role":     c.GetString("role"),
		})
	})

	wsReq := wsUpgradeRequest(t, "/api/echo?token="+url.QueryEscape(resp.Ticket))
	wsRec := httptest.NewRecorder()
	protected.ServeHTTP(wsRec, wsReq)
	if wsRec.Code != http.StatusOK {
		t.Fatalf("ws upgrade with ticket: %d %s", wsRec.Code, wsRec.Body.String())
	}
	var identity struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(wsRec.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Username != "alice" || identity.Role != "operator" {
		t.Fatalf("ticket identity = %+v, want alice/operator", identity)
	}

	// Single-use: the same ticket must be rejected on reuse.
	wsReq2 := wsUpgradeRequest(t, "/api/echo?token="+url.QueryEscape(resp.Ticket))
	wsRec2 := httptest.NewRecorder()
	protected.ServeHTTP(wsRec2, wsReq2)
	if wsRec2.Code != http.StatusUnauthorized {
		t.Fatalf("reused ticket: got %d, want 401", wsRec2.Code)
	}
}

func TestWSTicketExpiry(t *testing.T) {
	// Plant an already-expired ticket directly.
	wsTickets.mu.Lock()
	wsTickets.entries[testTicketHash("expired-ticket")] = wsTicket{username: "bob", expires: time.Now().Add(-time.Second)}
	wsTickets.mu.Unlock()

	if _, ok := consumeWSTicket("expired-ticket"); ok {
		t.Fatal("expired ticket accepted")
	}
}

func testTicketHash(ticket string) string {
	sum := sha256.Sum256([]byte(ticket))
	return hex.EncodeToString(sum[:])
}
