package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
)

func AuthMiddleware(jwtSecret string, database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		// Fallback to query param token for WebSocket connections
		// (WebSocket cannot send custom headers during upgrade)
		if authHeader == "" {
			if token := c.Query("token"); token != "" && c.IsWebsocket() {
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
				return
			}
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

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		host := c.Request.Host

		// Only allow same-origin requests or explicit host match
		allowed := ""
		if origin != "" {
			// Allow if origin host matches request host (same-origin)
			if strings.Contains(origin, "://"+host) || strings.Contains(origin, "://localhost:") {
				allowed = origin
			}
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
			if strings.Contains(err.Error(), "instance not found") {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				c.Abort()
				return
			}
			// Check if instance is offline (503)
			if strings.Contains(err.Error(), "instance offline") || strings.Contains(err.Error(), "no edge connection") {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "instance offline"})
				c.Abort()
				return
			}
			// Generic error
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			c.Abort()
			return
		}

		// Create a registry manager for this context
		registryMgr := registry.NewManager(database, jwtSecret)

		c.Set("instance_id", instanceID)
		c.Set("agent_client", client)
		c.Set("database", database)
		c.Set("registry_manager", registryMgr)
		c.Next()
	}
}