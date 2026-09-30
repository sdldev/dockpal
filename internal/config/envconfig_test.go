package config

import (
	"testing"
	"time"
)

func TestLoadEnvConfig_Defaults(t *testing.T) {
	// Clear all relevant env vars to test defaults
	envVars := []string{
		"DOCKPAL_DATA_DIR", "DOCKPAL_DB_PATH", "DOCKPAL_LOG_PATH", "DOCKPAL_SECRET_PATH",
		"PORT", "DOCKPAL_TLS", "DOCKPAL_TLS_CERT", "DOCKPAL_TLS_KEY", "DOCKPAL_TLS_DOMAIN",
		"DOCKPAL_INITIAL_ADMIN_PASSWORD", "JWT_SECRET",
		"DOCKPAL_BACKUP_INTERVAL", "DOCKPAL_BACKUP_RETENTION", "DOCKPAL_AUDIT_LOG_RETENTION",
		"DOCKPAL_SHUTDOWN_TIMEOUT", "DOCKPAL_MAX_REQUEST_BODY_BYTES", "DOCKPAL_AGENT_IMAGE",
	}
	for _, v := range envVars {
		t.Setenv(v, "")
	}

	cfg := LoadEnvConfig()

	if cfg.DataDir != DefaultDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, DefaultDataDir)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %q, want %q", cfg.Port, DefaultPort)
	}
	if cfg.BackupInterval != DefaultBackupInterval {
		t.Errorf("BackupInterval = %v, want %v", cfg.BackupInterval, DefaultBackupInterval)
	}
	if cfg.BackupRetention != DefaultBackupRetention {
		t.Errorf("BackupRetention = %v, want %v", cfg.BackupRetention, DefaultBackupRetention)
	}
	if cfg.AuditRetention != DefaultAuditRetention {
		t.Errorf("AuditRetention = %v, want %v", cfg.AuditRetention, DefaultAuditRetention)
	}
	if cfg.TLS {
		t.Error("TLS should be false by default")
	}
	if cfg.AdminPassword != "" {
		t.Error("AdminPassword should be empty by default")
	}
}

func TestLoadEnvConfig_CustomValues(t *testing.T) {
	t.Setenv("DOCKPAL_DATA_DIR", "/custom/data")
	t.Setenv("PORT", "8080")
	t.Setenv("DOCKPAL_TLS", "true")
	t.Setenv("DOCKPAL_BACKUP_INTERVAL", "12h")
	t.Setenv("DOCKPAL_INITIAL_ADMIN_PASSWORD", "testpass123")

	cfg := LoadEnvConfig()

	if cfg.DataDir != "/custom/data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "/custom/data")
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, "8080")
	}
	if !cfg.TLS {
		t.Error("TLS should be true")
	}
	if cfg.BackupInterval != 12*time.Hour {
		t.Errorf("BackupInterval = %v, want %v", cfg.BackupInterval, 12*time.Hour)
	}
	if cfg.AdminPassword != "testpass123" {
		t.Errorf("AdminPassword = %q, want %q", cfg.AdminPassword, "testpass123")
	}
}

func TestLoadEnvConfig_TLSDefaultPort(t *testing.T) {
	t.Setenv("DOCKPAL_TLS", "true")
	t.Setenv("PORT", "")

	cfg := LoadEnvConfig()

	if cfg.Port != DefaultTLSPort {
		t.Errorf("Port with TLS = %q, want %q", cfg.Port, DefaultTLSPort)
	}
}

func TestLoadEnvConfig_DerivedPaths(t *testing.T) {
	t.Setenv("DOCKPAL_DATA_DIR", "/my/data")
	t.Setenv("DOCKPAL_DB_PATH", "")
	t.Setenv("DOCKPAL_LOG_PATH", "")
	t.Setenv("DOCKPAL_SECRET_PATH", "")

	cfg := LoadEnvConfig()

	if cfg.DBPath != "/my/data/dockpal.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/my/data/dockpal.db")
	}
	if cfg.LogPath != "/my/data/dockpal.log" {
		t.Errorf("LogPath = %q, want %q", cfg.LogPath, "/my/data/dockpal.log")
	}
	if cfg.SecretPath != "/my/data/.secret" {
		t.Errorf("SecretPath = %q, want %q", cfg.SecretPath, "/my/data/.secret")
	}
}

func TestToValidationConfig(t *testing.T) {
	t.Setenv("DOCKPAL_DATA_DIR", "/test/data")
	t.Setenv("DOCKPAL_TLS", "true")
	t.Setenv("DOCKPAL_TLS_CERT", "/cert.pem")
	t.Setenv("DOCKPAL_TLS_KEY", "/key.pem")

	cfg := LoadEnvConfig()
	vc := cfg.ToValidationConfig()

	if vc.DataDir != "/test/data" {
		t.Errorf("ValidationConfig DataDir = %q, want %q", vc.DataDir, "/test/data")
	}
	if !vc.TLS {
		t.Error("ValidationConfig TLS should be true")
	}
	if vc.TLSCert != "/cert.pem" {
		t.Errorf("ValidationConfig TLSCert = %q, want %q", vc.TLSCert, "/cert.pem")
	}
}
