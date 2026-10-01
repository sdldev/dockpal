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
	"github.com/sdldev/dockpal/internal/ssh"
)

// keyListItem is the API shape of a stored key — no private material.
type keyListItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"key_type"`
	SecretType  string `json:"secret_type"` // "public" | "private"; "" = legacy private
	// PublicKeyLine is set for public-type keys only — the authorized_keys
	// line itself is not a secret, and pickers (hardening) need it directly.
	PublicKeyLine string `json:"public_key,omitempty"`
	CreatedAt     int64  `json:"created_at"`
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
			item := keyListItem{
				ID:          k.ID,
				Name:        k.Name,
				Fingerprint: k.Fingerprint,
				KeyType:     k.KeyType,
				SecretType:  k.SecretType,
				CreatedAt:   k.CreatedAt,
			}
			if k.SecretType == "public" {
				item.PublicKeyLine = k.PublicKey
			}
			out = append(out, item)
		}
		c.JSON(http.StatusOK, out)
	}
}

// HandleCreateSSHKey validates and stores a PUBLIC key (e.g. the content of
// ~/.ssh/id_ed25519.pub on the operator's PC). Private keys are no longer
// accepted: the panel generates and manages its own per-instance keypair for
// connecting, so the operator's private key must never leave their machine —
// matching the upstream convention (ssh-copy-id / Vito's public-only keys).
// Keys uploaded before this change (legacy private) keep working.
func HandleCreateSSHKey(database *db.DB, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Name       string `json:"name"`
			PublicKey  string `json:"public_key"`
			PrivateKey string `json:"private_key"` // legacy field — rejected with guidance
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if strings.TrimSpace(req.PrivateKey) != "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Dockpal no longer accepts PRIVATE keys — upload the PUBLIC key (.pub) instead; your private key stays on your PC and the panel connects with its own generated key",
			})
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" || len(req.Name) > 64 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name must be 1-64 characters"})
			return
		}
		line := strings.TrimSpace(req.PublicKey)
		if line == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "public_key is required"})
			return
		}
		if err := ssh.ValidatePublicKeyLine(line); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "not a valid SSH public key: " + err.Error()})
			return
		}
		parsed, _, _, _, err := cryptossh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "not a valid SSH public key: " + err.Error()})
			return
		}

		key := db.SSHKey{
			ID:          generateID("sshkey"),
			Name:        req.Name,
			Fingerprint: cryptossh.FingerprintSHA256(parsed),
			KeyType:     parsed.Type(),
			SecretType:  "public",
			PublicKey:   line,
			CreatedAt:   time.Now().Unix(),
		}
		if err := database.SaveSSHKey(key); err != nil {
			internalError(c, err)
			return
		}
		LogAudit(c, database, "sshkey.create", key.ID, "success", "name="+key.Name+" fingerprint="+key.Fingerprint)
		c.JSON(http.StatusCreated, keyListItem{
			ID:            key.ID,
			Name:          key.Name,
			Fingerprint:   key.Fingerprint,
			KeyType:       key.KeyType,
			SecretType:    key.SecretType,
			PublicKeyLine: key.PublicKey,
			CreatedAt:     key.CreatedAt,
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

// resolveSSHPublicLine returns the authorized_keys line for a saved key.
// Public-type keys store it plaintext; legacy private keys have it derived
// from their (decrypted) private half.
func resolveSSHPublicLine(database *db.DB, cryptoKey []byte, keyID string) (string, error) {
	key, err := database.GetSSHKey(keyID)
	if err != nil {
		if errors.Is(err, db.ErrSSHKeyNotFound) {
			return "", fmt.Errorf("saved ssh key %q not found", keyID)
		}
		return "", err
	}
	if key.SecretType == "public" {
		if strings.TrimSpace(key.PublicKey) == "" {
			return "", fmt.Errorf("saved key %q has no public key line", key.Name)
		}
		return strings.TrimSpace(key.PublicKey), nil
	}
	plain, err := registry.Decrypt(key.PrivateKeyEnc, cryptoKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt saved ssh key")
	}
	pub, _, err := ssh.PublicKeyFromPrivate(string(plain))
	if err != nil {
		return "", fmt.Errorf("failed to derive public key from saved key %q", key.Name)
	}
	return pub, nil
}
