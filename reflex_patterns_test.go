package main

import (
	"regexp"
	"testing"
)

// TestReflexPatterns validates that the regex patterns used in reflex.conf
// compile without error and match/reject the intended file paths.
// Requirements: 1.4, 2.2, 4.2

func TestReflexPatterns_GoSourceInclude(t *testing.T) {
	pattern := regexp.MustCompile(`\.go$`)

	tests := []struct {
		path  string
		match bool
	}{
		{"main.go", true},
		{"internal/auth/handler.go", true},
		{"internal/docker/client.go", true},
		{"cmd/server/main.go", true},
		{"README.md", false},
		{"go.mod", false},
		{"reflex.conf", false},
		{"web/pages/dashboard.html", false},
		{"Makefile", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := pattern.MatchString(tt.path)
			if got != tt.match {
				t.Errorf("pattern %q on %q: got %v, want %v", pattern.String(), tt.path, got, tt.match)
			}
		})
	}
}

func TestReflexPatterns_VendorExclusion(t *testing.T) {
	pattern := regexp.MustCompile(`^vendor/`)

	tests := []struct {
		path  string
		match bool
	}{
		{"vendor/lib/foo.go", true},
		{"vendor/github.com/pkg/errors/errors.go", true},
		{"internal/vendor/x.go", false},
		{"cmd/vendor_tool/main.go", false},
		{"main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := pattern.MatchString(tt.path)
			if got != tt.match {
				t.Errorf("pattern %q on %q: got %v, want %v", pattern.String(), tt.path, got, tt.match)
			}
		})
	}
}

func TestReflexPatterns_GeneratedFileExclusion(t *testing.T) {
	generatedPattern := regexp.MustCompile(`_generated\.go$`)
	pbPattern := regexp.MustCompile(`\.pb\.go$`)

	t.Run("generated pattern", func(t *testing.T) {
		tests := []struct {
			path  string
			match bool
		}{
			{"types_generated.go", true},
			{"internal/api/schema_generated.go", true},
			{"main.go", false},
			{"generated.go", false},
			{"handler.go", false},
		}

		for _, tt := range tests {
			t.Run(tt.path, func(t *testing.T) {
				got := generatedPattern.MatchString(tt.path)
				if got != tt.match {
					t.Errorf("pattern %q on %q: got %v, want %v", generatedPattern.String(), tt.path, got, tt.match)
				}
			})
		}
	})

	t.Run("protobuf pattern", func(t *testing.T) {
		tests := []struct {
			path  string
			match bool
		}{
			{"api/service.pb.go", true},
			{"internal/proto/message.pb.go", true},
			{"main.go", false},
			{"pb.go", false},
			{"handler.go", false},
		}

		for _, tt := range tests {
			t.Run(tt.path, func(t *testing.T) {
				got := pbPattern.MatchString(tt.path)
				if got != tt.match {
					t.Errorf("pattern %q on %q: got %v, want %v", pbPattern.String(), tt.path, got, tt.match)
				}
			})
		}
	})
}

func TestReflexPatterns_BinaryPattern(t *testing.T) {
	pattern := regexp.MustCompile(`^dockpal$`)

	tests := []struct {
		path  string
		match bool
	}{
		{"dockpal", true},
		{"dockpal-linux-amd64", false},
		{"dockpal-linux-arm64", false},
		{"internal/dockpal_test.go", false},
		{"./dockpal", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := pattern.MatchString(tt.path)
			if got != tt.match {
				t.Errorf("pattern %q on %q: got %v, want %v", pattern.String(), tt.path, got, tt.match)
			}
		})
	}
}

func TestReflexPatterns_WebAssetInclude(t *testing.T) {
	pattern := regexp.MustCompile(`^web/.*\.(html|css|js)$`)

	tests := []struct {
		path  string
		match bool
	}{
		{"web/pages/dashboard.html", true},
		{"web/assets/styles.css", true},
		{"web/scripts/app.js", true},
		{"web/partials/login.html", true},
		{"web/embed.go", false},
		{"web/templates/base.tmpl", false},
		{"internal/web/page.html", false},
		{"styles.css", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := pattern.MatchString(tt.path)
			if got != tt.match {
				t.Errorf("pattern %q on %q: got %v, want %v", pattern.String(), tt.path, got, tt.match)
			}
		})
	}
}

func TestReflexPatterns_WebVendorExclusion(t *testing.T) {
	pattern := regexp.MustCompile(`^web/assets/vendor/`)

	tests := []struct {
		path  string
		match bool
	}{
		{"web/assets/vendor/alpine.js", true},
		{"web/assets/vendor/htmx.min.js", true},
		{"web/assets/vendor/bootstrap/css/bootstrap.css", true},
		{"web/assets/styles.css", false},
		{"web/pages/dashboard.html", false},
		{"vendor/lib/foo.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := pattern.MatchString(tt.path)
			if got != tt.match {
				t.Errorf("pattern %q on %q: got %v, want %v", pattern.String(), tt.path, got, tt.match)
			}
		})
	}
}

func TestReflexPatterns_AllPatternsCompile(t *testing.T) {
	// Verify all patterns from reflex.conf compile without error
	patterns := []struct {
		name    string
		pattern string
	}{
		{"go source include", `\.go$`},
		{"vendor exclusion", `^vendor/`},
		{"generated file exclusion", `_generated\.go$`},
		{"protobuf exclusion", `\.pb\.go$`},
		{"binary match", `^dockpal$`},
		{"web asset include", `^web/.*\.(html|css|js)$`},
		{"web vendor exclusion", `^web/assets/vendor/`},
	}

	for _, tt := range patterns {
		t.Run(tt.name, func(t *testing.T) {
			_, err := regexp.Compile(tt.pattern)
			if err != nil {
				t.Errorf("pattern %q (%s) failed to compile: %v", tt.pattern, tt.name, err)
			}
		})
	}
}
