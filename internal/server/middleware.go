package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
)

// authFailureLimiter throttles repeated authentication failures per client
// IP — failed auth previously consumed no rate-limit budget at all, so
// online guessing was unthrottled (audit-auth L4). Successful requests are
// not counted; this only bites on 401s.
var authFailureLimiter = NewRateLimiterWithPolicy(RateLimitPolicy{Window: rateLimitWindow, MaxRequests: 20})

// rejectAuth responds 401 while charging the client against the auth-failure
// limiter; when the limiter is exhausted it responds 429 instead.
func rejectAuth(c *gin.Context, message string) {
	if allowed, retryAfter := authFailureLimiter.Allow(c.ClientIP()); !allowed {
		c.Header("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed authentication attempts"})
		c.Abort()
		return
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": message})
	c.Abort()
}

func AuthMiddleware(jwtSecret string, database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key := c.GetHeader("X-API-Key"); key != "" {
			// Resolution lives in the auth package — one place answering "is
			// this credential valid" for every credential type (audit A2).
			if apiKey := auth.ValidateAPIKey(database, key); apiKey != nil {
				c.Set("user_id", apiKey.ID)
				c.Set("username", "api-key:"+apiKey.Name)
				c.Set("role", apiKey.Role)
				c.Next()
				return
			}
			rejectAuth(c, "invalid api key")
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// Browser WebSocket handshakes cannot send custom headers, so
			// browser WS clients pass the JWT as a ?token= query param
			// (documented contract; see the deploy/install WS routes). Only
			// honored for actual upgrade requests so normal API calls can't
			// leak tokens into URLs.
			if isWebSocketUpgrade(c.Request) {
				if q := c.Query("token"); q != "" {
					// Prefer a single-use WS ticket (audit-auth L1): short-
					// lived, carries the issuer's identity, and worthless once
					// consumed — unlike a 4h JWT in a URL.
					if tkt, ok := consumeWSTicket(q); ok {
						c.Set("user_id", tkt.userID)
						c.Set("username", tkt.username)
						c.Set("role", tkt.role)
						c.Next()
						return
					}
					if claims, err := auth.ValidateJWTWithVersionCheck(q, jwtSecret, database); err == nil {
						c.Set("user_id", claims.UserID)
						c.Set("username", claims.Username)
						c.Set("role", claims.Role)
						c.Next()
						return
					}
					rejectAuth(c, "invalid or expired token")
					return
				}
			}
			rejectAuth(c, "missing authorization header")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			rejectAuth(c, "invalid authorization format")
			return
		}

		token := parts[1]
		claims, err := auth.ValidateJWTWithVersionCheck(token, jwtSecret, database)
		if err != nil {
			rejectAuth(c, "invalid or expired token")
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// isWebSocketUpgrade reports whether the request is a WebSocket handshake
// (Connection: Upgrade + Upgrade: websocket), case-insensitively per RFC 6455.
func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, v := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(v), "upgrade") {
			return true
		}
	}
	return false
}

func RequireRole(requiredRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		roleVal, exists := c.Get("role")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions: no role assigned"})
			c.Abort()
			return
		}
		userRole, ok := roleVal.(string)
		if !ok || !auth.HasRole(userRole, requiredRole) {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "0")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// script-src is 'self' only: the Vite production build emits external
		// hashed chunks (no inline scripts), so 'unsafe-inline'/'unsafe-eval'
		// are not needed. style-src keeps 'unsafe-inline' because the SPA uses
		// dynamic inline style attributes (e.g. width gauges on the Dashboard).
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self' ws: wss:")
		c.Next()
	}
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowed := ""
		if origin != "" && originAllowed(origin, c.Request.Host) {
			allowed = origin
		}

		if allowed != "" {
			c.Header("Access-Control-Allow-Origin", allowed)
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Vary", "Origin")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func originAllowed(origin, requestHost string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Host == requestHost {
		return true
	}
	originHost, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		originHost = u.Hostname()
	}
	requestHostname, _, err := net.SplitHostPort(requestHost)
	if err != nil {
		requestHostname = requestHost
	}
	originIP := net.ParseIP(originHost)
	requestIP := net.ParseIP(requestHostname)
	originLoopback := originHost == "localhost" || (originIP != nil && originIP.IsLoopback())
	requestLoopback := requestHostname == "localhost" || (requestIP != nil && requestIP.IsLoopback())
	return originLoopback && requestLoopback
}

// InstanceMiddleware resolves the instance from the URL parameter and validates it's available.
// It sets "instance_id", "agent_client", "database", and "registry_manager" in the Gin context for downstream handlers.
func InstanceMiddleware(agentMgr *agent.Manager, database *db.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID := c.Param("instance_id")
		if instanceID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing instance_id parameter"})
			c.Abort()
			return
		}

		client, err := agentMgr.GetClient(instanceID)
		if err != nil {
			// Check if instance not found (404) takes priority
			if errors.Is(err, agent.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				c.Abort()
				return
			}
			// Check if instance is offline (503)
			if errors.Is(err, agent.ErrInstanceOffline) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "instance offline"})
				c.Abort()
				return
			}
			// Generic error
			internalError(c, err)
			c.Abort()
			return
		}

		// Create a registry manager for this context
		registryMgr := registry.NewManager(database, jwtSecret)

		c.Set("instance_id", instanceID)
		c.Set("agent_client", client)
		c.Set("database", database)
		c.Set("jwt_secret", jwtSecret)
		c.Set("registry_manager", registryMgr)
		c.Next()
	}
}
