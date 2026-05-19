package registry

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// DeriveKey derives a 32-byte encryption key from the JWT secret using HKDF-SHA256.
// The derived key is cryptographically isolated from the JWT signing key via a dedicated
// salt and info context string.
func DeriveKey(jwtSecret string) ([]byte, error) {
	if jwtSecret == "" {
		return nil, errors.New("jwt secret must not be empty")
	}

	salt := []byte("dockpal-registry-v1")
	info := []byte("dockpal-registry-encryption")

	hkdfReader := hkdf.New(sha256.New, []byte(jwtSecret), salt, info)
	key := make([]byte, 32) // 256 bits for AES-256
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("failed to derive encryption key: %w", err)
	}

	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM with a random 12-byte nonce.
// The returned data is formatted as: nonce (12 bytes) || ciphertext || tag (16 bytes).
func Encrypt(plaintext []byte, key []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("plaintext must not be empty")
	}
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Seal appends the ciphertext+tag to the nonce slice, resulting in: nonce || ciphertext || tag
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts data that was encrypted with Encrypt. It expects the format:
// nonce (12 bytes) || ciphertext || tag (16 bytes).
// Returns a descriptive error on failure without exposing key or ciphertext values.
func Decrypt(data []byte, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("decryption key must be 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize+gcm.Overhead() {
		return nil, errors.New("stored credential cannot be decrypted: data too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("stored credential cannot be decrypted: corrupted or modified ciphertext")
	}

	return plaintext, nil
}

// zeroBytes overwrites a byte slice with zeros for secure memory cleanup.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
