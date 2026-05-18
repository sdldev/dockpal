package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// DefaultMaxSize is the default maximum size of a log file before rotation (2MB).
	DefaultMaxSize = 2 * 1024 * 1024
	// DefaultMaxFiles is the default maximum number of rotated log files to retain.
	DefaultMaxFiles = 5
)

// LogRotator implements io.Writer with automatic log file rotation.
// It rotates the active log file when size reaches MaxSize and retains
// at most MaxFiles rotated files.
type LogRotator struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	maxSize  int64
	maxFiles int
	size     int64
}

// NewLogRotator creates a new LogRotator for the given file path.
// It creates the directory structure if needed and opens the log file for appending.
func NewLogRotator(path string, maxSize int64, maxFiles int) (*LogRotator, error) {
	if maxSize <= 0 {
		maxSize = int64(DefaultMaxSize)
	}
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}

	lr := &LogRotator{
		path:     path,
		maxSize:  maxSize,
		maxFiles: maxFiles,
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to stat log file: %w", err)
	}

	lr.file = f
	lr.size = info.Size()
	return lr, nil
}

// Write implements io.Writer. It checks if writing p would exceed maxSize
// and triggers rotation if needed before writing.
func (lr *LogRotator) Write(p []byte) (int, error) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	if lr.size+int64(len(p)) > lr.maxSize {
		if err := lr.rotate(); err != nil {
			return 0, fmt.Errorf("log rotation failed: %w", err)
		}
	}

	n, err := lr.file.Write(p)
	lr.size += int64(n)
	return n, err
}

// rotate closes the current file, shifts existing rotated files by incrementing
// their numeric suffix, and creates a new empty active log file.
// Files are named: path.1, path.2, ..., path.N where path.1 is the most recent.
func (lr *LogRotator) rotate() error {
	if err := lr.file.Close(); err != nil {
		return fmt.Errorf("failed to close current log file: %w", err)
	}

	// Delete the oldest file if it would exceed maxFiles after shift
	oldest := fmt.Sprintf("%s.%d", lr.path, lr.maxFiles)
	os.Remove(oldest)

	// Shift existing rotated files: .4 -> .5, .3 -> .4, .2 -> .3, .1 -> .2
	for i := lr.maxFiles - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", lr.path, i)
		dst := fmt.Sprintf("%s.%d", lr.path, i+1)
		os.Rename(src, dst)
	}

	// Rename current log file to .1
	os.Rename(lr.path, fmt.Sprintf("%s.1", lr.path))

	// Create new empty log file
	f, err := os.OpenFile(lr.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create new log file: %w", err)
	}

	lr.file = f
	lr.size = 0
	return nil
}

// Close closes the underlying log file.
func (lr *LogRotator) Close() error {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if lr.file != nil {
		return lr.file.Close()
	}
	return nil
}
