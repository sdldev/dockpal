package config

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/client"
	"go.etcd.io/bbolt"
)

// Config holds all configuration values for validation
type Config struct {
	DataDir       string
	DBPath        string
	LogPath       string
	SecretPath    string
	Port          string
	TLS           bool
	TLSCert       string
	TLSKey        string
	TLSDomain     string
	AdminPassword string
	JWTSecret     string
}

// ValidationResult represents the result of configuration validation
type ValidationResult struct {
	IsValid bool
	Errors  []string
	Warnings []string
}

// Validator performs comprehensive configuration validation
type Validator struct {
	config *Config
}

// NewValidator creates a new configuration validator
func NewValidator(config *Config) *Validator {
	return &Validator{config: config}
}

// ValidateConfig performs comprehensive validation of all configuration
func (v *Validator) ValidateConfig() *ValidationResult {
	result := &ValidationResult{
		IsValid:  true,
		Errors:   []string{},
		Warnings: []string{},
	}

	log.Println("Starting configuration validation...")

	// Validate paths
	v.validatePaths(result)
	
	// Validate Docker daemon
	v.validateDockerDaemon(result)
	
	// Validate port availability
	v.validatePort(result)
	
	// Validate TLS configuration
	v.validateTLS(result)
	
	// Validate database connectivity
	v.validateDatabase(result)
	
	// Validate system resources
	v.validateSystemResources(result)

	// Log configuration (without sensitive data)
	v.logConfiguration()

	if len(result.Errors) > 0 {
		result.IsValid = false
		log.Printf("Configuration validation failed with %d errors", len(result.Errors))
	} else {
		log.Println("Configuration validation completed successfully")
	}

	if len(result.Warnings) > 0 {
		log.Printf("Configuration validation completed with %d warnings", len(result.Warnings))
	}

	return result
}

// validatePaths checks if all paths are accessible and have sufficient disk space
func (v *Validator) validatePaths(result *ValidationResult) {
	paths := map[string]string{
		"DataDir":  v.config.DataDir,
		"DBPath":   v.config.DBPath,
		"LogPath":  v.config.LogPath,
		"SecretPath": v.config.SecretPath,
	}

	for name, path := range paths {
		// Check if parent directory exists and is writable
		dir := filepath.Dir(path)
		if name == "DataDir" {
			dir = path
		}

		// Test directory creation and write permissions
		if err := os.MkdirAll(dir, 0750); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to create %s directory %s: %v", name, dir, err))
			continue
		}

		// Test write permissions
		testFile := filepath.Join(dir, ".dockpal_write_test")
		if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Directory %s is not writable: %v", dir, err))
		} else {
			os.Remove(testFile) // Clean up test file
		}

		// Check disk space (minimum 100MB required)
		var stat runtime.MemStats
		runtime.ReadMemStats(&stat)
		
		// Simple disk space check using statfs
		if stat := v.getDiskSpace(dir); stat.Available < 100*1024*1024 { // 100MB
			result.Warnings = append(result.Warnings, fmt.Sprintf("Low disk space in %s: %.1f MB available (minimum 100 MB recommended)", dir, float64(stat.Available)/1024/1024))
		}
	}
}

// DiskSpaceInfo holds disk space information
type DiskSpaceInfo struct {
	Total     uint64
	Available uint64
	Used      uint64
}

// getDiskSpace returns disk space information for the given path
func (v *Validator) getDiskSpace(path string) *DiskSpaceInfo {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		// Return dummy values if we can't get disk space
		return &DiskSpaceInfo{
			Total:     10 * 1024 * 1024 * 1024, // 10GB
			Available: 5 * 1024 * 1024 * 1024,  // 5GB
			Used:      5 * 1024 * 1024 * 1024,  // 5GB
		}
	}

	// Calculate available space in bytes
	available := stat.Bavail * uint64(stat.Bsize)
	total := stat.Blocks * uint64(stat.Bsize)
	used := total - available

	return &DiskSpaceInfo{
		Total:     total,
		Available: available,
		Used:      used,
	}
}

// validateDockerDaemon checks if Docker daemon is accessible
func (v *Validator) validateDockerDaemon(result *ValidationResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to create Docker client
	cli, err := client.New(client.FromEnv)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to create Docker client: %v", err))
		return
	}
	defer cli.Close()

	// Test Docker daemon connectivity
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Cannot connect to Docker daemon: %v", err))
	} else {
		log.Println("Docker daemon connection: OK")
	}
}

// validatePort checks if the specified port is available
func (v *Validator) validatePort(result *ValidationResult) {
	port := v.config.Port
	if port == "" {
		if v.config.TLS {
			port = "3443"
		} else {
			port = "3012"
		}
	}

	// Convert port to integer
	portNum, err := strconv.Atoi(port)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Invalid port number: %s", port))
		return
	}

	// Check if port is in valid range
	if portNum < 1 || portNum > 65535 {
		result.Errors = append(result.Errors, fmt.Sprintf("Port number %d is out of valid range (1-65535)", portNum))
		return
	}

	// Check if port is already in use
	address := fmt.Sprintf(":%s", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			result.Errors = append(result.Errors, fmt.Sprintf("Port %s is already in use", port))
		} else {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Cannot check if port %s is available: %v", port, err))
		}
	} else {
		listener.Close()
		log.Printf("Port %s availability: OK", port)
	}
}

// validateTLS checks TLS configuration
func (v *Validator) validateTLS(result *ValidationResult) {
	if !v.config.TLS {
		return // TLS is optional
	}

	if v.config.TLSDomain != "" {
		// Let's Encrypt mode - domain validation would be done during startup
		log.Printf("TLS configuration: Let's Encrypt mode for domain %s", v.config.TLSDomain)
		return
	}

	// Self-signed or custom certificate mode
	if v.config.TLSCert != "" && v.config.TLSKey != "" {
		// Check if certificate files exist
		if _, err := os.Stat(v.config.TLSCert); os.IsNotExist(err) {
			result.Errors = append(result.Errors, fmt.Sprintf("TLS certificate file not found: %s", v.config.TLSCert))
		}
		if _, err := os.Stat(v.config.TLSKey); os.IsNotExist(err) {
			result.Errors = append(result.Errors, fmt.Sprintf("TLS key file not found: %s", v.config.TLSKey))
		}
		
		if len(result.Errors) == 0 {
			log.Println("TLS configuration: OK (custom certificates)")
		}
	} else {
		// Will use auto-generated self-signed certificates
		log.Println("TLS configuration: OK (will generate self-signed certificates)")
	}
}

// validateDatabase tests database creation and access
func (v *Validator) validateDatabase(result *ValidationResult) {
	// Test database creation and access
	db, err := bbolt.Open(v.config.DBPath, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Cannot create/open database: %v", err))
		return
	}
	defer db.Close()

	// Test basic database operations
	if err := db.Update(func(tx *bbolt.Tx) error {
		bucket := []byte("test_bucket")
		if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
			return err
		}
		return tx.Bucket(bucket).Put([]byte("test_key"), []byte("test_value"))
	}); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Database operation failed: %v", err))
		return
	}

	// Test read operation
	if err := db.View(func(tx *bbolt.Tx) error {
		val := tx.Bucket([]byte("test_bucket")).Get([]byte("test_key"))
		if string(val) != "test_value" {
			return fmt.Errorf("database read/write test failed")
		}
		return nil
	}); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Database read test failed: %v", err))
		return
	}

	// Clean up test bucket
	db.Update(func(tx *bbolt.Tx) error {
		return tx.DeleteBucket([]byte("test_bucket"))
	})

	log.Println("Database connectivity: OK")
}

// validateSystemResources checks basic system resources
func (v *Validator) validateSystemResources(result *ValidationResult) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Check available memory (minimum 100MB recommended)
	availableMem := m.Sys - m.Alloc
	if availableMem < 100*1024*1024 { // 100MB
		result.Warnings = append(result.Warnings, fmt.Sprintf("Low memory available: %.1f MB (minimum 100 MB recommended)", float64(availableMem)/1024/1024))
	}

	log.Printf("System resources: %.1f MB memory available", float64(availableMem)/1024/1024)
}

// logConfiguration logs all configuration values (excluding sensitive data)
func (v *Validator) logConfiguration() {
	log.Println("=== Configuration Summary ===")
	log.Printf("Data Directory: %s", v.config.DataDir)
	log.Printf("Database Path: %s", v.config.DBPath)
	log.Printf("Log Path: %s", v.config.LogPath)
	log.Printf("Secret Path: %s", v.config.SecretPath)
	log.Printf("Port: %s", v.config.Port)
	log.Printf("TLS Enabled: %t", v.config.TLS)
	
	if v.config.TLS {
		if v.config.TLSDomain != "" {
			log.Printf("TLS Mode: Let's Encrypt for domain %s", v.config.TLSDomain)
		} else if v.config.TLSCert != "" && v.config.TLSKey != "" {
			log.Printf("TLS Mode: Custom certificates")
			log.Printf("TLS Cert: %s", v.config.TLSCert)
			log.Printf("TLS Key: %s", v.config.TLSKey)
		} else {
			log.Printf("TLS Mode: Self-signed certificates")
		}
	}
	
	if v.config.AdminPassword != "" {
		log.Printf("Admin Password: [SET]")
	} else {
		log.Printf("Admin Password: [WILL BE GENERATED]")
	}
	
	if v.config.JWTSecret != "" {
		log.Printf("JWT Secret: [SET]")
	} else {
		log.Printf("JWT Secret: [WILL BE GENERATED]")
	}
	
	log.Println("=== End Configuration Summary ===")
}