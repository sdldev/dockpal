package registry

import (
	"bytes"
	"testing"

	"pgregory.net/rapid"
)

// TestEncryptDecryptRoundTrip verifies that for any random plaintext and valid key,
// Decrypt(Encrypt(plaintext, key), key) produces the original plaintext.
//
// **Validates: Requirements 1.1, 5.1**
func TestEncryptDecryptRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random JWT secret (non-empty)
		secret := rapid.StringMatching(`[a-zA-Z0-9]{8,64}`).Draw(t, "secret")

		// Generate random plaintext (1-255 bytes, simulating token values)
		plaintextLen := rapid.IntRange(1, 255).Draw(t, "plaintextLen")
		plaintext := make([]byte, plaintextLen)
		for i := range plaintext {
			plaintext[i] = byte(rapid.IntRange(0, 255).Draw(t, "byte"))
		}

		// Derive key
		key, err := DeriveKey(secret)
		if err != nil {
			t.Fatalf("DeriveKey failed: %v", err)
		}
		if len(key) != 32 {
			t.Fatalf("expected 32-byte key, got %d", len(key))
		}

		// Encrypt
		ciphertext, err := Encrypt(plaintext, key)
		if err != nil {
			t.Fatalf("Encrypt failed: %v", err)
		}

		// Ciphertext must be longer than plaintext (nonce + tag overhead)
		if len(ciphertext) <= len(plaintext) {
			t.Fatalf("ciphertext should be longer than plaintext")
		}

		// Decrypt
		decrypted, err := Decrypt(ciphertext, key)
		if err != nil {
			t.Fatalf("Decrypt failed: %v", err)
		}

		// Round-trip must produce original plaintext
		if !bytes.Equal(plaintext, decrypted) {
			t.Fatalf("round-trip failed: plaintext mismatch")
		}
	})
}

// TestEncryptProducesUniqueCiphertext verifies that encrypting the same plaintext
// with the same key produces different ciphertext each time (due to random nonce).
//
// **Validates: Requirements 5.1**
func TestEncryptProducesUniqueCiphertext(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		secret := rapid.StringMatching(`[a-zA-Z0-9]{8,64}`).Draw(t, "secret")
		plaintext := []byte(rapid.StringMatching(`[a-zA-Z0-9]{4,40}`).Draw(t, "plaintext"))

		key, err := DeriveKey(secret)
		if err != nil {
			t.Fatalf("DeriveKey failed: %v", err)
		}

		ct1, err := Encrypt(plaintext, key)
		if err != nil {
			t.Fatalf("first Encrypt failed: %v", err)
		}

		ct2, err := Encrypt(plaintext, key)
		if err != nil {
			t.Fatalf("second Encrypt failed: %v", err)
		}

		if bytes.Equal(ct1, ct2) {
			t.Fatalf("two encryptions of same plaintext should produce different ciphertext")
		}
	})
}

// TestKeyIsolation verifies that different secrets produce different keys,
// ensuring the derived key is isolated from other uses of the same secret.
//
// **Validates: Requirements 5.4**
func TestKeyIsolation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		secret1 := rapid.StringMatching(`[a-zA-Z0-9]{8,64}`).Draw(t, "secret1")
		secret2 := rapid.StringMatching(`[a-zA-Z0-9]{8,64}`).Draw(t, "secret2")

		if secret1 == secret2 {
			return // skip if same secret generated
		}

		key1, err := DeriveKey(secret1)
		if err != nil {
			t.Fatalf("DeriveKey(secret1) failed: %v", err)
		}

		key2, err := DeriveKey(secret2)
		if err != nil {
			t.Fatalf("DeriveKey(secret2) failed: %v", err)
		}

		if bytes.Equal(key1, key2) {
			t.Fatalf("different secrets should produce different keys")
		}
	})
}
