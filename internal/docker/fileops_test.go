package docker

import (
	"testing"
)

func TestValidatePath_ValidPaths(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/etc/config", "/etc/config"},
		{"/var/log/app.log", "/var/log/app.log"},
		{"/", "/"},
		{"/home/user/./file", "/home/user/file"},
		{"/a/b/c/../d", "/a/b/d"},
	}

	for _, tt := range tests {
		result, err := ValidatePath(tt.input)
		if err != nil {
			t.Errorf("ValidatePath(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if result != tt.expected {
			t.Errorf("ValidatePath(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestValidatePath_RejectsTraversal(t *testing.T) {
	// After filepath.Clean, "/../x" becomes "/x" (can't escape root on absolute paths).
	// The traversal check catches paths where ".." remains after cleaning.
	// For absolute paths, filepath.Clean absorbs ".." at root, so traversal
	// prevention for absolute paths is inherently handled by Clean + absolute check.
	// However, non-absolute paths with ".." are caught by the absolute check.

	// Verify that paths resolving safely after Clean are accepted
	result, err := ValidatePath("/../etc/passwd")
	if err != nil {
		t.Errorf("ValidatePath(\"/../etc/passwd\") error: %v (resolves to /etc/passwd)", err)
	}
	if result != "/etc/passwd" {
		t.Errorf("ValidatePath(\"/../etc/passwd\") = %q, want /etc/passwd", result)
	}

	// Non-absolute traversal paths are rejected by the absolute check
	_, err = ValidatePath("../etc/passwd")
	if err == nil {
		t.Error("ValidatePath(\"../etc/passwd\") expected error, got nil")
	}

	_, err = ValidatePath("../../etc/shadow")
	if err == nil {
		t.Error("ValidatePath(\"../../etc/shadow\") expected error, got nil")
	}
}

func TestValidatePath_RejectsNonAbsolute(t *testing.T) {
	tests := []string{
		"relative/path",
		"./relative",
		"../escape",
		"file.txt",
	}

	for _, path := range tests {
		_, err := ValidatePath(path)
		if err == nil {
			t.Errorf("ValidatePath(%q) expected error for non-absolute, got nil", path)
		}
	}
}

func TestValidatePath_RejectsNullBytes(t *testing.T) {
	paths := []string{
		"/etc/\x00passwd",
		"/var/log\x00.txt",
	}

	for _, path := range paths {
		_, err := ValidatePath(path)
		if err == nil {
			t.Errorf("ValidatePath(%q) expected error for null byte, got nil", path)
		}
	}
}

func TestValidatePath_RejectsControlChars(t *testing.T) {
	paths := []string{
		"/etc/\x01file",
		"/var/\x0Blog",
		"/home/\x1Fuser",
	}

	for _, path := range paths {
		_, err := ValidatePath(path)
		if err == nil {
			t.Errorf("ValidatePath(%q) expected error for control char, got nil", path)
		}
	}
}
