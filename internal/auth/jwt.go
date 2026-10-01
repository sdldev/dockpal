package auth

import (
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
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil || !token.Valid {
		return nil, err
	}

	return claims, nil
}

// ValidateJWTWithVersionCheck validates the JWT token and additionally checks
// that the token_version claim matches the current stored version for the user.
//
// PRIVILEGE INVARIANT (audit-auth A4): the role claim embedded in a JWT is
// only trustworthy because every DB write that changes a user's privileges
// (UpdateUserRole, UpdatePasswordWithVersion, IncrementTokenVersion,
// IncrementAllTokenVersions) also bumps TokenVersion in the same transaction,
// so a stale-privilege token always fails this check. Any NEW privilege-
// mutating DB method (disable user, revoke session, demote) MUST bump
// TokenVersion the same way — if you add one without the bump, demoted
// admins keep their old token's role until expiry.
//
// Note: API keys deliberately have no version mechanism — a key's stored
// role is read fresh from the DB on every request (middleware.go), so role
// changes on the issuing user do NOT propagate to keys they created, and the
// only revocation is deleting the key. This is a documented design choice,
// not an oversight.
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
