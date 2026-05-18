package tunnel

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
)

// **Validates: Requirements 14.4**

// Property 16: Tunnel Token Validation
// Verify rejection of empty strings and strings with chars outside [a-zA-Z0-9\-_.]

func TestProperty16_TunnelTokenValidation(t *testing.T) {
	// Sub-property: Empty tokens are always rejected
	t.Run("empty_tokens_rejected", func(t *testing.T) {
		err := ValidateTunnelToken("")
		if err == nil {
			t.Error("expected error for empty token, got nil")
		}
	})

	// Sub-property: Tokens with only valid chars [a-zA-Z0-9\-_.] are accepted
	t.Run("valid_tokens_accepted", func(t *testing.T) {
		prop := func(params validTokenParams) bool {
			err := ValidateTunnelToken(params.token)
			return err == nil
		}

		cfg := &quick.Config{
			MaxCount: 500,
			Values:   validTokenGenerator,
		}
		if err := quick.Check(prop, cfg); err != nil {
			t.Errorf("Property 16 (valid tokens) failed: %v", err)
		}
	})

	// Sub-property: Tokens containing chars outside [a-zA-Z0-9\-_.] are rejected
	t.Run("invalid_tokens_rejected", func(t *testing.T) {
		prop := func(params invalidTokenParams) bool {
			err := ValidateTunnelToken(params.token)
			return err != nil
		}

		cfg := &quick.Config{
			MaxCount: 500,
			Values:   invalidTokenGenerator,
		}
		if err := quick.Check(prop, cfg); err != nil {
			t.Errorf("Property 16 (invalid tokens) failed: %v", err)
		}
	})
}

// validTokenParams holds a token composed only of valid characters
type validTokenParams struct {
	token string
}

const validChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_."

func validTokenGenerator(values []reflect.Value, rng *rand.Rand) {
	length := 1 + rng.Intn(64) // [1, 64] - non-empty
	token := make([]byte, length)
	for i := range token {
		token[i] = validChars[rng.Intn(len(validChars))]
	}
	values[0] = reflect.ValueOf(validTokenParams{token: string(token)})
}

// invalidTokenParams holds a token that contains at least one invalid character
type invalidTokenParams struct {
	token string
}

// Characters that are NOT in [a-zA-Z0-9\-_.]
const invalidChars = " !@#$%^&*()+=[]{}|;:',<>?/`~\"\\\n\t\x00"

func invalidTokenGenerator(values []reflect.Value, rng *rand.Rand) {
	// Build a token with at least one invalid char injected
	length := 1 + rng.Intn(32)
	token := make([]byte, length)

	// Fill with valid chars first
	for i := range token {
		token[i] = validChars[rng.Intn(len(validChars))]
	}

	// Inject at least one invalid character at a random position
	invalidPos := rng.Intn(length)
	token[invalidPos] = invalidChars[rng.Intn(len(invalidChars))]

	values[0] = reflect.ValueOf(invalidTokenParams{token: string(token)})
}
