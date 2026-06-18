package docker

import (
	"strings"
	"testing"
)

func TestNormalizeRestartPolicy(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		force bool
		want  string
	}{
		{"empty defaults", "", true, "unless-stopped"},
		{"empty defaults no force", "", false, "unless-stopped"},
		{"no upgraded when forced", "no", true, "unless-stopped"},
		{"no preserved when not forced", "no", false, "no"},
		{"on-failure upgraded when forced", "on-failure", true, "unless-stopped"},
		{"on-failure preserved when not forced", "on-failure", false, "on-failure"},
		{"on-failure with retries upgraded", "on-failure:5", true, "unless-stopped"},
		{"on-failure with retries preserved", "on-failure:5", false, "on-failure:5"},
		{"always kept", "always", true, "always"},
		{"unless-stopped kept", "unless-stopped", true, "unless-stopped"},
		{"unknown defaults", "bogus", true, "unless-stopped"},
		{"uppercase normalized", "No", true, "unless-stopped"},
		{"whitespace trimmed", "  no  ", false, "no"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeRestartPolicy(tc.raw, tc.force)
			if got != tc.want {
				t.Fatalf("NormalizeRestartPolicy(%q, %v) = %q, want %q", tc.raw, tc.force, got, tc.want)
			}
		})
	}
}

func restartFor(t *testing.T, yamlOut, service string) string {
	t.Helper()
	cf, err := ParseComposeFile(yamlOut)
	if err != nil {
		t.Fatalf("re-parse failed: %v\n%s", err, yamlOut)
	}
	svc, ok := cf.Services[service]
	if !ok {
		t.Fatalf("service %q missing in output:\n%s", service, yamlOut)
	}
	return svc.Restart
}

func TestEnsureComposeAutoStartForcesRebootUnsafe(t *testing.T) {
	in := `services:
  web:
    image: nginx
    restart: "no"
  worker:
    image: busybox
    restart: on-failure
  db:
    image: postgres
`
	out, err := EnsureComposeAutoStart(in, "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, svc := range []string{"web", "worker", "db"} {
		if got := restartFor(t, out, svc); got != "unless-stopped" {
			t.Errorf("service %q restart = %q, want unless-stopped", svc, got)
		}
	}
}

func TestEnsureComposeAutoStartPreservesWhenNotForced(t *testing.T) {
	in := `services:
  job:
    image: busybox
    restart: "no"
`
	out, err := EnsureComposeAutoStart(in, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := restartFor(t, out, "job"); got != "no" {
		t.Errorf("restart = %q, want no (preserved)", got)
	}
}

func TestEnsureComposeAutoStartInsertsMissing(t *testing.T) {
	in := `services:
  app:
    image: caddy
`
	out, err := EnsureComposeAutoStart(in, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := restartFor(t, out, "app"); got != "unless-stopped" {
		t.Errorf("restart = %q, want unless-stopped", got)
	}
}

func TestEnsureComposeAutoStartOverrideWins(t *testing.T) {
	in := `services:
  app:
    image: caddy
    restart: unless-stopped
`
	out, err := EnsureComposeAutoStart(in, "always", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := restartFor(t, out, "app"); got != "always" {
		t.Errorf("restart = %q, want always (override)", got)
	}
}

func TestEnsureComposeAutoStartInvalidOverrideIgnored(t *testing.T) {
	in := `services:
  app:
    image: caddy
    restart: "no"
`
	// Invalid override falls back to forced normalization.
	out, err := EnsureComposeAutoStart(in, "bogus", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := restartFor(t, out, "app"); got != "unless-stopped" {
		t.Errorf("restart = %q, want unless-stopped", got)
	}
}

func TestEnsureComposeAutoStartPreservesOtherKeys(t *testing.T) {
	in := `services:
  web:
    image: nginx
    ports:
      - "8080:80"
    environment:
      FOO: bar
    restart: "no"
`
	out, err := EnsureComposeAutoStart(in, "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "8080:80") || !strings.Contains(out, "FOO") {
		t.Errorf("unrelated keys dropped:\n%s", out)
	}
	cf, err := ParseComposeFile(out)
	if err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
	if len(cf.Services["web"].Ports) != 1 {
		t.Errorf("ports lost: %+v", cf.Services["web"].Ports)
	}
}

func TestEnsureComposeAutoStartRejectsInvalidYAML(t *testing.T) {
	if _, err := EnsureComposeAutoStart("::: not yaml :::", "", true); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if _, err := EnsureComposeAutoStart("version: \"3\"\n", "", true); err == nil {
		t.Fatal("expected error when no services defined")
	}
}
