package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/auth"
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
	version        = "0.2.3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Dockpal — Simple & powerful Docker management platform")
		fmt.Printf("Version: %s\n", version)
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  dockpal server          Start the HTTP server")
		fmt.Println("  dockpal reset-password  Reset admin password")
		fmt.Println("  dockpal version         Show version")
		fmt.Println("  dockpal help            Show this help")
		return
	}

	switch os.Args[1] {
	case "server":
		runServer()
	case "reset-password":
		resetPassword()
	case "version":
		fmt.Printf("Dockpal v%s\n", version)
	case "help":
		fmt.Println("Dockpal — Simple & powerful Docker management platform")
		fmt.Printf("Version: %s\n", version)
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  server          Start the HTTP server")
		fmt.Println("  reset-password  Reset admin password")
		fmt.Println("  version         Show version")
		fmt.Println("  help            Show this help")
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runServer() {
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
		logPath = defaultLogPath
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
		dbPath = defaultDBPath
	}
	dbPath = mustAbs("DOCKPAL_DB_PATH", dbPath)

	jwtSecret, err := auth.LoadOrGenerateSecret()
	if err != nil {
		log.Fatalf("Failed to load or generate JWT secret: %v", err)
	}
	log.Println("JWT secret loaded successfully")

	database, err := db.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Ensure default admin user exists
	defaultHash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err := database.EnsureDefaultAdmin(string(defaultHash)); err != nil {
		log.Fatalf("Failed to create default admin: %v", err)
	}

	dockerClient, err := docker.NewClient()
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	defer dockerClient.Close()

	if err := dockerClient.Ping(context.Background()); err != nil {
		log.Fatalf("Failed to connect to Docker daemon: %v", err)
	}
	log.Println("Connected to Docker daemon")

	// Start container auto-recovery health monitor
	healthMonitor := docker.NewHealthMonitor(dockerClient)
	healthMonitor.Start()
	defer healthMonitor.Stop()

	srv := server.New()
	srv.Router().Use(server.CORSMiddleware())

	// Initialize version service
	versionService := update.NewVersionService(dataDir, "v"+version)

	// Initialize update service
	updateService := update.NewUpdateService("v" + version)

	server.RegisterRoutes(srv.Router(), dockerClient, jwtSecret, database, versionService, updateService)

	// Initialize and start background version check scheduler (6-hour interval)
	scheduler := update.NewVersionCheckScheduler(versionService, 6*time.Hour)
	ctx, cancelScheduler := context.WithCancel(context.Background())
	scheduler.Start(ctx, 6*time.Hour)

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
	// Stop the scheduler first for graceful shutdown
	cancelScheduler()
	scheduler.Stop()
	srv.Shutdown(context.Background())
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

	if len(password) < 4 {
		log.Fatal("Password must be at least 4 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	if err := database.UpdatePassword("admin", string(hash)); err != nil {
		log.Fatalf("Failed to update password: %v", err)
	}

	fmt.Println("Dockpal: password reset successfully")
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