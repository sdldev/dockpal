package server

import (
	"path/filepath"
	"testing"

	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
)

func TestHelperFunctions(t *testing.T) {
	// 1. sanitizeFilename
	t.Run("sanitizeFilename", func(t *testing.T) {
		got := sanitizeFilename("test\r\nfile\"name.txt")
		want := "testfile'name.txt"
		if got != want {
			t.Errorf("sanitizeFilename got %q, want %q", got, want)
		}
	})

	// 2. generateID
	t.Run("generateID", func(t *testing.T) {
		id := generateID("pref")
		if len(id) == 0 {
			t.Errorf("generateID returned empty string")
		}
	})

	// 3. extractFirstPort
	t.Run("extractFirstPort", func(t *testing.T) {
		// Valid compose with ports
		compose := `
services:
  web:
    image: nginx
    ports:
      - "8080:80"
`
		port := extractFirstPort(compose)
		if port != 80 {
			t.Errorf("extractFirstPort got %d, want 80", port)
		}

		// Invalid compose
		port = extractFirstPort("invalid compose")
		if port != 80 {
			t.Errorf("extractFirstPort invalid compose got %d, want 80", port)
		}
	})

	// 4. getRegistryAuths
	t.Run("getRegistryAuths", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.New(dbPath)
		if err != nil {
			t.Fatalf("failed to create test db: %v", err)
		}
		defer database.Close()

		mgr := registry.NewManager(database, "secret")
		auths := getRegistryAuths(mgr, "services:\n  app:\n    image: nginx:latest")
		if auths != nil {
			t.Errorf("getRegistryAuths expected nil, got %v", auths)
		}
	})

}
