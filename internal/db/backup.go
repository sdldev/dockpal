package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// BackupTo creates a consistent snapshot of the database at the given path.
// It writes to a temporary file and renames atomically to avoid partial backups.
// A sidecar .sha256 file containing the checksum is also created.
func (d *DB) BackupTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}

	err = d.db.View(func(tx *bbolt.Tx) error {
		_, werr := tx.WriteTo(f)
		return werr
	})
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write backup: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close backup file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to set backup permissions: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to finalize backup: %w", err)
	}

	checksum, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}
	checksumPath := path + ".sha256"
	if err := os.WriteFile(checksumPath, []byte(checksum+"\n"), 0600); err != nil {
		return fmt.Errorf("failed to write checksum: %w", err)
	}

	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ValidateBackup checks whether the file at path is a valid BBolt database.
// If a .sha256 sidecar file exists, it also verifies the checksum.
func ValidateBackup(path string) error {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 1 * time.Second, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("invalid backup file: %w", err)
	}
	_ = db.Close()

	checksumPath := path + ".sha256"
	if data, err := os.ReadFile(checksumPath); err == nil {
		expected := strings.TrimSpace(string(data))
		actual, err := fileSHA256(path)
		if err != nil {
			return fmt.Errorf("failed to compute checksum: %w", err)
		}
		if actual != expected {
			return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
		}
	}

	return nil
}
