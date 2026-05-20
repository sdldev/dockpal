package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/auth"
)

func TestRequireRoleMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		assignedRole   string
		hasRoleInCtx   bool
		requiredRole   string
		expectedStatus int
	}{
		{
			name:           "missing role in context",
			hasRoleInCtx:   false,
			requiredRole:   auth.RoleViewer,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "viewer trying to access viewer endpoint",
			hasRoleInCtx:   true,
			assignedRole:   auth.RoleViewer,
			requiredRole:   auth.RoleViewer,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "viewer trying to access operator endpoint",
			hasRoleInCtx:   true,
			assignedRole:   auth.RoleViewer,
			requiredRole:   auth.RoleOperator,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "operator trying to access operator endpoint",
			hasRoleInCtx:   true,
			assignedRole:   auth.RoleOperator,
			requiredRole:   auth.RoleOperator,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "operator trying to access admin endpoint",
			hasRoleInCtx:   true,
			assignedRole:   auth.RoleOperator,
			requiredRole:   auth.RoleAdmin,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "admin trying to access admin endpoint",
			hasRoleInCtx:   true,
			assignedRole:   auth.RoleAdmin,
			requiredRole:   auth.RoleAdmin,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "admin trying to access operator endpoint",
			hasRoleInCtx:   true,
			assignedRole:   auth.RoleAdmin,
			requiredRole:   auth.RoleOperator,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "admin trying to access viewer endpoint",
			hasRoleInCtx:   true,
			assignedRole:   auth.RoleAdmin,
			requiredRole:   auth.RoleViewer,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "invalid role assigned",
			hasRoleInCtx:   true,
			assignedRole:   "hacker",
			requiredRole:   auth.RoleViewer,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tt.hasRoleInCtx {
					c.Set("role", tt.assignedRole)
				}
				c.Next()
			})

			r.GET("/test", RequireRole(tt.requiredRole), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/test", nil)
			r.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}
