package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// HashAPIKey returns the SHA-256 hex digest of an API key — the canonical
// form stored in the database. It lives here (not in server) so all
// credential types share one home in the auth package (audit-auth A2).
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// ValidateAPIKey resolves an API key to its stored record using constant-time
// comparison. Returns nil when no key matches (audit-auth A2/A3: one place
// that answers "is this credential valid").
func ValidateAPIKey(database *db.DB, rawKey string) *db.APIKey {
	if rawKey == "" {
		return nil
	}
	apiKeys, err := database.ListAPIKeys()
	if err != nil {
		return nil
	}
	hashed := HashAPIKey(rawKey)
	for i := range apiKeys {
		if subtle.ConstantTimeCompare([]byte(apiKeys[i].KeyHash), []byte(hashed)) == 1 {
			return &apiKeys[i]
		}
	}
	return nil
}

// AuditHook lets the server package record audit entries for auth events
// without auth importing server (which would be an import cycle). It is
// wired once from server.RegisterRoutes. Nil-safe: audit calls are skipped
// when unset (e.g. in unit tests).
var AuditHook func(c *gin.Context, action, resource, status, details string)

func audit(c *gin.Context, action, resource, status, details string) {
	if AuditHook != nil {
		AuditHook(c, action, resource, status, details)
	}
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// dummyPasswordHash is a throwaway bcrypt hash compared against when the
// username doesn't exist, so the unknown-user path costs the same ~bcrypt
// time as the known-user path and response latency can't enumerate valid
// usernames (audit-auth M3).
var dummyPasswordHash []byte

func init() {
	h, err := bcrypt.GenerateFromPassword([]byte("dockpal-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		panic("failed to generate dummy bcrypt hash: " + err.Error())
	}
	dummyPasswordHash = h
}

func HandleLogin(c *gin.Context, jwtSecret string, database *db.DB) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	user, err := database.GetUser(req.Username)
	if err != nil {
		// Spend the same bcrypt time as a real comparison (audit-auth M3).
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(req.Password))
		audit(c, "auth.login", "user:"+req.Username, "failed", "unknown username")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		audit(c, "auth.login", "user:"+req.Username, "failed", "wrong password")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := GenerateJWT(user.ID, user.Username, jwtSecret, user.Role, user.TokenVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}
	audit(c, "auth.login", "user:"+user.Username, "success", "")

	c.JSON(http.StatusOK, gin.H{
		"token":    token,
		"username": user.Username,
		"role":     user.Role,
	})
}

func HandleLogout(c *gin.Context, database *db.DB) {
	username := c.GetString("username")
	if username != "" {
		// Increment token version to invalidate all existing tokens. Surface
		// the failure: returning 200 while revocation failed leaves every
		// token server-valid for its full TTL while the user believes the
		// session ended (audit-auth M4).
		if err := database.IncrementTokenVersion(username); err != nil {
			log.Printf("failed to increment token version for %s: %v", username, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke session"})
			return
		}
		audit(c, "auth.logout", "user:"+username, "success", "session revoked")
	}
	c.JSON(http.StatusOK, gin.H{"status": "logged out"})
}

type ResetPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// bcryptMaxPasswordBytes is bcrypt's hard input limit — longer inputs are
// rejected with ErrPasswordTooLong on hash (and silently truncated on
// compare), so validate up front and return a clear 400 (audit-auth L7).
const bcryptMaxPasswordBytes = 72

func passwordWithinBcryptLimit(pw string) bool {
	return len([]byte(pw)) <= bcryptMaxPasswordBytes
}

func HandleResetPassword(c *gin.Context, database *db.DB) {
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if !passwordWithinBcryptLimit(req.NewPassword) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at most 72 bytes"})
		return
	}

	username := c.GetString("username")
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	if err := database.UpdatePasswordWithVersion(username, string(hash)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update password"})
		return
	}
	audit(c, "auth.password_reset", "user:"+username, "success", "")

	c.JSON(http.StatusOK, gin.H{"status": "password updated"})
}

func HandleListUsers(c *gin.Context, database *db.DB) {
	users, err := database.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}

	type userResponse struct {
		Username  string `json:"username"`
		Role      string `json:"role"`
		CreatedAt int64  `json:"created_at"`
	}

	// Non-nil even when empty so the endpoint serializes [] (not null) — the
	// SPA iterates the result with {#each}, which throws on null (audit L8).
	resp := make([]userResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, userResponse{
			Username:  u.Username,
			Role:      u.Role,
			CreatedAt: u.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, resp)
}

type UpdateRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

func HandleUpdateUserRole(c *gin.Context, database *db.DB) {
	targetUsername := c.Param("username")
	callerUsername := c.GetString("username")

	if targetUsername == callerUsername {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot change your own role"})
		return
	}

	var req UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if req.Role != RoleAdmin && req.Role != RoleOperator && req.Role != RoleViewer {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role: must be admin, operator, or viewer"})
		return
	}

	if _, err := database.GetUser(targetUsername); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if err := database.UpdateUserRole(targetUsername, req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update role"})
		return
	}
	audit(c, "auth.role_change", "user:"+targetUsername, "success", "role set to "+req.Role+" by "+callerUsername)

	c.JSON(http.StatusOK, gin.H{"status": "role updated"})
}

func HandleGetProfile(c *gin.Context, database *db.DB) {
	username := c.GetString("username")
	user, err := database.GetUser(username)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"username":   user.Username,
		"role":       user.Role,
		"created_at": user.CreatedAt,
	})
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

func HandleChangePassword(c *gin.Context, database *db.DB) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if !passwordWithinBcryptLimit(req.NewPassword) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at most 72 bytes"})
		return
	}

	username := c.GetString("username")
	user, err := database.GetUser(username)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid user"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	if err := database.UpdatePasswordWithVersion(username, string(hash)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update password"})
		return
	}
	audit(c, "auth.password_change", "user:"+username, "success", "")

	c.JSON(http.StatusOK, gin.H{"status": "password updated"})
}
