package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sdldev/dockpal/internal/db"
)

type Claims struct {
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	TokenVersion int    `json:"token_version"`
	Role         string `json:"role"`
	jwt.RegisteredClaims
}

func GenerateJWT(userID, username, secret, role string, tokenVersion int) (string, error) {
	claims := Claims{
		UserID:       userID,
		Username:     username,
		TokenVersion: tokenVersion,
		Role:         role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(4 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "dockpal",
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ValidateJWT(tokenString, secret string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})

	if err != nil || !token.Valid {
		return nil, err
	}

	return claims, nil
}

// ValidateJWTWithVersionCheck validates the JWT token and additionally checks
// that the token_version claim matches the current stored version for the user.
func ValidateJWTWithVersionCheck(tokenString, secret string, database *db.DB) (*Claims, error) {
	claims, err := ValidateJWT(tokenString, secret)
	if err != nil {
		return nil, err
	}

	user, err := database.GetUser(claims.Username)
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}

	if claims.TokenVersion != user.TokenVersion {
		return nil, fmt.Errorf("token version mismatch: token invalidated")
	}

	return claims, nil
}

func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
