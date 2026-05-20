package server

import (
	"path/filepath"
	"testing"

	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
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
		auths := getRegistryAuths(mgr)
		if auths != nil {
			t.Errorf("getRegistryAuths expected nil, got %v", auths)
		}
	})

	// 5. getSystemInfo and other metrics
	t.Run("getSystemInfo", func(t *testing.T) {
		dockerClient, err := docker.NewClient()
		if err == nil {
			defer dockerClient.Close()
			info := getSystemInfo(dockerClient)
			if info.OS == "" {
				t.Errorf("getSystemInfo returned empty OS")
			}
		}
	})

	// 6. getMemoryInfo
	t.Run("getMemoryInfo", func(t *testing.T) {
		total, used := getMemoryInfo()
		t.Logf("memory info: total=%d, used=%d", total, used)
	})

	// 7. getCPUPercent
	t.Run("getCPUPercent", func(t *testing.T) {
		pct := getCPUPercent()
		t.Logf("cpu percent: %f", pct)
	})

	// 8. getCgroupMemoryUsage
	t.Run("getCgroupMemoryUsage", func(t *testing.T) {
		usage := getCgroupMemoryUsage()
		t.Logf("cgroup memory usage: %d", usage)
	})

	// 9. getHostname
	t.Run("getHostname", func(t *testing.T) {
		hostname := getHostname()
		t.Logf("hostname: %s", hostname)
	})
}
