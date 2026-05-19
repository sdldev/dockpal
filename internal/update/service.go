package update

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Status values for UpdateProgress
	StatusDownloading = "downloading"
	StatusInstalling  = "installing"
	StatusRestarting  = "restarting"
	StatusComplete    = "complete"
	StatusError       = "error"

	// Binary paths
	ProductionBinaryPath = "/usr/local/bin/dockpal"
	TempBinaryPath       = "/tmp/dockpal-new"

	// Download timeout
	DownloadTimeout = 5 * time.Minute

	// Minimum binary size (1MB)
	MinBinarySize = 1024 * 1024
)

// UpdateProgress represents the progress of an update operation
type UpdateProgress struct {
	Status    string `json:"status"`    // "downloading", "installing", "restarting", "complete", "error"
	Message   string `json:"message"`   // Descriptive message about current status
	Percentage int   `json:"percentage"` // Progress percentage (0-100)
}

// UpdateService handles binary download, verification, and installation
type UpdateService struct {
	httpClient    *http.Client
	currentVersion string
	binPath        string
	tempPath       string
}

// NewUpdateService creates a new UpdateService
func NewUpdateService(currentVersion string) *UpdateService {
	return &UpdateService{
		httpClient: &http.Client{
			Timeout: DownloadTimeout,
		},
		currentVersion: currentVersion,
		binPath:        ProductionBinaryPath,
		tempPath:       TempBinaryPath,
	}
}

// NewUpdateServiceWithPaths creates a new UpdateService with custom paths
func NewUpdateServiceWithPaths(currentVersion, binPath, tempPath string) *UpdateService {
	if binPath == "" {
		binPath = ProductionBinaryPath
	}
	if tempPath == "" {
		tempPath = TempBinaryPath
	}
	return &UpdateService{
		httpClient: &http.Client{
			Timeout: DownloadTimeout,
		},
		currentVersion: currentVersion,
		binPath:        binPath,
		tempPath:       tempPath,
	}
}

// DownloadUpdate downloads the update binary from the given URL
// Returns the path to the downloaded file
func (s *UpdateService) DownloadUpdate(ctx context.Context, rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("download URL cannot be empty")
	}

	// Validate URL to prevent SSRF
	if err := ValidateDownloadURL(rawURL); err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "dockpal")

	// Download the file
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Create temp directory if needed
	tempDir := filepath.Dir(s.tempPath)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Create the file
	outFile, err := os.Create(s.tempPath)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer outFile.Close()

	// Get content length for progress tracking
	contentLength := resp.ContentLength

	// Copy with progress tracking
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, writeErr := outFile.Write(buf[:n])
			if writeErr != nil {
				return "", fmt.Errorf("failed to write to file: %w", writeErr)
			}
			written += int64(n)

			// Update progress if we know content length
			if contentLength > 0 {
				percentage := int((written * 100) / contentLength)
				if percentage > 100 {
					percentage = 100
				}
				// Progress is tracked via callback in real implementation
				_ = percentage
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("error reading response: %w", err)
		}
	}

	return s.tempPath, nil
}

// VerifyBinary checks if the downloaded binary is valid
// Returns nil if the binary is valid, error otherwise
func (s *UpdateService) VerifyBinary(path string) error {
	if path == "" {
		return fmt.Errorf("binary path cannot be empty")
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat binary: %w", err)
	}

	// Check file size (must be > 1MB)
	if info.Size() < MinBinarySize {
		return fmt.Errorf("binary size %d bytes is below minimum %d bytes", info.Size(), MinBinarySize)
	}

	// Check if executable bit is set
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("binary does not have executable permissions")
	}

	return nil
}

// InstallBinary replaces the current binary with the new one
// This method requires sudo privileges
func (s *UpdateService) InstallBinary(ctx context.Context, binaryPath string) error {
	if binaryPath == "" {
		return fmt.Errorf("binary path cannot be empty")
	}

	// First verify the binary
	if err := s.VerifyBinary(binaryPath); err != nil {
		return fmt.Errorf("binary verification failed: %w", err)
	}

	// Check sudo access first
	hasSudo, err := s.CheckSudoAccess()
	if err != nil {
		return fmt.Errorf("failed to check sudo access: %w", err)
	}
	if !hasSudo {
		return fmt.Errorf("sudo access required to install binary")
	}

	// Stop the dockpal service
	stopCmd := exec.Command("sudo", "systemctl", "stop", "dockpal")
	if output, err := stopCmd.CombinedOutput(); err != nil {
		// Try to clean up temp file on failure
		os.Remove(binaryPath)
		return fmt.Errorf("failed to stop dockpal service: %w, output: %s", err, string(output))
	}

	// Copy new binary to production path
	// Using sudo cp to handle the permission
	installCmd := exec.Command("sudo", "cp", binaryPath, s.binPath)
	if output, err := installCmd.CombinedOutput(); err != nil {
		// Try to restart service on failure
		exec.Command("sudo", "systemctl", "start", "dockpal")
		return fmt.Errorf("failed to install binary: %w, output: %s", err, string(output))
	}

	// Set executable permissions (0755)
	chmodCmd := exec.Command("sudo", "chmod", "0755", s.binPath)
	if output, err := chmodCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w, output: %s", err, string(output))
	}

	// Start the dockpal service
	startCmd := exec.Command("sudo", "systemctl", "start", "dockpal")
	if output, err := startCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start dockpal service: %w, output: %s", err, string(output))
	}

	// Clean up temp file
	os.Remove(binaryPath)

	return nil
}

// CheckSudoAccess checks if the current user has sudo privileges
// Returns true if the user can run sudo without password
func (s *UpdateService) CheckSudoAccess() (bool, error) {
	// Use sudo -n true to check passwordless sudo
	// -n flag makes sudo non-interactive (fails if password required)
	cmd := exec.Command("sudo", "-n", "true")
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

// GetProgressStatus returns the UpdateProgress for display
func (s *UpdateService) GetProgressStatus(status, message string, percentage int) UpdateProgress {
	return UpdateProgress{
		Status:    status,
		Message:   message,
		Percentage: percentage,
	}
}

// ValidateDownloadURL ensures the download URL is safe:
// - Must use https:// scheme
// - Host must be github.com or a github.com subdomain
// - No credentials in URL
// - Resolves DNS and rejects private/internal IPs
func ValidateDownloadURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format")
	}

	if u.Scheme != "https" {
		return fmt.Errorf("only https URLs are allowed")
	}

	host := strings.ToLower(u.Hostname())
	if host != "github.com" && !strings.HasSuffix(host, ".github.com") &&
		host != "githubusercontent.com" && !strings.HasSuffix(host, ".githubusercontent.com") {
		return fmt.Errorf("downloads are only allowed from github.com")
	}

	if u.User != nil {
		return fmt.Errorf("URL must not contain credentials")
	}

	// Resolve DNS and reject private/internal IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve host: %w", err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("download host resolves to private/internal IP")
		}
	}

	return nil
}