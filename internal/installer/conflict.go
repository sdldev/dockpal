// Package installer provides installation utilities including conflict detection
// for file operations during the dockpal installation process.
package installer

import (
	"bytes"
	"os"
	"path/filepath"
)

// DetectConflicts walks the source root directory and compares each entry
// with its counterpart in the destination root to detect conflicts.
// A conflict occurs when:
// - Type mismatch: source is a file but destination is a directory (or vice versa)
// - Content mismatch: both are regular files but their contents differ
//
// Returns:
//   - path: the relative path of the first conflict found
//   - reason: description of why it's a conflict
//   - conflict: true if a conflict was found, false otherwise
//   - err: any error encountered during the walk
func DetectConflicts(srcRoot, dstRoot string) (path, reason string, conflict bool, err error) {
	// Use WalkDir to walk through source directory
	err = filepath.WalkDir(srcRoot, func(srcPath string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Get relative path from srcRoot
		relPath, err := filepath.Rel(srcRoot, srcPath)
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if relPath == "." {
			return nil
		}

		// Build corresponding path in destination
		dstPath := filepath.Join(dstRoot, relPath)

		// Use Lstat to check the type without following symlinks
		srcInfo, err := os.Lstat(srcPath)
		if err != nil {
			// If source path cannot be stat'd, skip it
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		// Check if destination exists
		dstInfo, err := os.Lstat(dstPath)
		if err != nil {
			if os.IsNotExist(err) {
				// Path doesn't exist in destination - no conflict
				return nil
			}
			// Other error accessing destination
			return err
		}

		// Check for type mismatch (file vs directory)
		srcIsDir := srcInfo.IsDir()
		dstIsDir := dstInfo.IsDir()

		if srcIsDir != dstIsDir {
			if srcIsDir {
				return filepath.SkipDir
			}
			// Type mismatch: file vs directory
			path, reason, conflict = relPath, "type mismatch: source is directory, destination is file", true
			return filepath.SkipDir
		}

		// Both are directories - no conflict, continue walking
		if srcIsDir {
			return nil
		}

		// Both are files - check content
		srcMode := srcInfo.Mode()
		dstMode := dstInfo.Mode()

		// Only compare content for regular files
		if !srcMode.IsRegular() || !dstMode.IsRegular() {
			return nil
		}

		// Read file contents
		srcContent, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}

		dstContent, err := os.ReadFile(dstPath)
		if err != nil {
			if os.IsNotExist(err) {
				// Destination file doesn't exist - no conflict
				return nil
			}
			return err
		}

		// Compare contents using bytes.Equal
		if !bytes.Equal(srcContent, dstContent) {
			path, reason, conflict = relPath, "content mismatch", true
			return filepath.SkipDir
		}

		return nil
	})

	// Handle walk errors
	if err != nil && err != filepath.SkipDir {
		return "", "", false, err
	}

	// Clear error if we found a conflict (already returned via parameters)
	if conflict {
		err = nil
	}

	return path, reason, conflict, err
}