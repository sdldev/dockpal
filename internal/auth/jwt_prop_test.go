package auth

import (
	"encoding/base64"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/sdldev/dockpal/internal/db"
)

// **Validates: Requirements 5.2, 5.3, 5.4, 5.5**

// Property 5: Token Version Increment — N password changes → version increases by N
// **Validates: Requirements 5.2, 5.3**

func TestProperty5_TokenVersionIncrement(t *testing.T) {
	// Property: For any N in [1, 20], calling UpdatePasswordWithVersion N times
	// results in the user's token_version being incremented by exactly N.
	prop := func(params versionIncrementParams) bool {
		// Create a temp BBolt database
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.New(dbPath)
		if err != nil {
			t.Logf("failed to create db: %v", err)
			return false
		}
		defer database.Close()
		defer os.Remove(dbPath)

		// Create a user with initial token version 0
		user := db.User{
			ID:           "test-user-001",
			Username:     "testuser",
			PasswordHash: "initialhash",
			TokenVersion: 0,
			CreatedAt:    1000000,
		}
		if err := database.CreateUser(user); err != nil {
			t.Logf("failed to create user: %v", err)
			return false
		}

		// Perform N password changes
		for i := 0; i < params.numChanges; i++ {
			if err := database.UpdatePasswordWithVersion("testuser", "newhash"+string(rune('0'+i))); err != nil {
				t.Logf("failed to update password on iteration %d: %v", i, err)
				return false
			}
		}

		// Verify token version increased by exactly N
		updatedUser, err := database.GetUser("testuser")
		if err != nil {
			t.Logf("failed to get user: %v", err)
			return false
		}

		return updatedUser.TokenVersion == params.numChanges
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   versionIncrementGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 5 failed: %v", err)
	}
}

// versionIncrementParams represents the test parameters for version increment
type versionIncrementParams struct {
	numChanges int // number of password changes [1, 20]
}

func versionIncrementGenerator(values []reflect.Value, rng *rand.Rand) {
	params := versionIncrementParams{
		numChanges: 1 + rng.Intn(20),
	}
	values[0] = reflect.ValueOf(params)
}

// Property 6: Token Version Round-Trip — JWT with matching version passes, mismatched rejects
// **Validates: Requirements 5.4, 5.5**

// TestJWT_RejectsAlgNone verifies the signing-method check in ValidateJWT:
// a token with alg=none (no signature) must never be accepted (audit-auth T5
// — replaces the tautological non-DB version round-trip property).
func TestJWT_RejectsAlgNone(t *testing.T) {
	secret := "test-secret-algnone"

	// Hand-craft an alg=none JWT with plausible claims.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"user_id":"user-1","username":"admin","role":"admin","token_version":0,"iss":"dockpal","exp":9999999999}`))
	token := header + "." + payload + "."

	if _, err := ValidateJWT(token, secret); err == nil {
		t.Fatal("alg=none token was accepted — signing-method check is broken")
	}
}

// TestJWT_RejectsWrongSecret verifies a token signed with a different key is
// rejected (audit-auth T5).
func TestJWT_RejectsWrongSecret(t *testing.T) {
	tokenStr, err := GenerateJWT("user-1", "admin", "secret-A", "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateJWT(tokenStr, "secret-B"); err == nil {
		t.Fatal("token signed with a different secret was accepted")
	}
}

// TestJWT_RejectsNonHMACAlgorithm verifies asymmetric-alg tokens (RS256 etc.)
// are rejected even though the HMAC key is public knowledge — alg-confusion
// must not be possible (audit-auth T5, pins jwt.go's signing-method check).
func TestJWT_RejectsNonHMACAlgorithm(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"user_id":"user-1","username":"admin","role":"admin","token_version":0,"iss":"dockpal","exp":9999999999}`))
	token := header + "." + payload + ".ZmFrZXNpZ25hdHVyZQ"

	if _, err := ValidateJWT(token, "any-secret"); err == nil {
		t.Fatal("RS256 token was accepted — alg-confusion possible")
	}
}

// TestProperty6_TokenVersionRoundTrip_WithDB tests the full round-trip including
// database version check via ValidateJWTWithVersionCheck.
func TestProperty6_TokenVersionRoundTrip_WithDB(t *testing.T) {
	prop := func(params versionRoundTripParams) bool {
		// Create a temp BBolt database
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.New(dbPath)
		if err != nil {
			t.Logf("failed to create db: %v", err)
			return false
		}
		defer database.Close()
		defer os.Remove(dbPath)

		secret := "test-secret-key-property6"

		// Create user with specific token version
		user := db.User{
			ID:           "user-123",
			Username:     "testuser",
			PasswordHash: "hash",
			TokenVersion: params.tokenVersion,
			CreatedAt:    1000000,
		}
		if err := database.CreateUser(user); err != nil {
			return false
		}

		// Generate JWT with matching version → should validate
		tokenStr, err := GenerateJWT("user-123", "testuser", secret, "admin", params.tokenVersion)
		if err != nil {
			return false
		}

		claims, err := ValidateJWTWithVersionCheck(tokenStr, secret, database)
		if err != nil {
			return false
		}
		if claims.TokenVersion != params.tokenVersion {
			return false
		}

		// Generate JWT with mismatched version → should reject
		mismatchToken, err := GenerateJWT("user-123", "testuser", secret, "admin", params.mismatchVersion)
		if err != nil {
			return false
		}

		_, err = ValidateJWTWithVersionCheck(mismatchToken, secret, database)
		if err == nil {
			// Should have failed due to version mismatch
			return false
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   versionRoundTripGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 6 (with DB) failed: %v", err)
	}
}

// versionRoundTripParams represents the test parameters for version round-trip
type versionRoundTripParams struct {
	tokenVersion    int // version embedded in the token [0, 100]
	mismatchVersion int // a different version to simulate DB mismatch
}

func versionRoundTripGenerator(values []reflect.Value, rng *rand.Rand) {
	tokenVersion := rng.Intn(101) // [0, 100]
	// Generate a mismatch version that is guaranteed different
	mismatchVersion := tokenVersion + 1 + rng.Intn(50) // always different

	params := versionRoundTripParams{
		tokenVersion:    tokenVersion,
		mismatchVersion: mismatchVersion,
	}
	values[0] = reflect.ValueOf(params)
}
