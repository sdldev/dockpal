package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// Status values for UpdateProgress
	StatusIdle        = "idle"
	StatusDownloading = "downloading"
	StatusVerifying   = "verifying"
	StatusInstalling  = "installing"
	StatusRestarting  = "restarting"
	StatusComplete    = "complete"
	StatusError       = "error"

	// Verification substeps for stageDetail (R12.4).
	StageDetailSize     = "size"
	StageDetailMode     = "mode"
	StageDetailChecksum = "checksum"
	StageDetailArch     = "arch"

	// Binary paths
	ProductionBinaryPath = "/usr/local/bin/dockpal"
	TempBinaryPath       = "/tmp/dockpal-new"
	BackupBinaryPath     = "/usr/local/bin/dockpal.bak"
	StagingBinarySuffix  = ".new"

	// Download timeout
	DownloadTimeout = 5 * time.Minute

	// Progress throttle
	ProgressThrottle   = 100 * time.Millisecond
	DownloadBufferSize = 64 * 1024

	// Binary size bounds
	MinBinarySize int64 = 1 << 20         // 1 MiB
	MaxBinarySize int64 = 200 * (1 << 20) // 200 MiB

	// ELF magic and e_machine values (R2.5).
	elfMagic uint32 = 0x464C457F // "\x7fELF" little-endian

	EM_386     uint16 = 3
	EM_ARM     uint16 = 40
	EM_X86_64  uint16 = 62
	EM_AARCH64 uint16 = 183
)

// UpdateState represents the single-flight state of the update service.
type UpdateState int32

const (
	UpdateIdle UpdateState = iota
	UpdateRunning
)

// UpdateProgress represents the progress of an update operation
type UpdateProgress struct {
	Status      string `json:"status"`                // R7, R12.2
	Message     string `json:"message"`             // R7.6
	Percentage  int    `json:"percentage"`          // R7.2..R7.5, clamped 0..100
	ErrorCode   string `json:"errorCode,omitempty"`   // R12.3, R12.5
	StageDetail string `json:"stageDetail,omitempty"` // R12.4, R12.5
}

// ProgressEmitter is the callback the orchestrator uses to publish events.
type ProgressEmitter func(UpdateProgress)

// UpdateService handles binary download, verification, and installation
type UpdateService struct {
	httpClient     *http.Client
	currentVersion string
	binPath        string
	tempPath       string
	backupPath     string
	maxBinarySize  int64
	minBinarySize  int64
	throttle       time.Duration

	// Backends for testability
	fs     fsBackend
	svc    serviceController
	sudo   sudoChecker
	dns    resolver

	// Single-flight (R10).
	mu           sync.Mutex
	state        UpdateState
	lastProgress *UpdateProgress
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
		backupPath:     BackupBinaryPath,
		maxBinarySize:  MaxBinarySize,
		minBinarySize:  MinBinarySize,
		throttle:       ProgressThrottle,
		fs:             &osFS{},
		svc:            &systemctlController{},
		sudo:           &sudoCheckerImpl{},
		dns:            &netResolver{},
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
		backupPath:     BackupBinaryPath,
		maxBinarySize:  MaxBinarySize,
		minBinarySize:  MinBinarySize,
		throttle:       ProgressThrottle,
		fs:             &osFS{},
		svc:            &systemctlController{},
		sudo:           &sudoCheckerImpl{},
		dns:            &netResolver{},
	}
}

// NewUpdateServiceWithBackends creates a new UpdateService with injected backends for tests.
func NewUpdateServiceWithBackends(currentVersion, binPath, tempPath, backupPath string, fs fsBackend, svc serviceController, sudo sudoChecker, dns resolver) *UpdateService {
	if binPath == "" {
		binPath = ProductionBinaryPath
	}
	if tempPath == "" {
		tempPath = TempBinaryPath
	}
	if backupPath == "" {
		backupPath = BackupBinaryPath
	}
	return &UpdateService{
		httpClient: &http.Client{
			Timeout: DownloadTimeout,
		},
		currentVersion: currentVersion,
		binPath:        binPath,
		tempPath:       tempPath,
		backupPath:     backupPath,
		maxBinarySize:  MaxBinarySize,
		minBinarySize:  MinBinarySize,
		throttle:       ProgressThrottle,
		fs:             fs,
		svc:            svc,
		sudo:           sudo,
		dns:            dns,
	}
}

// attemptID generates a short unique identifier for a run.
func attemptID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// RunUpdate orchestrates the full update pipeline.
func (s *UpdateService) RunUpdate(ctx context.Context, downloadURL string, emit ProgressEmitter) error {
	id := attemptID()

	// Acquire single-flight lock (R10.1).
	s.mu.Lock()
	if s.state == UpdateRunning {
		s.mu.Unlock()
		emit(UpdateProgress{
			Status:     StatusError,
			Message:    "An update is already running. Please wait for it to finish.",
			Percentage: 0,
			ErrorCode:  ErrUpdateAlreadyRunning,
		})
		return fmt.Errorf("%s", ErrUpdateAlreadyRunning)
	}
	s.state = UpdateRunning
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.state = UpdateIdle
		s.mu.Unlock()
	}()

	// Structured logging at start (R11.1).
	log.Printf("update attempt_id=%s current_version=%s target_version=unknown download_url=%s", id, s.currentVersion, downloadURL)

	// Helper to emit and record progress.
	emitAndRecord := func(p UpdateProgress) {
		s.mu.Lock()
		s.lastProgress = &p
		s.mu.Unlock()
		emit(p)
		// R11.2: log each emit.
		if p.ErrorCode != "" {
			log.Printf("update attempt_id=%s stage=%s percentage=%d error_code=%s", id, p.Status, p.Percentage, p.ErrorCode)
		} else {
			log.Printf("update attempt_id=%s stage=%s percentage=%d", id, p.Status, p.Percentage)
		}
	}

	// Cleanup stale artifacts before starting (R9.5).
	s.cleanupStaleArtifacts()

	// Check sudo before downloading (R4.1).
	hasSudo, err := s.sudo.Check()
	if err != nil {
		log.Printf("update attempt_id=%s sudo check error: %v", id, err)
	}
	if !hasSudo {
		emitAndRecord(UpdateProgress{
			Status:     StatusError,
			Message:    "Update requires passwordless sudo. Configure sudoers and retry.",
			Percentage: 0,
			ErrorCode:  ErrSudoUnavailable,
		})
		s.cleanup(false)
		return fmt.Errorf("%s", ErrSudoUnavailable)
	}

	// Validate URL.
	if err := s.ValidateDownloadURL(downloadURL); err != nil {
		s.cleanup(false)
		return err
	}

	// Download.
	if err := s.downloadToTemp(ctx, downloadURL, emitAndRecord); err != nil {
		emitAndRecord(UpdateProgress{
			Status:     StatusError,
			Message:    err.Error(),
			Percentage: 0,
			ErrorCode:  s.mapDownloadError(err),
		})
		s.cleanup(false)
		return err
	}

	// Determine expected asset name for checksum lookup.
	expectedAssetName := assetNameForPlatform()

	// Verify.
	if err := s.verifyTempBinary(ctx, expectedAssetName, "", emitAndRecord); err != nil {
		s.cleanup(false)
		return err
	}

	// Install.
	if err := s.installAtomic(ctx, emitAndRecord); err != nil {
		if strings.Contains(err.Error(), ErrInstallStopFailed) {
			// Per cleanup table: keep temp, delete backup.
			_ = s.fs.Remove(s.backupPath)
		} else {
			s.cleanup(false)
		}
		return err
	}

	// Restart.
	if err := s.restartService(ctx, emitAndRecord); err != nil {
		s.cleanup(false)
		return err
	}

	// Success.
	s.cleanup(true)
	emitAndRecord(UpdateProgress{
		Status:     StatusComplete,
		Message:    "Update completed successfully",
		Percentage: 100,
	})
	return nil
}

// Status returns the current update progress.
func (s *UpdateService) Status() UpdateProgress {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == UpdateIdle {
		return UpdateProgress{Status: StatusIdle, Message: "No update in progress", Percentage: 0}
	}
	if s.lastProgress != nil {
		return *s.lastProgress
	}
	return UpdateProgress{Status: StatusIdle, Message: "No update in progress", Percentage: 0}
}

// cleanupStaleArtifacts deletes any pre-existing tempPath and backupPath before starting (R9.5).
func (s *UpdateService) cleanupStaleArtifacts() {
	_ = s.fs.Remove(s.tempPath)
	_ = s.fs.Remove(s.backupPath)
}

// cleanup removes files according to success/failure invariants.
func (s *UpdateService) cleanup(success bool) {
	if success {
		_ = s.fs.Remove(s.tempPath)
		_ = s.fs.Remove(s.backupPath)
		return
	}
	// On failure: delete both temp and backup (per cleanup table for most failures).
	_ = s.fs.Remove(s.tempPath)
	_ = s.fs.Remove(s.backupPath)
}

// mapDownloadError maps an error to an error code string.
func (s *UpdateService) mapDownloadError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, ErrDownloadHTTPStatus):
		return ErrDownloadHTTPStatus
	case strings.Contains(msg, ErrDownloadTimeout):
		return ErrDownloadTimeout
	case strings.Contains(msg, ErrDownloadDiskFull):
		return ErrDownloadDiskFull
	default:
		return ErrDownloadIOFailed
	}
}

// downloadToTemp streams the response body to tempPath.
func (s *UpdateService) downloadToTemp(ctx context.Context, rawURL string, emit ProgressEmitter) error {
	if rawURL == "" {
		return fmt.Errorf("download URL cannot be empty")
	}

	emit(UpdateProgress{Status: StatusDownloading, Message: "Starting download...", Percentage: 0})

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "dockpal")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", ErrDownloadIOFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: download returned status %d", ErrDownloadHTTPStatus, resp.StatusCode)
	}

	// Create temp directory if needed.
	tempDir := filepath.Dir(s.tempPath)
	if err := s.fs.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("%s: failed to create temp directory: %w", ErrDownloadIOFailed, err)
	}

	outFile, err := s.fs.Create(s.tempPath)
	if err != nil {
		return fmt.Errorf("%s: failed to create temp file: %w", ErrDownloadIOFailed, err)
	}
	// Ensure partial file is removed on any failure path.
	removeOnFail := true
	defer func() {
		outFile.Close()
		if removeOnFail {
			_ = s.fs.Remove(s.tempPath)
		}
	}()

	contentLength := resp.ContentLength
	var written int64
	buf := make([]byte, DownloadBufferSize)
	lastEmit := time.Now().Add(-s.throttle) // allow first emit immediately

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, writeErr := outFile.Write(buf[:n])
			if writeErr != nil {
				if isDiskFull(writeErr) {
					return fmt.Errorf("%s: disk full while writing temp file: %w", ErrDownloadDiskFull, writeErr)
				}
				return fmt.Errorf("%s: failed to write to file: %w", ErrDownloadIOFailed, writeErr)
			}
			written += int64(n)

			if time.Since(lastEmit) >= s.throttle {
				pct := computeDownloadPercentage(written, contentLength)
				emit(UpdateProgress{Status: StatusDownloading, Message: fmt.Sprintf("Downloading... (%d bytes)", written), Percentage: pct})
				lastEmit = time.Now()
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("%s: download timed out", ErrDownloadTimeout)
			}
			return fmt.Errorf("%s: error reading response: %w", ErrDownloadIOFailed, err)
		}
	}

	// Sync and close before any verification reads the file (R1.2).
	if err := outFile.Sync(); err != nil {
		return fmt.Errorf("%s: failed to sync temp file: %w", ErrDownloadIOFailed, err)
	}
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("%s: failed to close temp file: %w", ErrDownloadIOFailed, err)
	}

	// Set executable permissions before verification reads mode (R1.1).
	if err := s.fs.Chmod(s.tempPath, 0755); err != nil {
		return fmt.Errorf("%s: failed to chmod temp file: %w", ErrTempChmodFailed, err)
	}

	removeOnFail = false
	return nil
}

func computeDownloadPercentage(written, contentLength int64) int {
	if contentLength > 0 {
		if written >= contentLength {
			return 100
		}
		pct := int(float64(written) / float64(contentLength) * 100)
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		return pct
	}
	// No Content-Length: stepped indicator, never reach 100 while downloading.
	if written == 0 {
		return 0
	}
	pct := int(written % 99)
	if pct == 0 {
		pct = 1
	}
	return pct
}

func isDiskFull(err error) bool {
	if err == nil {
		return false
	}
	if errorsIs(err, syscall.ENOSPC) {
		return true
	}
	return false
}

func errorsIs(err, target error) bool {
	return err == target || (err != nil && target != nil && err.Error() == target.Error())
}

// verifyTempBinary executes 4 substeps in order.
func (s *UpdateService) verifyTempBinary(ctx context.Context, expectedAssetName string, checksumURL string, emit ProgressEmitter) error {
	info, err := s.fs.Stat(s.tempPath)
	if err != nil {
		return fmt.Errorf("%s: failed to stat temp binary: %w", ErrVerifySizeOOR, err)
	}

	// 1. Size check (R2.1, R2.6).
	emit(UpdateProgress{Status: StatusVerifying, Message: "Checking binary size...", Percentage: 10, StageDetail: StageDetailSize})
	size := info.Size()
	if size < s.minBinarySize || size > s.maxBinarySize {
		emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Downloaded binary size %d is outside allowed range [%d, %d]", size, s.minBinarySize, s.maxBinarySize), Percentage: 0, ErrorCode: ErrVerifySizeOOR})
		return fmt.Errorf("%s: size %d out of range [%d, %d]", ErrVerifySizeOOR, size, s.minBinarySize, s.maxBinarySize)
	}

	// 2. Mode check (R1.3, R2.2).
	emit(UpdateProgress{Status: StatusVerifying, Message: "Checking executable permissions...", Percentage: 30, StageDetail: StageDetailMode})
	mode := info.Mode()
	if mode&0111 == 0 {
		emit(UpdateProgress{Status: StatusError, Message: "Downloaded binary does not have executable permissions", Percentage: 0, ErrorCode: ErrTempChmodFailed})
		return fmt.Errorf("%s: binary mode %o has no executable bit", ErrTempChmodFailed, mode)
	}

	// 3. Checksum (optional, R2.3, R2.4, R2.7).
	emit(UpdateProgress{Status: StatusVerifying, Message: "Verifying checksum...", Percentage: 50, StageDetail: StageDetailChecksum})
	if checksumURL != "" {
		expectedHex, err := s.fetchAndParseChecksum(ctx, checksumURL, expectedAssetName)
		if err != nil {
			emit(UpdateProgress{Status: StatusError, Message: err.Error(), Percentage: 0, ErrorCode: ErrVerifyChecksum})
			return err
		}
		actualHex, err := s.sha256OfFile(s.tempPath)
		if err != nil {
			emit(UpdateProgress{Status: StatusError, Message: err.Error(), Percentage: 0, ErrorCode: ErrVerifyChecksum})
			return err
		}
		log.Printf("update checksum expected_sha256=%s actual_sha256=%s", expectedHex, actualHex)
		if !strings.EqualFold(expectedHex, actualHex) {
			emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Checksum mismatch: expected %s, got %s", expectedHex, actualHex), Percentage: 0, ErrorCode: ErrVerifyChecksum})
			return fmt.Errorf("%s: expected %s, got %s", ErrVerifyChecksum, expectedHex, actualHex)
		}
	} else {
		log.Printf("update no checksum published for asset=%s", expectedAssetName)
		emit(UpdateProgress{Status: StatusVerifying, Message: "No checksum published, skipping checksum verification", Percentage: 50, StageDetail: StageDetailChecksum})
	}

	// 4. ELF arch (R2.5, R2.8, R2.9).
	emit(UpdateProgress{Status: StatusVerifying, Message: "Verifying ELF architecture...", Percentage: 80, StageDetail: StageDetailArch})
	if err := verifyELFArch(s.tempPath); err != nil {
		code := ErrVerifyNotELF
		if strings.Contains(err.Error(), ErrVerifyArchMismatch) {
			code = ErrVerifyArchMismatch
		}
		emit(UpdateProgress{Status: StatusError, Message: err.Error(), Percentage: 0, ErrorCode: code})
		return err
	}

	return nil
}

func (s *UpdateService) fetchAndParseChecksum(ctx context.Context, checksumURL, expectedAssetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", checksumURL, nil)
	if err != nil {
		return "", fmt.Errorf("%s: failed to create checksum request: %w", ErrVerifyChecksum, err)
	}
	req.Header.Set("User-Agent", "dockpal")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: failed to fetch checksum file: %w", ErrVerifyChecksum, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: checksum download returned status %d", ErrVerifyChecksum, resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%s: failed to read checksum file: %w", ErrVerifyChecksum, err)
	}

	digest, err := parseChecksumFile(content, expectedAssetName)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ErrVerifyChecksum, err)
	}
	return digest, nil
}

func (s *UpdateService) sha256OfFile(path string) (string, error) {
	f, err := s.fs.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(f)
	return hex.EncodeToString(sum[:]), nil
}

// installAtomic performs atomic install with rollback support.
func (s *UpdateService) installAtomic(ctx context.Context, emit ProgressEmitter) error {
	emit(UpdateProgress{Status: StatusInstalling, Message: "Preparing installation...", Percentage: 60})

	// Re-check sudo (R4.3).
	hasSudo, err := s.sudo.Check()
	if err != nil {
		log.Printf("update sudo re-check error: %v", err)
	}
	if !hasSudo {
		emit(UpdateProgress{Status: StatusError, Message: "Sudo access was revoked during the update.", Percentage: 0, ErrorCode: ErrSudoLost})
		return fmt.Errorf("%s", ErrSudoLost)
	}

	// Backup production binary (R3.1).
	emit(UpdateProgress{Status: StatusInstalling, Message: "Backing up current binary...", Percentage: 65})
	if err := s.copyFile(s.binPath, s.backupPath); err != nil {
		emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Failed to backup binary: %v", err), Percentage: 0, ErrorCode: ErrInstallReplaceFailed})
		return fmt.Errorf("%s: backup failed: %w", ErrInstallReplaceFailed, err)
	}
	// Preserve ownership from backup.
	backupInfo, _ := s.fs.Stat(s.backupPath)

	// Stop service (R3.4).
	emit(UpdateProgress{Status: StatusInstalling, Message: "Stopping dockpal service...", Percentage: 70})
	if err := s.svc.Stop(ctx); err != nil {
		emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Failed to stop dockpal service: %v", err), Percentage: 0, ErrorCode: ErrInstallStopFailed})
		// R3.4: leave temp, prod, backup unchanged; no rollback.
		return fmt.Errorf("%s: %w", ErrInstallStopFailed, err)
	}

	// Atomic replace: write sibling then rename (R3.2).
	emit(UpdateProgress{Status: StatusInstalling, Message: "Replacing binary...", Percentage: 75})
	stagingPath := s.binPath + StagingBinarySuffix
	if err := s.copyFile(s.tempPath, stagingPath); err != nil {
		s.rollback("copy staging failed")
		_ = s.svc.Start(ctx)
		emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Failed to copy new binary: %v", err), Percentage: 0, ErrorCode: ErrInstallReplaceFailed})
		return fmt.Errorf("%s: %w", ErrInstallReplaceFailed, err)
	}
	if err := s.fs.Rename(stagingPath, s.binPath); err != nil {
		_ = s.fs.Remove(stagingPath)
		s.rollback("rename failed")
		_ = s.svc.Start(ctx)
		emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Failed to replace binary: %v", err), Percentage: 0, ErrorCode: ErrInstallReplaceFailed})
		return fmt.Errorf("%s: %w", ErrInstallReplaceFailed, err)
	}

	// Chmod and chown (R3.3).
	emit(UpdateProgress{Status: StatusInstalling, Message: "Setting permissions...", Percentage: 80})
	if err := s.fs.Chmod(s.binPath, 0755); err != nil {
		s.rollback("chmod failed")
		_ = s.svc.Start(ctx)
		emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Failed to set permissions: %v", err), Percentage: 0, ErrorCode: ErrInstallChownFailed})
		return fmt.Errorf("%s: %w", ErrInstallChownFailed, err)
	}
	if backupInfo != nil {
		uid, gid := getFileOwner(backupInfo)
		if err := s.fs.Chown(s.binPath, uid, gid); err != nil {
			s.rollback("chown failed")
			_ = s.svc.Start(ctx)
			emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Failed to set ownership: %v", err), Percentage: 0, ErrorCode: ErrInstallChownFailed})
			return fmt.Errorf("%s: %w", ErrInstallChownFailed, err)
		}
	}

	return nil
}

// restartService starts the dockpal service.
func (s *UpdateService) restartService(ctx context.Context, emit ProgressEmitter) error {
	emit(UpdateProgress{Status: StatusRestarting, Message: "Starting dockpal service...", Percentage: 90})
	if err := s.svc.Start(ctx); err != nil {
		// R3.6: rollback and retry start.
		outcome := s.rollback("start failed")
		retryErr := s.svc.Start(ctx)
		log.Printf("update rollback_outcome=%s retry_start_err=%v", outcome, retryErr)
		emit(UpdateProgress{Status: StatusError, Message: fmt.Sprintf("Failed to start dockpal service: %v", err), Percentage: 0, ErrorCode: ErrInstallStartFailed})
		return fmt.Errorf("%s: %w", ErrInstallStartFailed, err)
	}
	return nil
}

// rollback restores the backup binary to the production path.
func (s *UpdateService) rollback(reason string) string {
	log.Printf("update rollback reason=%s", reason)
	if err := s.fs.Rename(s.backupPath, s.binPath); err != nil {
		log.Printf("update rollback failed: %v", err)
		// Try to start anyway with whatever is at binPath.
		return "restored_but_start_failed"
	}
	return "restored_and_started"
}

// copyFile copies src to dst using the fs backend.
func (s *UpdateService) copyFile(src, dst string) error {
	data, err := s.fs.ReadFile(src)
	if err != nil {
		return err
	}
	if err := s.fs.WriteFile(dst, data, 0644); err != nil {
		return err
	}
	return nil
}

// CheckSudoAccess checks if the current user has sudo privileges.
func (s *UpdateService) CheckSudoAccess() (bool, error) {
	return s.sudo.Check()
}

// ValidateDownloadURL ensures the download URL is safe (package-level convenience).
func ValidateDownloadURL(rawURL string) error {
	return ValidateDownloadURLWithResolver(rawURL, &netResolver{})
}

// ValidateDownloadURL ensures the download URL is safe.
func (s *UpdateService) ValidateDownloadURL(rawURL string) error {
	return ValidateDownloadURLWithResolver(rawURL, s.dns)
}

// ValidateDownloadURLWithResolver validates a URL using an external resolver for testability.
func ValidateDownloadURLWithResolver(rawURL string, dns resolver) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s: invalid URL format", ErrURLSchemeNotHTTPS)
	}

	if u.Scheme != "https" {
		return fmt.Errorf("%s: only https URLs are allowed", ErrURLSchemeNotHTTPS)
	}

	host := strings.ToLower(u.Hostname())

	if u.User != nil {
		return fmt.Errorf("%s: URL must not contain credentials", ErrURLCredentialsPresent)
	}

	if host != "github.com" && !strings.HasSuffix(host, ".github.com") &&
		host != "objects.githubusercontent.com" &&
		!strings.HasSuffix(host, ".githubusercontent.com") {
		return fmt.Errorf("%s: downloads are only allowed from github.com", ErrURLHostNotAllowed)
	}

	// Resolve DNS and reject private/internal IPs
	ips, err := dns.LookupIP(host)
	if err != nil {
		return fmt.Errorf("%s: failed to resolve host: %w", ErrURLResolvesPrivateIP, err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("%s: download host resolves to private/internal IP", ErrURLResolvesPrivateIP)
		}
	}

	return nil
}

// assetNameForPlatform returns the expected release asset name for the current platform.
func assetNameForPlatform() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "arm64"
	case "arm":
		arch = "armv7"
	}
	return fmt.Sprintf("dockpal-%s-%s", runtime.GOOS, arch)
}

type ownerInfo interface {
	Uid() uint32
	Gid() uint32
}

func getFileOwner(info os.FileInfo) (uid, gid int) {
	uid = os.Getuid()
	gid = os.Getgid()
	if o, ok := info.Sys().(ownerInfo); ok {
		uid = int(o.Uid())
		gid = int(o.Gid())
	}
	return
}
