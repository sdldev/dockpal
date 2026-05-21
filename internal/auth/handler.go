package auth

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"golang.org/x/crypto/bcrypt"
)

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func HandleLogin(c *gin.Context, jwtSecret string, database *db.DB) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	user, err := database.GetUser(req.Username)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := GenerateJWT(user.ID, user.Username, jwtSecret, user.Role, user.TokenVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":    token,
		"username": user.Username,
		"role":     user.Role,
	})
}

func HandleLogout(c *gin.Context, database *db.DB) {
	username := c.GetString("username")
	if username != "" {
		// Increment token version to invalidate all existing tokens
		if err := database.IncrementTokenVersion(username); err != nil {
			log.Printf("failed to increment token version for %s: %v", username, err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "logged out"})
}

type ResetPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

func HandleResetPassword(c *gin.Context, database *db.DB) {
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
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

	var resp []userResponse
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

	c.JSON(http.StatusOK, gin.H{"status": "password updated"})
}
