package db

import (
	"encoding/json"
	"errors"
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
	Role         string `json:"role"`
	CreatedAt    int64  `json:"created_at"`
}

type AuditLog struct {
	ID        string `json:"id"`
	Timestamp int64  `json:"timestamp"`
	Username  string `json:"username"`
	UserRole  string `json:"user_role"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Status    string `json:"status"`
	Details   string `json:"details"`
	IPAddress string `json:"ip_address"`
}

type Service struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id,omitempty"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Domain     string `json:"domain,omitempty"`
	Compose    string `json:"compose,omitempty"`
	Repo       string `json:"repo,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

type Domain struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id,omitempty"`
	Domain     string `json:"domain"`
	Service    string `json:"service"`
	Port       int    `json:"port"`
}

type RegistryCredential struct {
	ID              string `json:"id"`
	InstanceID      string `json:"instance_id,omitempty"`
	Registry        string `json:"registry"`
	Username        string `json:"username"`
	EncryptedToken  []byte `json:"encrypted_token"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	LastValidatedAt int64  `json:"last_validated_at,omitempty"`
}

type Instance struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	Mode                 string `json:"mode"`
	AgentTokenHash       string `json:"agent_token_hash"`
	AgentTokenEncrypted  []byte `json:"agent_token_encrypted"`
	AgentVersion         string `json:"agent_version"`
	Status               string `json:"status"`
	DockerVersion        string `json:"docker_version"`
	OS                   string `json:"os"`
	CPUCores             int    `json:"cpu_cores"`
	TotalMemory          int64  `json:"total_memory"`
	LastSeen             int64  `json:"last_seen"`
	CreatedAt            int64  `json:"created_at"`
	SSHHost              string `json:"ssh_host,omitempty"`
	SSHPort              int    `json:"ssh_port,omitempty"`
	SSHUser              string `json:"ssh_user,omitempty"`
	SSHAuthType          string `json:"ssh_auth_type,omitempty"` // "password" | "key"
	SSHPasswordEncrypted []byte `json:"ssh_password_encrypted,omitempty"`
	SSHKeyEncrypted      []byte `json:"ssh_key_encrypted,omitempty"`
}

type DB struct {
	db *bbolt.DB
}

var (
	bucketUsers      = []byte("users")
	bucketServices   = []byte("services")
	bucketDomains    = []byte("domains")
	bucketRegistries = []byte("registries")
	bucketInstances  = []byte("instances")
	bucketAuditLogs  = []byte("audit_logs")
	bucketWebhooks   = []byte("webhooks")
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrServiceNotFound   = errors.New("service not found")
	ErrInstanceNotFound  = errors.New("instance not found")
	ErrRegistryNotFound  = errors.New("registry credential not found")
	ErrWebhookNotFound   = errors.New("webhook not found")
	ErrCannotDeleteLocal = errors.New("cannot delete the local instance")
)

func New(path string) (*DB, error) {
	bdb, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := bdb.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range [][]byte{bucketUsers, bucketServices, bucketDomains, bucketRegistries, bucketInstances, bucketAuditLogs, bucketWebhooks} {
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
			return ErrUserNotFound
		}
		return json.Unmarshal(data, &user)
	})
	if err != nil {
		return nil, err
	}
	// Normalize empty role for backward compatibility
	if user.Role == "" {
		if user.Username == "admin" {
			user.Role = "admin"
		} else {
			user.Role = "viewer"
		}
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
			return ErrUserNotFound
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
			return ErrUserNotFound
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

func (d *DB) ListUsers() ([]User, error) {
	var users []User
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		return b.ForEach(func(k, v []byte) error {
			var user User
			if err := json.Unmarshal(v, &user); err != nil {
				return err
			}
			if user.Role == "" {
				if user.Username == "admin" {
					user.Role = "admin"
				} else {
					user.Role = "viewer"
				}
			}
			users = append(users, user)
			return nil
		})
	})
	return users, err
}

func (d *DB) UpdateUserRole(username, role string) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		data := b.Get([]byte(username))
		if data == nil {
			return ErrUserNotFound
		}
		var user User
		if err := json.Unmarshal(data, &user); err != nil {
			return err
		}
		user.Role = role
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
			Role:         "admin",
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
			return ErrServiceNotFound
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

// ListServicesByInstance returns services scoped to a specific instance.
// For "local" instance, returns services with empty InstanceID.
// For other instances, returns services with matching InstanceID.
func (d *DB) ListServicesByInstance(instanceID string) ([]Service, error) {
	var services []Service
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketServices)
		return b.ForEach(func(k, v []byte) error {
			var svc Service
			if err := json.Unmarshal(v, &svc); err != nil {
				return err
			}
			// For "local" instance, match empty InstanceID
			// For other instances, match exact InstanceID
			if instanceID == "local" {
				if svc.InstanceID == "" {
					services = append(services, svc)
				}
			} else if svc.InstanceID == instanceID {
				services = append(services, svc)
			}
			return nil
		})
	})
	return services, err
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

// ListDomainsByInstance returns domains scoped to a specific instance.
// For "local" instance, returns domains with empty InstanceID.
// For other instances, returns domains with matching InstanceID.
func (d *DB) ListDomainsByInstance(instanceID string) ([]Domain, error) {
	var domains []Domain
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketDomains)
		return b.ForEach(func(k, v []byte) error {
			var dom Domain
			if err := json.Unmarshal(v, &dom); err != nil {
				return err
			}
			// For "local" instance, match empty InstanceID
			// For other instances, match exact InstanceID
			if instanceID == "local" {
				if dom.InstanceID == "" {
					domains = append(domains, dom)
				}
			} else if dom.InstanceID == instanceID {
				domains = append(domains, dom)
			}
			return nil
		})
	})
	return domains, err
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
			return ErrRegistryNotFound
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

// FindRegistryCredentialByDomainAndInstance finds a registry credential by domain with instance scoping.
// It first attempts to find a credential matching both domain and instanceID.
// If not found, it falls back to a global credential (empty InstanceID) with matching domain.
// Returns the most recently updated match based on UpdatedAt timestamp.
func (d *DB) FindRegistryCredentialByDomainAndInstance(domain string, instanceID string) (*RegistryCredential, error) {
	var instanceMatch *RegistryCredential
	var globalMatch *RegistryCredential

	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketRegistries)
		return b.ForEach(func(k, v []byte) error {
			var cred RegistryCredential
			if err := json.Unmarshal(v, &cred); err != nil {
				return err
			}
			if strings.EqualFold(cred.Registry, domain) {
				// Check for instance-specific match
				if cred.InstanceID == instanceID {
					if instanceMatch == nil || cred.UpdatedAt > instanceMatch.UpdatedAt {
						c := cred
						instanceMatch = &c
					}
				}
				// Check for global fallback (empty InstanceID)
				if cred.InstanceID == "" {
					if globalMatch == nil || cred.UpdatedAt > globalMatch.UpdatedAt {
						c := cred
						globalMatch = &c
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	// Return instance-specific match if found, otherwise fall back to global
	if instanceMatch != nil {
		return instanceMatch, nil
	}
	return globalMatch, nil
}

// Instances

// SaveInstance saves an instance to the database
func (d *DB) SaveInstance(instance Instance) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketInstances)
		data, err := json.Marshal(instance)
		if err != nil {
			return err
		}
		return b.Put([]byte(instance.ID), data)
	})
}

// GetInstance retrieves an instance by ID
func (d *DB) GetInstance(id string) (*Instance, error) {
	var instance Instance
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketInstances)
		data := b.Get([]byte(id))
		if data == nil {
			return ErrInstanceNotFound
		}
		return json.Unmarshal(data, &instance)
	})
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// ListInstances returns all instances
func (d *DB) ListInstances() ([]Instance, error) {
	var instances []Instance
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketInstances)
		return b.ForEach(func(k, v []byte) error {
			var instance Instance
			if err := json.Unmarshal(v, &instance); err != nil {
				return err
			}
			instances = append(instances, instance)
			return nil
		})
	})
	return instances, err
}

// DeleteInstance deletes an instance by ID
func (d *DB) DeleteInstance(id string) error {
	// Reject deletion of the "local" instance
	if id == "local" {
		return ErrCannotDeleteLocal
	}
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketInstances)
		data := b.Get([]byte(id))
		if data == nil {
			return ErrInstanceNotFound
		}
		return b.Delete([]byte(id))
	})
}

// UpdateInstanceStatus updates the status of an instance
func (d *DB) UpdateInstanceStatus(id string, status string) error {
	instance, err := d.GetInstance(id)
	if err != nil {
		return err
	}
	instance.Status = status
	return d.SaveInstance(*instance)
}

// UpdateInstanceLastSeen updates the last seen timestamp of an instance
func (d *DB) UpdateInstanceLastSeen(id string, timestamp int64) error {
	instance, err := d.GetInstance(id)
	if err != nil {
		return err
	}
	instance.LastSeen = timestamp
	return d.SaveInstance(*instance)
}

// UpdateInstanceInfo updates instance information (DockerVersion, OS, CPUCores, TotalMemory)
func (d *DB) UpdateInstanceInfo(id string, info Instance) error {
	instance, err := d.GetInstance(id)
	if err != nil {
		return err
	}
	instance.DockerVersion = info.DockerVersion
	instance.OS = info.OS
	instance.CPUCores = info.CPUCores
	instance.TotalMemory = info.TotalMemory
	return d.SaveInstance(*instance)
}

// EnsureLocalInstance creates the local instance if it doesn't exist
func (d *DB) EnsureLocalInstance() error {
	_, err := d.GetInstance("local")
	if err != nil {
		if errors.Is(err, ErrInstanceNotFound) {
			return d.SaveInstance(Instance{
				ID:        "local",
				Name:      "This Server",
				Mode:      "local",
				Status:    "online",
				CreatedAt: time.Now().Unix(),
			})
		}
		return err
	}
	return nil
}

// Audit Logs
func (d *DB) SaveAuditLog(log AuditLog) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketAuditLogs)
		data, err := json.Marshal(log)
		if err != nil {
			return err
		}
		// Key: <unix_nano>_<id>
		key := fmt.Sprintf("%019d_%s", time.Now().UnixNano(), log.ID)
		return b.Put([]byte(key), data)
	})
}

func (d *DB) ListAuditLogs(limit, offset int) ([]AuditLog, int, error) {
	var logs []AuditLog
	var total int
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketAuditLogs)
		c := b.Cursor()

		// Count total
		total = 0
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			total++
		}

		if offset >= total {
			return nil
		}

		// Iterate backwards to get newest first
		skipped := 0
		var k, v []byte
		for k, v = c.Last(); k != nil; k, v = c.Prev() {
			if skipped < offset {
				skipped++
				continue
			}
			if len(logs) >= limit && limit > 0 {
				break
			}
			var logEntry AuditLog
			if err := json.Unmarshal(v, &logEntry); err != nil {
				return err
			}
			logs = append(logs, logEntry)
		}
		return nil
	})
	return logs, total, err
}
