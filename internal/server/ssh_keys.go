package server

// Admin-managed saved SSH keys: upload a private key once (Settings →
// Administration → SSH Keys), then pick it by ID in the Add Server panel
// instead of pasting the key material on every install. Private keys are
// encrypted at rest with the same AES-256-GCM scheme as agent tokens; the
// list endpoint never returns key material, only the fingerprint.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	cryptossh "golang.org/x/crypto/ssh"

	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
)

// keyListItem is the API shape of a stored key — no private material.
type keyListItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"key_type"`
	CreatedAt   int64  `json:"created_at"`
}

// HandleListSSHKeys returns the saved keys for pickers and management lists.
func HandleListSSHKeys(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		keys, err := database.ListSSHKeys()
		if err != nil {
			internalError(c, err)
			return
		}
		out := make([]keyListItem, 0, len(keys))
		for _, k := range keys {
			out = append(out, keyListItem{
				ID:          k.ID,
				Name:        k.Name,
				Fingerprint: k.Fingerprint,
				KeyType:     k.KeyType,
				CreatedAt:   k.CreatedAt,
			})
		}
		c.JSON(http.StatusOK, out)
	}
}

// HandleCreateSSHKey validates and stores a private key. The key is parsed
// server-side so a typo never becomes a stored unusable secret, and the
// fingerprint is derived from the parsed public half.
func HandleCreateSSHKey(database *db.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Name       string `json:"name" binding:"required"`
			PrivateKey string `json:"private_key" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name and private_key are required"})
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" || len(req.Name) > 64 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name must be 1-64 characters"})
			return
		}

		signer, err := cryptossh.ParsePrivateKey([]byte(req.PrivateKey))
		if err != nil {
			// Distinguish encrypted keys (common mistake) from corrupt input.
			if strings.Contains(err.Error(), "contains an encrypted key") || strings.Contains(err.Error(), "passphrase") {
				c.JSON(http.StatusBadRequest, gin.H{"error": "key is passphrase-protected; remove the passphrase or use a key without one"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "not a valid SSH private key: " + err.Error()})
			return
		}
		pub := signer.PublicKey()

		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			internalError(c, err)
			return
		}
		enc, err := registry.Encrypt([]byte(req.PrivateKey), cryptoKey)
		if err != nil {
			internalError(c, err)
			return
		}

		key := db.SSHKey{
			ID:            generateID("sshkey"),
			Name:          req.Name,
			Fingerprint:   cryptossh.FingerprintSHA256(pub),
			KeyType:       pub.Type(),
			PrivateKeyEnc: enc,
			CreatedAt:     time.Now().Unix(),
		}
		if err := database.SaveSSHKey(key); err != nil {
			internalError(c, err)
			return
		}
		LogAudit(c, database, "sshkey.create", key.ID, "success", "name="+key.Name+" fingerprint="+key.Fingerprint)
		c.JSON(http.StatusCreated, keyListItem{
			ID:          key.ID,
			Name:        key.Name,
			Fingerprint: key.Fingerprint,
			KeyType:     key.KeyType,
			CreatedAt:   key.CreatedAt,
		})
	}
}

// HandleDeleteSSHKey removes a stored key. Instances that already used it
// keep their own encrypted copy of the secret, so deletion is safe.
func HandleDeleteSSHKey(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if err := database.DeleteSSHKey(id); err != nil {
			internalError(c, err)
			return
		}
		LogAudit(c, database, "sshkey.delete", id, "success", "")
		c.JSON(http.StatusOK, gin.H{"message": "deleted"})
	}
}

// resolveSSHKeySecret loads and decrypts a saved key for the installer.
// Returns an error when the key does not exist or cannot be decrypted.
func resolveSSHKeySecret(database *db.DB, cryptoKey []byte, keyID string) (string, error) {
	key, err := database.GetSSHKey(keyID)
	if err != nil {
		if errors.Is(err, db.ErrSSHKeyNotFound) {
			return "", fmt.Errorf("saved ssh key %q not found", keyID)
		}
		return "", err
	}
	plain, err := registry.Decrypt(key.PrivateKeyEnc, cryptoKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt saved ssh key")
	}
	return string(plain), nil
}
