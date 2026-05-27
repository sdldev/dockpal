package server

import (
	"errors"
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

func AuthMiddleware(jwtSecret string, database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key := c.GetHeader("X-API-Key"); key != "" {
			apiKeys, err := database.ListAPIKeys()
			if err != nil {
				internalError(c, err)
				c.Abort()
				return
			}
			hashed := hashAPIKey(key)
			for _, apiKey := range apiKeys {
				if apiKey.KeyHash == hashed {
					c.Set("user_id", apiKey.ID)
					c.Set("username", "api-key:"+apiKey.Name)
					c.Set("role", apiKey.Role)
					c.Next()
					return
				}
			}
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			c.Abort()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			c.Abort()
			return
		}

		token := parts[1]
		claims, err := auth.ValidateJWTWithVersionCheck(token, jwtSecret, database)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			c.Abort()
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
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
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self' ws: wss:")
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
