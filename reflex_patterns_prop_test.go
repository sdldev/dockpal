package main

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// Property 1: Regex pattern correctness for file matching
// **Validates: Requirements 2.2**
//
// For any valid Go source file path (matching *.go and not in vendor/, not ending
// in _generated.go or .pb.go), the Go Source Rebuild watcher's include pattern
// SHALL match the path AND the exclusion patterns SHALL NOT match the path.
// Conversely, for any path in excluded directories or matching excluded suffixes,
// at least one exclusion pattern SHALL match.

var (
	includePattern   = regexp.MustCompile(`\.go$`)
	vendorExclude    = regexp.MustCompile(`^vendor/`)
	generatedExclude = regexp.MustCompile(`_generated\.go$`)
	pbExclude        = regexp.MustCompile(`\.pb\.go$`)
)

// genValidGoSourcePath generates a random valid Go source file path that:
// - Ends in .go
// - Does NOT start with vendor/
// - Does NOT end in _generated.go
// - Does NOT end in .pb.go
func genValidGoSourcePath(rng *rand.Rand) string {
	// Generate 0-3 directory segments (not starting with "vendor")
	numDirs := rng.Intn(4)
	parts := make([]string, 0, numDirs+1)

	dirNames := []string{"internal", "cmd", "pkg", "api", "server", "auth", "docker", "handlers", "models", "utils", "config", "middleware"}

	for i := 0; i < numDirs; i++ {
		dir := dirNames[rng.Intn(len(dirNames))]
		parts = append(parts, dir)
	}

	// Generate a filename that ends in .go but NOT _generated.go or .pb.go
	baseNames := []string{"main", "handler", "client", "server", "config", "routes", "middleware", "auth", "db", "utils", "helpers", "types", "models"}
	suffixes := []string{"", "_test", "_linux", "_amd64", "_impl"}

	base := baseNames[rng.Intn(len(baseNames))]
	suffix := suffixes[rng.Intn(len(suffixes))]
	filename := base + suffix + ".go"

	parts = append(parts, filename)
	return strings.Join(parts, "/")
}

// genExcludedPath generates a random path that should be excluded by at least one
// exclusion pattern: either in vendor/, ending in _generated.go, or ending in .pb.go.
func genExcludedPath(rng *rand.Rand) string {
	exclusionType := rng.Intn(3)

	switch exclusionType {
	case 0:
		// vendor/ path
		subDirs := []string{"github.com", "golang.org", "google.golang.org", "gopkg.in"}
		pkgs := []string{"errors", "fmt", "net", "http", "json", "yaml", "proto"}
		files := []string{"main.go", "lib.go", "util.go", "types.go", "handler.go"}
		return "vendor/" + subDirs[rng.Intn(len(subDirs))] + "/" + pkgs[rng.Intn(len(pkgs))] + "/" + files[rng.Intn(len(files))]
	case 1:
		// _generated.go path
		dirs := []string{"", "internal/", "api/", "pkg/models/", "internal/schema/"}
		bases := []string{"types", "schema", "api", "models", "zz_deepcopy", "wire"}
		dir := dirs[rng.Intn(len(dirs))]
		base := bases[rng.Intn(len(bases))]
		return dir + base + "_generated.go"
	default:
		// .pb.go path
		dirs := []string{"", "api/", "internal/proto/", "pkg/pb/", "proto/"}
		bases := []string{"service", "message", "types", "api", "health", "rpc"}
		dir := dirs[rng.Intn(len(dirs))]
		base := bases[rng.Intn(len(bases))]
		return dir + base + ".pb.go"
	}
}

// TestReflexProperty_ValidGoSourceMatchesIncludeNotExclude verifies that for any
// valid Go source file path, the include pattern matches AND no exclusion pattern matches.
func TestReflexProperty_ValidGoSourceMatchesIncludeNotExclude(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 500; i++ {
		path := genValidGoSourcePath(rng)

		// Include pattern must match
		if !includePattern.MatchString(path) {
			t.Fatalf("Property violated - include pattern `\\.go$` did not match valid Go source path: %q", path)
		}

		// No exclusion pattern should match
		if vendorExclude.MatchString(path) {
			t.Fatalf("Property violated - vendor exclusion `^vendor/` matched valid Go source path: %q", path)
		}
		if generatedExclude.MatchString(path) {
			t.Fatalf("Property violated - generated exclusion `_generated\\.go$` matched valid Go source path: %q", path)
		}
		if pbExclude.MatchString(path) {
			t.Fatalf("Property violated - protobuf exclusion `\\.pb\\.go$` matched valid Go source path: %q", path)
		}
	}
}

// TestReflexProperty_ExcludedPathMatchesAtLeastOneExclusion verifies that for any
// path in excluded directories or matching excluded suffixes, at least one exclusion
// pattern matches.
func TestReflexProperty_ExcludedPathMatchesAtLeastOneExclusion(t *testing.T) {
	rng := rand.New(rand.NewSource(99))

	for i := 0; i < 500; i++ {
		path := genExcludedPath(rng)

		// At least one exclusion pattern must match
		matchesVendor := vendorExclude.MatchString(path)
		matchesGenerated := generatedExclude.MatchString(path)
		matchesPb := pbExclude.MatchString(path)

		if !matchesVendor && !matchesGenerated && !matchesPb {
			t.Fatalf("Property violated - no exclusion pattern matched excluded path: %q", path)
		}
	}
}

// TestReflexProperty_ExcludedPathsAreAlsoGoFiles verifies that excluded paths
// (vendor .go files, _generated.go, .pb.go) still match the include pattern,
// demonstrating that exclusion patterns are necessary to filter them out.
func TestReflexProperty_ExcludedPathsAreAlsoGoFiles(t *testing.T) {
	rng := rand.New(rand.NewSource(77))

	for i := 0; i < 500; i++ {
		path := genExcludedPath(rng)

		// All generated excluded paths end in .go, so include pattern should match
		if !includePattern.MatchString(path) {
			t.Fatalf("Property violated - excluded path %q does not match include pattern `\\.go$` (exclusion patterns would be unnecessary)", path)
		}
	}
}
