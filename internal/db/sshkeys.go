package db

import (
	"encoding/json"
	"errors"

	"go.etcd.io/bbolt"
)

// SSHKey is a stored private SSH key the admin can reuse when installing the
// agent on new servers. The private key material is encrypted at rest
// (PrivateKeyEnc, AES-256-GCM — the handler encrypts before calling
// SaveSSHKey); only the fingerprint is stored plaintext so the UI can show
// which key is which without ever exposing the secret.
type SSHKey struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Fingerprint   string `json:"fingerprint"` // "SHA256:..." of the public key
	KeyType       string `json:"key_type"`    // ssh-rsa, ssh-ed25519, ecdsa-...
	PrivateKeyEnc []byte `json:"private_key_encrypted"`
	CreatedAt     int64  `json:"created_at"`
}

// ErrSSHKeyNotFound is returned by GetSSHKey when the ID does not exist.
var ErrSSHKeyNotFound = errors.New("ssh key not found")

// SaveSSHKey inserts or updates a stored key, keyed by ID.
func (d *DB) SaveSSHKey(key SSHKey) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		data, err := json.Marshal(key)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketSSHKeys).Put([]byte(key.ID), data)
	})
}

// ListSSHKeys returns all stored keys (including the encrypted blob — callers
// that serve the list over the API must strip PrivateKeyEnc first).
func (d *DB) ListSSHKeys() ([]SSHKey, error) {
	var out []SSHKey
	err := d.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketSSHKeys).ForEach(func(_, v []byte) error {
			var k SSHKey
			if err := json.Unmarshal(v, &k); err != nil {
				return nil // skip malformed entries
			}
			out = append(out, k)
			return nil
		})
	})
	return out, err
}

// GetSSHKey returns one stored key by ID.
func (d *DB) GetSSHKey(id string) (*SSHKey, error) {
	var k SSHKey
	err := d.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucketSSHKeys).Get([]byte(id))
		if v == nil {
			return ErrSSHKeyNotFound
		}
		return json.Unmarshal(v, &k)
	})
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// DeleteSSHKey removes a stored key. Missing IDs are not an error.
func (d *DB) DeleteSSHKey(id string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketSSHKeys).Delete([]byte(id))
	})
}
