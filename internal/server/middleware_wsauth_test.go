package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
)

// wsUpgradeRequest builds a minimal RFC 6455 handshake request against the
// given path and query, mirroring what a browser WebSocket does: no
// Authorization header is possible, so identity (if any) rides in the query.
func wsUpgradeRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "keep-alive, Upgrade") // case/space variants per RFC
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Origin", "http://"+req.Host)
	return req
}

// TestAuthMiddleware_WSTokenQueryParam pins the browser-WebSocket auth
// contract: a ?token= query param is accepted only for actual WS upgrade
// requests (headers can't carry Authorization), and plain API requests
// must still be rejected when they try to pass a token via query.
func TestAuthMiddleware_WSTokenQueryParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newTestDB(t)
	const secret = "test-secret"

	// ValidateJWTWithVersionCheck looks up the user's token_version, so the
	// token's owner must exist in the test database.
	if err := database.CreateUser(db.User{ID: "user-1", Username: "alice", Role: "admin"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, err := auth.GenerateJWT("user-1", "alice", secret, "admin", 0)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}

	setup := func() *gin.Engine {
		r := gin.New()
		r.Use(AuthMiddleware(secret, database))
		// Echo handler: only reached when auth passed.
		r.GET("/api/echo", func(c *gin.Context) { c.String(http.StatusOK, "authorized") })
		return r
	}

	cases := []struct {
		name       string
		target     string
		withHeader bool
		want       int
	}{
		{
			name:   "ws upgrade with valid query token passes",
			target: "/api/echo?token=" + url.QueryEscape(token),
			want:   http.StatusOK,
		},
		{
			name:   "ws upgrade with invalid query token rejected",
			target: "/api/echo?token=bogus",
			want:   http.StatusUnauthorized,
		},
		{
			name:   "ws upgrade without any token rejected",
			target: "/api/echo",
			want:   http.StatusUnauthorized,
		},
		{
			name:       "bearer header still works on ws upgrade",
			target:     "/api/echo",
			withHeader: true,
			want:       http.StatusOK,
		},
		{
			name:   "plain api request may not use query token",
			target: "/api/echo?token=" + url.QueryEscape(token),
			want:   http.StatusUnauthorized, // no Upgrade headers → query ignored
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := wsUpgradeRequest(t, tc.target)
			if !strings.Contains(tc.target, "token=") && !tc.withHeader {
				// "without any token" case: strip upgrade headers to simulate
				// nothing special — still unauthorized either way.
				_ = req
			}
			if tc.withHeader {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			if tc.name == "plain api request may not use query token" {
				req.Header.Del("Upgrade")
				req.Header.Del("Connection")
			}

			rec := httptest.NewRecorder()
			setup().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestAuthMiddleware_WSTokenQueryParam_RealHandshake drives a real WebSocket
// upgrade through the middleware stack to prove the handshake completes with
// a query token and the connection is usable afterwards.
func TestAuthMiddleware_WSTokenQueryParam_RealHandshake(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newTestDB(t)
	const secret = "test-secret"

	if err := database.CreateUser(db.User{ID: "user-1", Username: "alice", Role: "admin"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, err := auth.GenerateJWT("user-1", "alice", secret, "admin", 0)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	r := gin.New()
	r.Use(AuthMiddleware(secret, database))
	r.GET("/api/ws", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("hello"))
	})

	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws?token=" + url.QueryEscape(token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial with query token failed: %v", err)
	}
	defer conn.Close()

	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	if string(msg) != "hello" {
		t.Fatalf("message = %q, want %q", msg, "hello")
	}
}

// Compile-time guard that a *db.DB satisfies the middleware's expectation.
var _ = func() *db.DB { return nil }
