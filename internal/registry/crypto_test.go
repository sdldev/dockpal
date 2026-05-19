package registry

import (
	"bytes"
	"testing"
)

func TestDeriveKey(t *testing.T) {
	t.Run("derives 32-byte key from secret", func(t *testing.T) {
		key, err := DeriveKey("my-test-secret")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(key) != 32 {
			t.Fatalf("expected 32-byte key, got %d bytes", len(key))
		}
	})

	t.Run("same secret produces same key", func(t *testing.T) {
		key1, _ := DeriveKey("same-secret")
		key2, _ := DeriveKey("same-secret")
		if !bytes.Equal(key1, key2) {
			t.Fatal("same secret should produce same key")
		}
	})

	t.Run("different secrets produce different keys", func(t *testing.T) {
		key1, _ := DeriveKey("secret-one")
		key2, _ := DeriveKey("secret-two")
		if bytes.Equal(key1, key2) {
			t.Fatal("different secrets should produce different keys")
		}
	})

	t.Run("empty secret returns error", func(t *testing.T) {
		_, err := DeriveKey("")
		if err == nil {
			t.Fatal("expected error for empty secret")
		}
	})
}

func TestEncryptDecrypt(t *testing.T) {
	key, _ := DeriveKey("test-secret-for-encryption")

	t.Run("round-trip encrypt/decrypt", func(t *testing.T) {
		plaintext := []byte("ghp_abc123def456ghi789jkl012mno345pqr6")
		ciphertext, err := Encrypt(plaintext, key)
		if err != nil {
			t.Fatalf("encrypt error: %v", err)
		}

		decrypted, err := Decrypt(ciphertext, key)
		if err != nil {
			t.Fatalf("decrypt error: %v", err)
		}

		if !bytes.Equal(plaintext, decrypted) {
			t.Fatalf("decrypted text does not match original")
		}
	})

	t.Run("each encryption produces different ciphertext (unique nonce)", func(t *testing.T) {
		plaintext := []byte("same-plaintext-value")
		ct1, _ := Encrypt(plaintext, key)
		ct2, _ := Encrypt(plaintext, key)
		if bytes.Equal(ct1, ct2) {
			t.Fatal("two encryptions of same plaintext should produce different ciphertext")
		}
	})

	t.Run("decrypt with wrong key fails", func(t *testing.T) {
		plaintext := []byte("secret-token")
		ciphertext, _ := Encrypt(plaintext, key)

		wrongKey, _ := DeriveKey("wrong-secret")
		_, err := Decrypt(ciphertext, wrongKey)
		if err == nil {
			t.Fatal("expected error when decrypting with wrong key")
		}
	})

	t.Run("decrypt corrupted data fails", func(t *testing.T) {
		plaintext := []byte("secret-token")
		ciphertext, _ := Encrypt(plaintext, key)

		// Corrupt a byte in the ciphertext
		ciphertext[15] ^= 0xFF

		_, err := Decrypt(ciphertext, key)
		if err == nil {
			t.Fatal("expected error when decrypting corrupted data")
		}
	})

	t.Run("decrypt too-short data fails", func(t *testing.T) {
		_, err := Decrypt([]byte("short"), key)
		if err == nil {
			t.Fatal("expected error for too-short data")
		}
	})

	t.Run("encrypt empty plaintext returns error", func(t *testing.T) {
		_, err := Encrypt([]byte{}, key)
		if err == nil {
			t.Fatal("expected error for empty plaintext")
		}
	})

	t.Run("encrypt with wrong key size returns error", func(t *testing.T) {
		_, err := Encrypt([]byte("data"), []byte("short-key"))
		if err == nil {
			t.Fatal("expected error for wrong key size")
		}
	})

	t.Run("decrypt with wrong key size returns error", func(t *testing.T) {
		ciphertext, _ := Encrypt([]byte("data"), key)
		_, err := Decrypt(ciphertext, []byte("short-key"))
		if err == nil {
			t.Fatal("expected error for wrong key size")
		}
	})
}

func TestZeroBytes(t *testing.T) {
	t.Run("zeroes out byte slice", func(t *testing.T) {
		data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		zeroBytes(data)
		for i, b := range data {
			if b != 0 {
				t.Fatalf("byte at index %d is %d, expected 0", i, b)
			}
		}
	})

	t.Run("handles empty slice", func(t *testing.T) {
		data := []byte{}
		zeroBytes(data) // should not panic
	})

	t.Run("handles nil slice", func(t *testing.T) {
		var data []byte
		zeroBytes(data) // should not panic
	})
}

func TestDecryptErrorMessages(t *testing.T) {
	key, _ := DeriveKey("test-secret")

	t.Run("error does not expose key or ciphertext values", func(t *testing.T) {
		plaintext := []byte("sensitive-token-value")
		ciphertext, _ := Encrypt(plaintext, key)
		ciphertext[15] ^= 0xFF // corrupt

		_, err := Decrypt(ciphertext, key)
		if err == nil {
			t.Fatal("expected error")
		}

		errMsg := err.Error()
		// Error should be descriptive but not expose sensitive data
		if bytes.Contains([]byte(errMsg), plaintext) {
			t.Fatal("error message should not contain plaintext")
		}
		if bytes.Contains([]byte(errMsg), key) {
			t.Fatal("error message should not contain key")
		}
	})
}
