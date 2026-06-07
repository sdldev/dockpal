package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	backupPkg "github.com/sdldev/dockpal/internal/backup"
	"github.com/sdldev/dockpal/internal/config"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/logging"
	"github.com/sdldev/dockpal/internal/metrics"
	"github.com/sdldev/dockpal/internal/registry"
	"github.com/sdldev/dockpal/internal/server"
	"github.com/sdldev/dockpal/web"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultDataDir = "/opt/dockpal/data"
	defaultDBPath  = "/opt/dockpal/data/dockpal.db"
	defaultLogPath = "/opt/dockpal/data/dockpal.log"
)

var version = "0.9.0-dev"

func init() {
	version = strings.TrimPrefix(version, "v")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Dockpal — Simple & powerful Docker management platform")
		fmt.Printf("Version: %s\n", version)
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  dockpal server          Start the HTTP/HTTPS server")
		fmt.Println("  dockpal backup          Create a database backup")
		fmt.Println("  dockpal restore         Restore database from a backup")
		fmt.Println("  dockpal reset-password  Reset admin password")
		fmt.Println("  dockpal version         Show version")
		fmt.Println("  dockpal help            Show this help")
		return
	}

	switch os.Args[1] {
	case "server":
		serverCmd := flag.NewFlagSet("server", flag.ExitOnError)

		envTLS := os.Getenv("DOCKPAL_TLS") == "true"
		envTLSCert := os.Getenv("DOCKPAL_TLS_CERT")
		envTLSKey := os.Getenv("DOCKPAL_TLS_KEY")
		envTLSDomain := os.Getenv("DOCKPAL_TLS_DOMAIN")

		tls := serverCmd.Bool("tls", envTLS, "Enable TLS (HTTPS)")
		tlsCert := serverCmd.String("tls-cert", envTLSCert, "Path to TLS certificate file")
		tlsKey := serverCmd.String("tls-key", envTLSKey, "Path to TLS private key file")
		tlsDomain := serverCmd.String("tls-domain", envTLSDomain, "Domain name for Let's Encrypt autocert")

		serverCmd.Parse(os.Args[2:])
		runServer(*tls, *tlsCert, *tlsKey, *tlsDomain)
	case "backup":
		backupCmd := flag.NewFlagSet("backup", flag.ExitOnError)
		output := backupCmd.String("output", "", "Backup output path (default: <data_dir>/backups/dockpal-<timestamp>.db)")
		backupCmd.Parse(os.Args[2:])
		backup(*output)
	case "restore":
		restoreCmd := flag.NewFlagSet("restore", flag.ExitOnError)
		from := restoreCmd.String("from", "", "Path to backup file to restore from (required)")
		force := restoreCmd.Bool("force", false, "Skip confirmation prompt")
		restoreCmd.Parse(os.Args[2:])
		restore(*from, *force)
	case "reset-password":
		resetCmd := flag.NewFlagSet("reset-password", flag.ExitOnError)
		resetUsername := resetCmd.String("username", "admin", "Username to reset")
		resetPassword := resetCmd.String("password", "", "New password (min 8 chars); omit to generate a random one")
		resetCmd.Parse(os.Args[2:])
		runResetPassword(*resetUsername, *resetPassword)
	case "rotate-secrets":
		rotateSecrets()
	case "version":
		fmt.Printf("Dockpal v%s\n", version)
	case "help":
		fmt.Println("Dockpal — Simple & powerful Docker management platform")
		fmt.Printf("Version: %s\n", version)
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  server          Start the HTTP/HTTPS server")
		fmt.Println("  backup          Create a database backup")
		fmt.Println("  restore         Restore database from a backup")
		fmt.Println("  reset-password  Reset a user's password (--username, --password)")
		fmt.Println("  rotate-secrets  Rotate JWT/encryption secret")
		fmt.Println("  version         Show version")
		fmt.Println("  help            Show this help")
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runServer(tls bool, tlsCert, tlsKey, tlsDomain string) {
	dataDir := os.Getenv("DOCKPAL_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	dataDir = mustAbs("DOCKPAL_DATA_DIR", dataDir)

	if err := os.MkdirAll(dataDir, 0750); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Set up log rotation
	logPath := os.Getenv("DOCKPAL_LOG_PATH")
	if logPath == "" {
		logPath = filepath.Join(dataDir, "dockpal.log")
	}
	logPath = mustAbs("DOCKPAL_LOG_PATH", logPath)

	logRotator, err := logging.NewLogRotatorWithAge(
		logPath,
		parseSizeEnv("DOCKPAL_LOG_MAX_SIZE", logging.DefaultMaxSize),
		parseIntEnv("DOCKPAL_LOG_MAX_FILES", logging.DefaultMaxFiles),
		parseDurationEnv("DOCKPAL_LOG_MAX_AGE", logging.DefaultMaxAge),
	)
	if err != nil {
		log.Fatalf("Failed to initialize log rotator: %v", err)
	}
	defer logRotator.Close()
	log.SetOutput(logRotator)
	logging.ConfigureJSON(logRotator)

	dbPath := os.Getenv("DOCKPAL_DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "dockpal.db")
	}
	dbPath = mustAbs("DOCKPAL_DB_PATH", dbPath)

	secretPath := os.Getenv("DOCKPAL_SECRET_PATH")
	if secretPath == "" {
		secretPath = filepath.Join(dataDir, ".secret")
	}
	secretPath = mustAbs("DOCKPAL_SECRET_PATH", secretPath)

	// Get port configuration
	port := os.Getenv("PORT")
	if port == "" {
		if tls {
			port = "3443"
		} else {
			port = "3012"
		}
	}

	// Get admin password and JWT secret for validation
	adminPassword := os.Getenv("DOCKPAL_INITIAL_ADMIN_PASSWORD")
	jwtSecret := os.Getenv("JWT_SECRET")

	// Perform comprehensive configuration validation
	cfg := &config.Config{
		DataDir:       dataDir,
		DBPath:        dbPath,
		LogPath:       logPath,
		SecretPath:    secretPath,
		Port:          port,
		TLS:           tls,
		TLSCert:       tlsCert,
		TLSKey:        tlsKey,
		TLSDomain:     tlsDomain,
		AdminPassword: adminPassword,
		JWTSecret:     jwtSecret,
	}

	validator := config.NewValidator(cfg)
	result := validator.ValidateConfig()

	if !result.IsValid {
		log.Println("Configuration validation failed:")
		for _, err := range result.Errors {
			log.Printf("  ERROR: %s", err)
		}
		log.Fatalf("Please fix the configuration errors above and restart Dockpal")
	}

	if len(result.Warnings) > 0 {
		log.Println("Configuration validation warnings:")
		for _, warning := range result.Warnings {
			log.Printf("  WARNING: %s", warning)
		}
	}

	// Load or generate JWT secret after validation
	jwtSecret, err = auth.LoadOrGenerateSecretAt(secretPath)
	if err != nil {
		log.Fatalf("Failed to load or generate JWT secret: %v", err)
	}

	database, err := db.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Ensure default admin user exists
	generatedAdminPassword := false
	if adminPassword == "" {
		var err error
		adminPassword, err = generatePassword(12)
		if err != nil {
			log.Fatalf("Failed to generate initial admin password: %v", err)
		}
		generatedAdminPassword = true
	}
	if len(adminPassword) < 8 {
		log.Fatal("DOCKPAL_INITIAL_ADMIN_PASSWORD must be at least 8 characters")
	}
	defaultHash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Failed to hash default admin password: %v", err)
	}
	if err := database.EnsureDefaultAdmin(string(defaultHash)); err != nil {
		log.Fatalf("Failed to create default admin: %v", err)
	}
	if generatedAdminPassword {
		log.Printf("Generated initial admin password for username admin: %s", adminPassword)
		fmt.Fprintf(os.Stderr, "Generated initial admin password for username admin: %s\n", adminPassword)
		log.Printf("Set DOCKPAL_INITIAL_ADMIN_PASSWORD before first startup to choose a bootstrap password")
	}

	// Ensure local instance exists
	if err := database.EnsureLocalInstance(); err != nil {
		log.Fatalf("Failed to ensure local instance: %v", err)
	}

	dockerClient, err := docker.NewClient()
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	defer dockerClient.Close()

	if err := dockerClient.Ping(context.Background()); err != nil {
		log.Fatalf("Failed to connect to Docker daemon: %v", err)
	}

	// Start container auto-recovery health monitor
	healthMonitor := docker.NewHealthMonitor(dockerClient)
	healthMonitor.Start()
	defer healthMonitor.Stop()

	srv := server.New(tls, tlsCert, tlsKey, tlsDomain, dataDir)
	srv.Router().Use(server.SecurityHeadersMiddleware())
	srv.Router().Use(server.CORSMiddleware())

	// Initialize agent manager (for multi-instance support)
	agentMgr, err := agent.NewManager(database, dockerClient, jwtSecret)
	if err != nil {
		log.Fatalf("Failed to create agent manager: %v", err)
	}

	// Initialize Prometheus metrics
	if err := metrics.RegisterMetrics("v" + version); err != nil {
		log.Fatalf("Failed to register Prometheus metrics: %v", err)
	}
	metricsCollector := metrics.NewMetricsCollector(agentMgr, "v"+version)
	metricsCollector.Start()
	defer metricsCollector.Stop()

	// Add HTTP metrics middleware
	srv.Router().Use(metrics.MetricsMiddleware())

	appCtx, cancelApp := context.WithCancel(context.Background())
	server.RegisterRoutes(appCtx, srv.Router(), dockerClient, jwtSecret, database, agentMgr, dataDir, dbPath, version)

	// Initialize and start background backup scheduler
	backupInterval := parseDurationEnv("DOCKPAL_BACKUP_INTERVAL", 24*time.Hour)
	backupRetention := parseDurationEnv("DOCKPAL_BACKUP_RETENTION", 168*time.Hour)
	backupScheduler := backupPkg.NewScheduler(database, dataDir, backupInterval, backupRetention)
	backupScheduler.Start(appCtx)

	auditRetention := parseDurationEnv("DOCKPAL_AUDIT_LOG_RETENTION", 2160*time.Hour)
	startAuditRetentionWorker(appCtx, database, auditRetention)

	// Serve embedded frontend
	assetsFS, _ := fs.Sub(web.Assets, "assets")
	srv.Router().StaticFS("/assets", http.FS(assetsFS))
	// Assemble HTML once at startup (resolves <!--#include --> directives)
	indexHTML, err := web.AssembleHTML()
	if err != nil {
		log.Fatalf("Failed to assemble index.html: %v", err)
	}
	indexBytes := []byte(indexHTML)
	srv.Router().GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexBytes)
	})
	// SPA fallback: serve index.html for client-side routes (/dashboard, /containers, etc.)
	srv.Router().NoRoute(func(c *gin.Context) {
		if c.Request.Method == "GET" && !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexBytes)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	go func() {
		if err := srv.Run(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	slog.Info("server started", "component", "startup", "version", version, "port", 3012)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutdown requested", "component", "shutdown")
	cancelApp()
	backupScheduler.Stop()
	server.StopBackgroundWorkers()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), srv.ShutdownTimeout())
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown failed", "component", "shutdown", "error", err)
	}
}

func backup(outputPath string) {
	dataDir := os.Getenv("DOCKPAL_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	dataDir = mustAbs("DOCKPAL_DATA_DIR", dataDir)

	if outputPath == "" {
		backupDir := filepath.Join(dataDir, "backups")
		timestamp := time.Now().Format("20060102-150405")
		outputPath = filepath.Join(backupDir, fmt.Sprintf("dockpal-%s.db", timestamp))
	}
	outputPath = mustAbs("output", outputPath)

	dbPath := os.Getenv("DOCKPAL_DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "dockpal.db")
	}
	dbPath = mustAbs("DOCKPAL_DB_PATH", dbPath)

	database, err := db.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	if err := database.BackupTo(outputPath); err != nil {
		log.Fatalf("Backup failed: %v", err)
	}
	checksumVerified, err := db.ValidateBackup(outputPath)
	if err != nil {
		log.Fatalf("Backup verification failed: %v", err)
	}

	checksumPath := outputPath + ".sha256"
	checksum, _ := os.ReadFile(checksumPath)
	fmt.Printf("Backup created: %s\n", outputPath)
	fmt.Printf("Checksum: %s\n", strings.TrimSpace(string(checksum)))
	if checksumVerified {
		fmt.Println("Verification: OK")
	} else {
		fmt.Println("Verification: OK (checksum sidecar missing)")
	}
}

func restore(fromPath string, force bool) {
	if fromPath == "" {
		fmt.Println("Usage: dockpal restore --from <backup-file> [--force]")
		os.Exit(1)
	}
	fromPath = mustAbs("from", fromPath)

	dataDir := os.Getenv("DOCKPAL_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	dataDir = mustAbs("DOCKPAL_DATA_DIR", dataDir)

	dbPath := os.Getenv("DOCKPAL_DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "dockpal.db")
	}
	dbPath = mustAbs("DOCKPAL_DB_PATH", dbPath)

	checksumVerified, err := db.ValidateBackup(fromPath)
	if err != nil {
		log.Fatalf("Backup validation failed: %v", err)
	}
	if !checksumVerified {
		fmt.Println("WARNING: No .sha256 checksum file found — backup integrity could not be fully verified.")
	}

	if !force {
		fmt.Printf("This will replace the current database at %s with %s. Continue? [y/N]: ", dbPath, fromPath)
		var answer string
		fmt.Scanln(&answer)
		if strings.ToLower(answer) != "y" {
			fmt.Println("Restore cancelled.")
			os.Exit(0)
		}
	}

	input, err := os.ReadFile(fromPath)
	if err != nil {
		log.Fatalf("Failed to read backup file: %v", err)
	}

	tmpPath := dbPath + ".restore.tmp"
	if err := os.WriteFile(tmpPath, input, 0600); err != nil {
		log.Fatalf("Failed to write restored database: %v", err)
	}

	if err := os.Rename(tmpPath, dbPath); err != nil {
		_ = os.Remove(tmpPath)
		log.Fatalf("Failed to replace database: %v", err)
	}

	fmt.Println("Database restored successfully")
}

func rotateSecrets() {
	if os.Getenv("JWT_SECRET") != "" {
		log.Fatal("rotate-secrets requires file-based secrets; unset JWT_SECRET and use DOCKPAL_SECRET_PATH")
	}

	dataDir := os.Getenv("DOCKPAL_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	dataDir = mustAbs("DOCKPAL_DATA_DIR", dataDir)

	dbPath := os.Getenv("DOCKPAL_DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "dockpal.db")
	}
	dbPath = mustAbs("DOCKPAL_DB_PATH", dbPath)

	secretPath := os.Getenv("DOCKPAL_SECRET_PATH")
	if secretPath == "" {
		secretPath = filepath.Join(dataDir, ".secret")
	}
	secretPath = mustAbs("DOCKPAL_SECRET_PATH", secretPath)

	oldSecret, err := auth.LoadOrGenerateSecretAt(secretPath)
	if err != nil {
		log.Fatalf("Failed to load current secret: %v", err)
	}
	newSecret, err := generateSecretHex(32)
	if err != nil {
		log.Fatalf("Failed to generate new secret: %v", err)
	}
	oldKey, err := registry.DeriveKey(oldSecret)
	if err != nil {
		log.Fatalf("Failed to derive old encryption key: %v", err)
	}
	newKey, err := registry.DeriveKey(newSecret)
	if err != nil {
		log.Fatalf("Failed to derive new encryption key: %v", err)
	}

	database, err := db.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	backupPath := filepath.Join(dataDir, "backups", fmt.Sprintf("pre-secret-rotation-%s.db", time.Now().Format("20060102-150405")))
	if err := database.BackupTo(backupPath); err != nil {
		log.Fatalf("Pre-rotation backup failed: %v", err)
	}
	if _, err := db.ValidateBackup(backupPath); err != nil {
		log.Fatalf("Pre-rotation backup verification failed: %v", err)
	}

	registries, err := database.ListRegistryCredentials()
	if err != nil {
		log.Fatalf("Failed to list registry credentials: %v", err)
	}
	for _, cred := range registries {
		plaintext, err := registry.Decrypt(cred.EncryptedToken, oldKey)
		if err != nil {
			log.Fatalf("Failed to decrypt registry credential %s: %v", cred.ID, err)
		}
		cred.EncryptedToken, err = registry.Encrypt(plaintext, newKey)
		for i := range plaintext {
			plaintext[i] = 0
		}
		if err != nil {
			log.Fatalf("Failed to re-encrypt registry credential %s: %v", cred.ID, err)
		}
		cred.UpdatedAt = time.Now().Unix()
		if err := database.SaveRegistryCredential(cred); err != nil {
			log.Fatalf("Failed to save registry credential %s: %v", cred.ID, err)
		}
	}

	instances, err := database.ListInstances()
	if err != nil {
		log.Fatalf("Failed to list instances: %v", err)
	}
	for _, inst := range instances {
		if len(inst.AgentTokenEncrypted) == 0 {
			continue
		}
		plaintext, err := registry.Decrypt(inst.AgentTokenEncrypted, oldKey)
		if err != nil {
			log.Fatalf("Failed to decrypt instance token %s: %v", inst.ID, err)
		}
		inst.AgentTokenEncrypted, err = registry.Encrypt(plaintext, newKey)
		for i := range plaintext {
			plaintext[i] = 0
		}
		if err != nil {
			log.Fatalf("Failed to re-encrypt instance token %s: %v", inst.ID, err)
		}
		if err := database.SaveInstance(inst); err != nil {
			log.Fatalf("Failed to save instance %s: %v", inst.ID, err)
		}
	}

	if err := database.IncrementAllTokenVersions(); err != nil {
		log.Fatalf("Failed to invalidate existing JWT tokens: %v", err)
	}
	if err := os.WriteFile(secretPath, []byte(newSecret), 0600); err != nil {
		log.Fatalf("Failed to write new secret: %v", err)
	}

	fmt.Printf("Secrets rotated successfully. Verified backup: %s\n", backupPath)
}

// generatePassword creates a cryptographically random alphanumeric password.
func generatePassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}
	return string(result), nil
}

func generateSecretHex(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func runResetPassword(username, password string) {
	dbPath := os.Getenv("DOCKPAL_DB_PATH")
	if dbPath == "" {
		dbPath = defaultDBPath
	}
	dbPath = mustAbs("DOCKPAL_DB_PATH", dbPath)

	database, err := db.New(dbPath)
	if err != nil {
		log.Printf("Failed to open database: %v", err)
		log.Fatalf("If Dockpal server is running, stop it first: systemctl stop dockpal")
	}
	defer database.Close()

	generated := false
	if password == "" {
		password, err = generatePassword(12)
		if err != nil {
			log.Fatalf("Failed to generate password: %v", err)
		}
		generated = true
	}

	if len(password) < 8 {
		log.Fatalf("Password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	if err := database.UpdatePasswordWithVersion(username, string(hash)); err != nil {
		log.Fatalf("Failed to update password: %v", err)
	}

	fmt.Printf("Dockpal: password reset successfully\n")
	fmt.Printf("Username: %s\n", username)
	if generated {
		fmt.Printf("New password (generated): %s\n", password)
	} else {
		fmt.Printf("Password updated.\n")
	}
}

// parseDurationEnv reads a duration from an environment variable.
// If the variable is unset or empty, it returns the default.
// If the variable is set but invalid, it logs a fatal error.
func parseDurationEnv(name string, defaultVal time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Fatalf("Invalid %s value %q: %v", name, raw, err)
	}
	return d
}

func startAuditRetentionWorker(ctx context.Context, database *db.DB, retention time.Duration) {
	if retention == 0 {
		slog.Info("audit log retention disabled", "component", "audit")
		return
	}
	purge := func() {
		cutoff := time.Now().Add(-retention)
		deleted, err := database.PurgeAuditLogsOlderThan(cutoff)
		if err != nil {
			slog.Error("audit log purge failed", "component", "audit", "cutoff", cutoff.Format(time.RFC3339), "error", err)
			return
		}
		if deleted > 0 {
			slog.Info("audit logs purged", "component", "audit", "deleted", deleted, "cutoff", cutoff.Format(time.RFC3339))
		}
	}
	purge()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				purge()
			}
		}
	}()
}

// parseIntEnv reads a positive integer from an environment variable.
// If the variable is unset or empty, it returns the default.
// If the variable is set but invalid or non-positive, it logs a fatal error.
func parseIntEnv(name string, defaultVal int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		log.Fatalf("Invalid %s value %q: must be a positive integer", name, raw)
	}
	return n
}

// parseSizeEnv reads a byte-size value from an environment variable.
// Accepts plain integers (bytes) or units: B, KB, MB, GB (case-insensitive,
// 1024-based). Returns defaultVal if the variable is unset.
// Logs a fatal error if the value is invalid.
func parseSizeEnv(name string, defaultVal int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultVal
	}
	upper := strings.ToUpper(raw)
	multiplier := int64(1)
	numeric := upper
	switch {
	case strings.HasSuffix(upper, "GB"):
		multiplier = 1024 * 1024 * 1024
		numeric = strings.TrimSuffix(upper, "GB")
	case strings.HasSuffix(upper, "MB"):
		multiplier = 1024 * 1024
		numeric = strings.TrimSuffix(upper, "MB")
	case strings.HasSuffix(upper, "KB"):
		multiplier = 1024
		numeric = strings.TrimSuffix(upper, "KB")
	case strings.HasSuffix(upper, "B"):
		numeric = strings.TrimSuffix(upper, "B")
	}
	numeric = strings.TrimSpace(numeric)
	n, err := strconv.ParseInt(numeric, 10, 64)
	if err != nil || n <= 0 {
		log.Fatalf("Invalid %s value %q: must be a positive size (e.g. 2MB, 512KB)", name, raw)
	}
	return n * multiplier
}

// mustAbs validates that the given value is an absolute path.
// If not, it logs a fatal error and exits.
// This satisfies Requirement 3.9.
func mustAbs(name, value string) string {
	if !strings.HasPrefix(value, "/") {
		log.Fatalf("env var %s must be an absolute path, got %q", name, value)
	}
	return value
}
