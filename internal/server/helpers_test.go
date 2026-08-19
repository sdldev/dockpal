package server

import (
	"path/filepath"
	"strings"
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

func TestApplyNetworkMode(t *testing.T) {
	compose := "services:\n  postgres:\n    image: postgres:17-alpine\n    ports:\n      - '5432:5432'\n    volumes:\n      - pg-data:/var/lib/postgresql/data\nvolumes:\n  pg-data:\n"

	// empty or bridge mode leaves compose untouched (default bridge)
	if got := applyNetworkMode(compose, "", ""); got != compose {
		t.Errorf("empty mode: compose modified")
	}
	if got := applyNetworkMode(compose, "bridge", ""); got != compose {
		t.Errorf("bridge mode: compose modified")
	}

	// host mode sets network_mode and removes ports (mutually exclusive)
	got := applyNetworkMode(compose, "host", "")
	if !strings.Contains(got, "network_mode: host") {
		t.Errorf("host mode: missing network_mode, got:\n%s", got)
	}
	if strings.Contains(got, "ports:") {
		t.Errorf("host mode: ports must be removed, got:\n%s", got)
	}

	// none mode sets network_mode: none
	got = applyNetworkMode(compose, "none", "")
	if !strings.Contains(got, "network_mode: none") {
		t.Errorf("none mode: missing network_mode, got:\n%s", got)
	}

	// custom mode attaches the named network to services and top level
	got = applyNetworkMode(compose, "custom", "my-net")
	if !strings.Contains(got, "my-net") {
		t.Errorf("custom mode: network name missing, got:\n%s", got)
	}
	if strings.Contains(got, "network_mode") {
		t.Errorf("custom mode: must not set network_mode, got:\n%s", got)
	}
	if !strings.Contains(got, "ports:") {
		t.Errorf("custom mode: ports must be preserved, got:\n%s", got)
	}

	// custom mode without a network name is a no-op
	if got := applyNetworkMode(compose, "custom", ""); got != compose {
		t.Errorf("custom mode without name: compose modified")
	}

	// invalid YAML returns the original untouched
	if got := applyNetworkMode("not yaml: [", "host", ""); got != "not yaml: [" {
		t.Errorf("invalid YAML: compose modified")
	}
}
