package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/auth"
	backupPkg "github.com/sdldev/dockpal/internal/backup"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"github.com/sdldev/dockpal/internal/logging"
	"github.com/sdldev/dockpal/internal/server"
	"github.com/sdldev/dockpal/internal/update"
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
		resetPassword()
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
		fmt.Println("  reset-password  Reset admin password")
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

	logRotator, err := logging.NewLogRotator(logPath, logging.DefaultMaxSize, logging.DefaultMaxFiles)
	if err != nil {
		log.Fatalf("Failed to initialize log rotator: %v", err)
	}
	defer logRotator.Close()
	log.SetOutput(logRotator)

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

	jwtSecret, err := auth.LoadOrGenerateSecretAt(secretPath)
	if err != nil {
		log.Fatalf("Failed to load or generate JWT secret: %v", err)
	}

	database, err := db.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Ensure default admin user exists
	adminPassword := os.Getenv("DOCKPAL_INITIAL_ADMIN_PASSWORD")
	generatedAdminPassword := false
	if adminPassword == "" {
		passwordBytes := make([]byte, 18)
		if _, err := rand.Read(passwordBytes); err != nil {
			log.Fatalf("Failed to generate initial admin password: %v", err)
		}
		adminPassword = hex.EncodeToString(passwordBytes)
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

	// Initialize version service
	versionService := update.NewVersionService(dataDir, "v"+version)

	// Initialize update service
	updateService := update.NewUpdateService("v" + version)

	// Initialize agent manager (for multi-instance support)
	agentMgr, err := agent.NewManager(database, dockerClient, jwtSecret)
	if err != nil {
		log.Fatalf("Failed to create agent manager: %v", err)
	}

	server.RegisterRoutes(srv.Router(), dockerClient, jwtSecret, database, versionService, updateService, agentMgr, dataDir)

	// Initialize and start background version check scheduler (6-hour interval)
	scheduler := update.NewVersionCheckScheduler(versionService, 6*time.Hour)
	ctx, cancelScheduler := context.WithCancel(context.Background())
	scheduler.Start(ctx, 6*time.Hour)

	// Initialize and start background backup scheduler
	backupInterval := parseDurationEnv("DOCKPAL_BACKUP_INTERVAL", 24*time.Hour)
	backupRetention := parseDurationEnv("DOCKPAL_BACKUP_RETENTION", 168*time.Hour)
	backupScheduler := backupPkg.NewScheduler(database, dataDir, backupInterval, backupRetention)
	backupScheduler.Start(ctx)

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
	srv.Router().NoRoute(func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexBytes)
	})

	go func() {
		if err := srv.Run(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	log.Printf("Dockpal v%s running on port 3012", version)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	// Stop the schedulers first for graceful shutdown
	cancelScheduler()
	scheduler.Stop()
	backupScheduler.Stop()
	srv.Shutdown(context.Background())
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

	checksumPath := outputPath + ".sha256"
	checksum, _ := os.ReadFile(checksumPath)
	fmt.Printf("Backup created: %s\n", outputPath)
	fmt.Printf("Checksum: %s\n", strings.TrimSpace(string(checksum)))
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

func resetPassword() {
	dbPath := os.Getenv("DOCKPAL_DB_PATH")
	if dbPath == "" {
		dbPath = defaultDBPath
	}
	dbPath = mustAbs("DOCKPAL_DB_PATH", dbPath)

	database, err := db.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	fmt.Print("Enter new admin password: ")
	var password string
	fmt.Scanln(&password)

	if len(password) < 8 {
		log.Fatal("Password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	if err := database.UpdatePasswordWithVersion("admin", string(hash)); err != nil {
		log.Fatalf("Failed to update password: %v", err)
	}

	fmt.Println("Dockpal: password reset successfully")
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

// mustAbs validates that the given value is an absolute path.
// If not, it logs a fatal error and exits.
// This satisfies Requirement 3.9.
func mustAbs(name, value string) string {
	if !strings.HasPrefix(value, "/") {
		log.Fatalf("env var %s must be an absolute path, got %q", name, value)
	}
	return value
}
