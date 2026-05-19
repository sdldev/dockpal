package db

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	TokenVersion int    `json:"token_version"`
	CreatedAt    int64  `json:"created_at"`
}

type Service struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Domain    string `json:"domain,omitempty"`
	Compose   string `json:"compose,omitempty"`
	Repo      string `json:"repo,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type Domain struct {
	ID      string `json:"id"`
	Domain  string `json:"domain"`
	Service string `json:"service"`
	Port    int    `json:"port"`
}

type RegistryCredential struct {
	ID              string `json:"id"`
	Registry        string `json:"registry"`
	Username        string `json:"username"`
	EncryptedToken  []byte `json:"encrypted_token"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	LastValidatedAt int64  `json:"last_validated_at,omitempty"`
}

type DB struct {
	db *bbolt.DB
}

var (
	bucketUsers      = []byte("users")
	bucketServices   = []byte("services")
	bucketDomains    = []byte("domains")
	bucketRegistries = []byte("registries")
)

func New(path string) (*DB, error) {
	bdb, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := bdb.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range [][]byte{bucketUsers, bucketServices, bucketDomains, bucketRegistries} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to create buckets: %w", err)
	}

	return &DB{db: bdb}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

// Users
func (d *DB) CreateUser(user User) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		data, err := json.Marshal(user)
		if err != nil {
			return err
		}
		return b.Put([]byte(user.Username), data)
	})
}

func (d *DB) GetUser(username string) (*User, error) {
	var user User
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		data := b.Get([]byte(username))
		if data == nil {
			return fmt.Errorf("user not found")
		}
		return json.Unmarshal(data, &user)
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *DB) UpdatePassword(username, hash string) error {
	user, err := d.GetUser(username)
	if err != nil {
		return err
	}
	user.PasswordHash = hash
	return d.CreateUser(*user)
}

// UpdatePasswordWithVersion atomically updates the password hash and increments
// the token_version field, invalidating all previously issued JWT tokens.
func (d *DB) UpdatePasswordWithVersion(username, hash string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		data := b.Get([]byte(username))
		if data == nil {
			return fmt.Errorf("user not found")
		}
		var user User
		if err := json.Unmarshal(data, &user); err != nil {
			return err
		}
		user.PasswordHash = hash
		user.TokenVersion++
		updated, err := json.Marshal(user)
		if err != nil {
			return err
		}
		return b.Put([]byte(username), updated)
	})
}

// IncrementTokenVersion atomically increments the token_version field,
// invalidating all previously issued JWT tokens for the user.
func (d *DB) IncrementTokenVersion(username string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		data := b.Get([]byte(username))
		if data == nil {
			return fmt.Errorf("user not found")
		}
		var user User
		if err := json.Unmarshal(data, &user); err != nil {
			return err
		}
		user.TokenVersion++
		updated, err := json.Marshal(user)
		if err != nil {
			return err
		}
		return b.Put([]byte(username), updated)
	})
}

func (d *DB) EnsureDefaultAdmin(passwordHash string) error {
	_, err := d.GetUser("admin")
	if err != nil {
		return d.CreateUser(User{
			ID:           "admin-001",
			Username:     "admin",
			PasswordHash: passwordHash,
			CreatedAt:    time.Now().Unix(),
		})
	}
	return nil
}

// Services
func (d *DB) SaveService(svc Service) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketServices)
		data, err := json.Marshal(svc)
		if err != nil {
			return err
		}
		return b.Put([]byte(svc.ID), data)
	})
}

func (d *DB) ListServices() ([]Service, error) {
	var services []Service
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketServices)
		return b.ForEach(func(k, v []byte) error {
			var svc Service
			if err := json.Unmarshal(v, &svc); err != nil {
				return err
			}
			services = append(services, svc)
			return nil
		})
	})
	return services, err
}

func (d *DB) GetService(id string) (*Service, error) {
	var svc Service
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketServices)
		data := b.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("service not found")
		}
		return json.Unmarshal(data, &svc)
	})
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

func (d *DB) DeleteService(id string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketServices).Delete([]byte(id))
	})
}

// Domains
func (d *DB) SaveDomain(domain Domain) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketDomains)
		data, err := json.Marshal(domain)
		if err != nil {
			return err
		}
		return b.Put([]byte(domain.ID), data)
	})
}

func (d *DB) ListDomains() ([]Domain, error) {
	var domains []Domain
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketDomains)
		return b.ForEach(func(k, v []byte) error {
			var d Domain
			if err := json.Unmarshal(v, &d); err != nil {
				return err
			}
			domains = append(domains, d)
			return nil
		})
	})
	return domains, err
}

func (d *DB) DeleteDomain(id string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketDomains).Delete([]byte(id))
	})
}

// Registry Credentials

func (d *DB) SaveRegistryCredential(cred RegistryCredential) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketRegistries)
		data, err := json.Marshal(cred)
		if err != nil {
			return err
		}
		return b.Put([]byte(cred.ID), data)
	})
}

func (d *DB) GetRegistryCredential(id string) (*RegistryCredential, error) {
	var cred RegistryCredential
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketRegistries)
		data := b.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("registry credential not found")
		}
		return json.Unmarshal(data, &cred)
	})
	if err != nil {
		return nil, err
	}
	return &cred, nil
}

func (d *DB) ListRegistryCredentials() ([]RegistryCredential, error) {
	var creds []RegistryCredential
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketRegistries)
		return b.ForEach(func(k, v []byte) error {
			var cred RegistryCredential
			if err := json.Unmarshal(v, &cred); err != nil {
				return err
			}
			creds = append(creds, cred)
			return nil
		})
	})
	return creds, err
}

func (d *DB) DeleteRegistryCredential(id string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketRegistries).Delete([]byte(id))
	})
}

func (d *DB) FindRegistryCredentialByDomain(domain string) (*RegistryCredential, error) {
	var match *RegistryCredential
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketRegistries)
		return b.ForEach(func(k, v []byte) error {
			var cred RegistryCredential
			if err := json.Unmarshal(v, &cred); err != nil {
				return err
			}
			if strings.EqualFold(cred.Registry, domain) {
				if match == nil || cred.UpdatedAt > match.UpdatedAt {
					c := cred
					match = &c
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return match, nil
}
