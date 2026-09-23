package docker

import (
	"testing"
)

func TestParseComposeFile_Valid(t *testing.T) {
	yaml := `
version: "3.8"
services:
  web:
    image: nginx:latest
    ports:
      - "8080:80"
    volumes:
      - ./data:/var/www:ro
    environment:
      - NODE_ENV=production
    networks:
      - frontend
    depends_on:
      - db
  db:
    image: postgres:15
    environment:
      POSTGRES_PASSWORD: secret
      POSTGRES_DB: mydb
    volumes:
      - pgdata:/var/lib/postgresql/data
`

	cf, err := ParseComposeFile(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cf.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(cf.Services))
	}

	web := cf.Services["web"]
	if web.Image != "nginx:latest" {
		t.Errorf("expected image nginx:latest, got %s", web.Image)
	}
	if len(web.Ports) != 1 || web.Ports[0] != "8080:80" {
		t.Errorf("unexpected ports: %v", web.Ports)
	}
	if len(web.Networks) != 1 || web.Networks[0] != "frontend" {
		t.Errorf("unexpected networks: %v", web.Networks)
	}

	db := cf.Services["db"]
	if db.Image != "postgres:15" {
		t.Errorf("expected image postgres:15, got %s", db.Image)
	}
}

func TestParseComposeFile_NoServices(t *testing.T) {
	yaml := `version: "3"`
	_, err := ParseComposeFile(yaml)
	if err == nil {
		t.Fatal("expected error for missing services")
	}
}

func TestParseComposeFile_InvalidYAML(t *testing.T) {
	yaml := `{{{ not valid yaml`
	_, err := ParseComposeFile(yaml)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		spec     string
		host     int
		cport    int
		protocol string
	}{
		{"80", 80, 80, "tcp"},
		{"8080:80", 8080, 80, "tcp"},
		{"8080:80/tcp", 8080, 80, "tcp"},
		{"5353:53/udp", 5353, 53, "udp"},
		{"9090:9090", 9090, 9090, "tcp"},
	}

	for _, tt := range tests {
		pb, err := ParsePort(tt.spec)
		if err != nil {
			t.Errorf("ParsePort(%q) error: %v", tt.spec, err)
			continue
		}
		if pb.HostPort != tt.host {
			t.Errorf("ParsePort(%q) host = %d, want %d", tt.spec, pb.HostPort, tt.host)
		}
		if pb.ContainerPort != tt.cport {
			t.Errorf("ParsePort(%q) container = %d, want %d", tt.spec, pb.ContainerPort, tt.cport)
		}
		if pb.Protocol != tt.protocol {
			t.Errorf("ParsePort(%q) protocol = %s, want %s", tt.spec, pb.Protocol, tt.protocol)
		}
	}
}

func TestParsePort_Invalid(t *testing.T) {
	_, err := ParsePort("abc:def")
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestParseVolume(t *testing.T) {
	tests := []struct {
		spec      string
		host      string
		container string
		readOnly  bool
	}{
		{"/data", "", "/data", false},
		{"./host:/container", "./host", "/container", false},
		{"/host:/container:ro", "/host", "/container", true},
		{"/host:/container:rw", "/host", "/container", false},
	}

	for _, tt := range tests {
		vm, err := ParseVolume(tt.spec)
		if err != nil {
			t.Errorf("ParseVolume(%q) error: %v", tt.spec, err)
			continue
		}
		if vm.HostPath != tt.host {
			t.Errorf("ParseVolume(%q) host = %q, want %q", tt.spec, vm.HostPath, tt.host)
		}
		if vm.ContainerPath != tt.container {
			t.Errorf("ParseVolume(%q) container = %q, want %q", tt.spec, vm.ContainerPath, tt.container)
		}
		if vm.ReadOnly != tt.readOnly {
			t.Errorf("ParseVolume(%q) readOnly = %v, want %v", tt.spec, vm.ReadOnly, tt.readOnly)
		}
	}
}

func TestParseEnvironment_List(t *testing.T) {
	env := []interface{}{"FOO=bar", "BAZ=qux"}
	result := ParseEnvironment(env)
	if len(result) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(result))
	}
	if result[0] != "FOO=bar" || result[1] != "BAZ=qux" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestParseEnvironment_Map(t *testing.T) {
	env := map[string]interface{}{
		"FOO": "bar",
		"NUM": 42,
	}
	result := ParseEnvironment(env)
	if len(result) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(result))
	}
	// Map order is non-deterministic, just check both are present
	found := make(map[string]bool)
	for _, r := range result {
		found[r] = true
	}
	if !found["FOO=bar"] || !found["NUM=42"] {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestParseEnvironment_Nil(t *testing.T) {
	result := ParseEnvironment(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestResolveStartOrder_Simple(t *testing.T) {
	yaml := `
services:
  web:
    image: nginx
    depends_on:
      - db
      - redis
  db:
    image: postgres
  redis:
    image: redis
`
	cf, err := ParseComposeFile(yaml)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	order, err := ResolveStartOrder(cf)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	// db and redis must come before web
	indexOf := func(s string) int {
		for i, v := range order {
			if v == s {
				return i
			}
		}
		return -1
	}

	if indexOf("db") > indexOf("web") {
		t.Error("db should start before web")
	}
	if indexOf("redis") > indexOf("web") {
		t.Error("redis should start before web")
	}
}

func TestResolveStartOrder_Circular(t *testing.T) {
	yaml := `
services:
  a:
    image: alpine
    depends_on:
      - b
  b:
    image: alpine
    depends_on:
      - a
`
	cf, err := ParseComposeFile(yaml)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	_, err = ResolveStartOrder(cf)
	if err == nil {
		t.Fatal("expected circular dependency error")
	}
}

func TestResolveStartOrder_NoDeps(t *testing.T) {
	yaml := `
services:
  web:
    image: nginx
  api:
    image: node
`
	cf, err := ParseComposeFile(yaml)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	order, err := ResolveStartOrder(cf)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("expected 2 services in order, got %d", len(order))
	}
}

// TestBuildBinds_NamedVolumeScopedToProject verifies that a bare-name volume
// like "pg-data" is prefixed with the project name, matching `docker compose`.
// Without the prefix, two unrelated projects declaring "pg-data" would mount
// the same global Docker volume — the collision that made a fresh postgres
// stack inherit PG16 data files from an older project and crash-loop.
func TestBuildBinds_NamedVolumeScopedToProject(t *testing.T) {
	binds, err := buildBinds("postgres16-376157", []string{"pg-data:/var/lib/postgresql/data"})
	if err != nil {
		t.Fatalf("buildBinds error: %v", err)
	}
	if len(binds) != 1 {
		t.Fatalf("expected 1 bind, got %d: %v", len(binds), binds)
	}
	want := "postgres16-376157_pg-data:/var/lib/postgresql/data"
	if binds[0] != want {
		t.Fatalf("named volume not project-scoped: got %q, want %q", binds[0], want)
	}
}

// TestBuildBinds_AbsolutePathIsHostMount verifies absolute sources are passed
// through untouched rather than being treated as project-scoped volumes.
func TestBuildBinds_AbsolutePathIsHostMount(t *testing.T) {
	specs := map[string]string{
		"socket":   "/var/run/docker.sock:/var/run/docker.sock",
		"socket-ro": "/var/run/docker.sock:/var/run/docker.sock:ro",
	}
	for name, spec := range specs {
		binds, err := buildBinds("anyproject", []string{spec})
		if err != nil {
			t.Fatalf("%s: buildBinds error: %v", name, err)
		}
		if binds[0] != spec {
			t.Fatalf("%s: absolute path should be unchanged: got %q, want %q", name, binds[0], spec)
		}
	}
}

// TestBuildBinds_DistinctProjectsGetDistinctVolumes is the regression guard
// for the actual bug: same template, two projects, no shared volume.
func TestBuildBinds_DistinctProjectsGetDistinctVolumes(t *testing.T) {
	a, _ := buildBinds("postgres-A", []string{"pg-data:/var/lib/postgresql/data"})
	b, _ := buildBinds("postgres-B", []string{"pg-data:/var/lib/postgresql/data"})
	if a[0] == b[0] {
		t.Fatalf("distinct projects must not share a volume: both resolved to %q", a[0])
	}
}

// TestBuildBinds_AnonymousVolumeYieldsNoBind verifies a source-less spec
// (anonymous volume) contributes no bind rather than a malformed one.
func TestBuildBinds_AnonymousVolumeYieldsNoBind(t *testing.T) {
	binds, err := buildBinds("proj", []string{"/var/lib/postgresql/data"})
	if err != nil {
		t.Fatalf("buildBinds error: %v", err)
	}
	if len(binds) != 0 {
		t.Fatalf("anonymous volume must not produce a bind, got %v", binds)
	}
}
