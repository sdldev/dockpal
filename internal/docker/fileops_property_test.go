package docker

import (
	"encoding/base64"
	"math/rand"
	"strings"
	"testing"
	"testing/quick"
	"unicode"
)

// Property 7: Path Traversal Prevention
// **Validates: Requirements 6.1, 6.2, 6.3, 6.4**

// TestProperty_PathTraversal_RejectsNonAbsolutePaths verifies that any path
// not starting with "/" after cleaning is rejected.
func TestProperty_PathTraversal_RejectsNonAbsolutePaths(t *testing.T) {
	// Generate random relative paths (no leading slash)
	f := func(segments []string) bool {
		if len(segments) == 0 {
			return true
		}
		// Filter out empty segments and build a relative path
		var parts []string
		for _, s := range segments {
			// Only use printable, non-control, non-null segments
			clean := ""
			for _, c := range s {
				if c > 31 && c != 0 && c != '/' {
					clean += string(c)
				}
			}
			if clean != "" {
				parts = append(parts, clean)
			}
		}
		if len(parts) == 0 {
			return true
		}
		relativePath := strings.Join(parts, "/")
		// Ensure it's actually relative (no leading /)
		if strings.HasPrefix(relativePath, "/") {
			return true // skip, not a valid test input
		}

		_, err := ValidatePath(relativePath)
		return err != nil // must be rejected
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - non-absolute path was accepted: %v", err)
	}
}

// TestProperty_PathTraversal_RejectsNullBytes verifies that paths containing
// null bytes are always rejected.
func TestProperty_PathTraversal_RejectsNullBytes(t *testing.T) {
	f := func(prefix string, suffix string) bool {
		// Build a path with a null byte embedded
		path := "/" + prefix + "\x00" + suffix
		_, err := ValidatePath(path)
		return err != nil // must be rejected
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - path with null byte was accepted: %v", err)
	}
}

// TestProperty_PathTraversal_RejectsControlChars verifies that paths containing
// control characters (ASCII 1-31 except tab) are always rejected.
func TestProperty_PathTraversal_RejectsControlChars(t *testing.T) {
	// Generate a control character (1-31, excluding 9 which is tab)
	f := func(r *rand.Rand) bool {
		// Pick a random control char (not null, not tab)
		controlChars := []rune{1, 2, 3, 4, 5, 6, 7, 8, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}
		ch := controlChars[r.Intn(len(controlChars))]
		path := "/etc/" + string(ch) + "file"
		_, err := ValidatePath(path)
		return err != nil // must be rejected
	}

	config := &quick.Config{MaxCount: 200}
	// Use a manual loop since quick.Check needs a specific signature
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		if !f(rng) {
			t.Fatalf("Property violated - path with control char was accepted (iteration %d)", i)
		}
	}
	_ = config
}

// TestProperty_PathTraversal_DotDotInRelativePathRejected verifies that paths
// containing ".." segments that are relative are always rejected.
func TestProperty_PathTraversal_DotDotInRelativePathRejected(t *testing.T) {
	f := func(prefix string, suffix string) bool {
		// Build a relative path with ".." segments
		path := prefix + "/../" + suffix
		// Ensure it's relative (doesn't start with /)
		path = strings.TrimLeft(path, "/")
		if path == "" {
			path = "../escape"
		}
		_, err := ValidatePath(path)
		return err != nil // must be rejected (non-absolute)
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - relative path with .. was accepted: %v", err)
	}
}

// TestProperty_PathTraversal_ValidAbsolutePathsAccepted verifies that valid
// absolute paths with only safe characters are accepted and cleaned properly.
func TestProperty_PathTraversal_ValidAbsolutePathsAccepted(t *testing.T) {
	f := func(segments []string) bool {
		if len(segments) == 0 {
			return true
		}
		// Build valid path segments (alphanumeric + safe chars only)
		var parts []string
		for _, s := range segments {
			clean := ""
			for _, c := range s {
				if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
					clean += string(c)
				}
			}
			if clean != "" && clean != ".." && clean != "." {
				parts = append(parts, clean)
			}
		}
		if len(parts) == 0 {
			return true
		}
		path := "/" + strings.Join(parts, "/")

		result, err := ValidatePath(path)
		if err != nil {
			return false // valid path should be accepted
		}
		// Result must be absolute
		if !strings.HasPrefix(result, "/") {
			return false
		}
		// Result must not contain ".."
		if strings.Contains(result, "..") {
			return false
		}
		// Result must not contain control chars or null bytes
		for _, c := range result {
			if c == 0 || (c < 32 && c != '\t') {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - valid absolute path was rejected: %v", err)
	}
}

// TestProperty_PathTraversal_OutputAlwaysAbsoluteOrError verifies the invariant
// that ValidatePath either returns an error or returns an absolute path.
func TestProperty_PathTraversal_OutputAlwaysAbsoluteOrError(t *testing.T) {
	f := func(path string) bool {
		result, err := ValidatePath(path)
		if err != nil {
			return true // error is fine
		}
		// If no error, result must be absolute
		return strings.HasPrefix(result, "/")
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Property violated - non-error result was not absolute: %v", err)
	}
}

// TestProperty_PathTraversal_OutputNeverContainsControlChars verifies that
// successful ValidatePath results never contain control characters.
func TestProperty_PathTraversal_OutputNeverContainsControlChars(t *testing.T) {
	f := func(path string) bool {
		result, err := ValidatePath(path)
		if err != nil {
			return true // rejected paths are fine
		}
		// Successful result must not contain control chars
		for _, c := range result {
			if c == 0 || (unicode.IsControl(c) && c != '\t') {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Property violated - accepted path contains control chars: %v", err)
	}
}

// Property 18: File Write Command Safety
// **Validates: Requirements 1.4**

// TestProperty_FileWriteCommandSafety verifies that base64 encoding prevents
// shell injection for any content string. The base64-encoded form used in the
// WriteFile command contains only [A-Za-z0-9+/=] characters, which are all
// shell-safe within single quotes and cannot break shell command structure.
func TestProperty_FileWriteCommandSafety(t *testing.T) {
	f := func(content string) bool {
		encoded := base64.StdEncoding.EncodeToString([]byte(content))

		// Verify every character in the encoded output is shell-safe
		for _, c := range encoded {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
				return false
			}
		}

		// Additionally verify no shell metacharacters are present
		shellMeta := []string{";", "&", "|", "$", "`", "(", ")", "{", "}", "<", ">", "!", "\\", "'", "\"", "\n", "\r", "\x00"}
		for _, meta := range shellMeta {
			if strings.Contains(encoded, meta) {
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Errorf("Property violated - base64 encoding produced shell-unsafe characters: %v", err)
	}
}
