// Package config provides centralized configuration loading for dockpal.
// It reads environment variables in a type-safe manner and provides
// sensible defaults, replacing scattered os.Getenv() calls throughout main.go.
//
// This is a lightweight alternative to Viper that doesn't add external
// dependencies while providing the same benefits: centralized config,
// defaults, and type-safe access.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults
const (
	DefaultDataDir         = "/opt/dockpal/data"
	DefaultPort            = "3012"
	DefaultTLSPort         = "3443"
	DefaultBackupInterval  = 24 * time.Hour
	DefaultBackupRetention = 168 * time.Hour // 7 days
	DefaultAuditRetention  = 2160 * time.Hour // 90 days
	DefaultShutdownTimeout = 30 * time.Second
)

// EnvConfig holds all configuration values read from the environment.
// This is the single source of truth for all env-var-based configuration.
type EnvConfig struct {
	// Paths
	DataDir    string
	DBPath     string
	LogPath    string
	SecretPath string

	// Server
	Port             string
	ShutdownTimeout  time.Duration
	MaxRequestBody   int64

	// TLS
	TLS       bool
	TLSCert   string
	TLSKey    string
	TLSDomain string

	// Auth
	AdminPassword string
	JWTSecret     string

	// Backup
	BackupInterval  time.Duration
	BackupRetention time.Duration

	// Audit
	AuditRetention time.Duration

	// Agent
	AgentImage string
}

// LoadEnvConfig reads all configuration from environment variables and
// returns a populated EnvConfig with defaults applied.
func LoadEnvConfig() *EnvConfig {
	cfg := &EnvConfig{}

	// Paths
	cfg.DataDir = envOrDefault("DOCKPAL_DATA_DIR", DefaultDataDir)
	cfg.DBPath = envOrDefault("DOCKPAL_DB_PATH", cfg.DataDir+"/dockpal.db")
	cfg.LogPath = envOrDefault("DOCKPAL_LOG_PATH", cfg.DataDir+"/dockpal.log")
	cfg.SecretPath = envOrDefault("DOCKPAL_SECRET_PATH", cfg.DataDir+"/.secret")

	// Server
	cfg.Port = envOrDefault("PORT", DefaultPort)
	cfg.ShutdownTimeout = envDurationOrDefault("DOCKPAL_SHUTDOWN_TIMEOUT", DefaultShutdownTimeout)
	cfg.MaxRequestBody = envInt64OrDefault("DOCKPAL_MAX_REQUEST_BODY_BYTES", 10<<20) // 10MB

	// TLS
	cfg.TLS = os.Getenv("DOCKPAL_TLS") == "true"
	cfg.TLSCert = os.Getenv("DOCKPAL_TLS_CERT")
	cfg.TLSKey = os.Getenv("DOCKPAL_TLS_KEY")
	cfg.TLSDomain = os.Getenv("DOCKPAL_TLS_DOMAIN")
	if cfg.TLS && cfg.Port == DefaultPort {
		cfg.Port = DefaultTLSPort
	}

	// Auth
	cfg.AdminPassword = os.Getenv("DOCKPAL_INITIAL_ADMIN_PASSWORD")
	cfg.JWTSecret = os.Getenv("JWT_SECRET")

	// Backup
	cfg.BackupInterval = envDurationOrDefault("DOCKPAL_BACKUP_INTERVAL", DefaultBackupInterval)
	cfg.BackupRetention = envDurationOrDefault("DOCKPAL_BACKUP_RETENTION", DefaultBackupRetention)

	// Audit
	cfg.AuditRetention = envDurationOrDefault("DOCKPAL_AUDIT_LOG_RETENTION", DefaultAuditRetention)

	// Agent
	cfg.AgentImage = os.Getenv("DOCKPAL_AGENT_IMAGE")

	return cfg
}

// ToValidationConfig converts EnvConfig to the validation Config struct
// used by the existing config.Validator.
func (c *EnvConfig) ToValidationConfig() *Config {
	return &Config{
		DataDir:       c.DataDir,
		DBPath:        c.DBPath,
		LogPath:       c.LogPath,
		SecretPath:    c.SecretPath,
		Port:          c.Port,
		TLS:           c.TLS,
		TLSCert:       c.TLSCert,
		TLSKey:        c.TLSKey,
		TLSDomain:     c.TLSDomain,
		AdminPassword: c.AdminPassword,
		JWTSecret:     c.JWTSecret,
	}
}

// envOrDefault returns the value of the environment variable, or the default if unset.
func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// envDurationOrDefault reads a duration from the environment.
// If the variable is set but invalid, it returns an error.
func envDurationOrDefault(key string, defaultVal time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		// Log the error but use default — don't crash on invalid config
		fmt.Fprintf(os.Stderr, "WARNING: Invalid %s value %q, using default %s\n", key, raw, defaultVal)
		return defaultVal
	}
	return d
}

// envInt64OrDefault reads an int64 from the environment.
func envInt64OrDefault(key string, defaultVal int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		fmt.Fprintf(os.Stderr, "WARNING: Invalid %s value %q, using default %d\n", key, raw, defaultVal)
		return defaultVal
	}
	return n
}
