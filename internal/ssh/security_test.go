package ssh

import (
	"strings"
	"testing"
)

const sampleSSHDOutput = `port 22
passwordauthentication no
permitrootlogin without-password
kbdinteractiveauthentication no
pubkeyauthentication yes
`

func TestParseSSHDValue(t *testing.T) {
	if got := parseSSHDValue(sampleSSHDOutput, "passwordauthentication"); got != "no" {
		t.Errorf("passwordauthentication = %q, want no", got)
	}
	if got := parseSSHDValue(sampleSSHDOutput, "permitrootlogin"); got != "prohibit-password" {
		t.Errorf("permitrootlogin = %q, want prohibit-password (without-password normalized)", got)
	}
	if got := parseSSHDValue(sampleSSHDOutput, "maxauthtries"); got != "" {
		t.Errorf("missing key should return empty, got %q", got)
	}
	if got := parseSSHDValue("", "passwordauthentication"); got != "" {
		t.Errorf("empty output should return empty, got %q", got)
	}
}

func TestSecurityDropInLines(t *testing.T) {
	// Password disabled only.
	lines := securityDropInLines(true, "")
	want := []string{
		"# Managed by Dockpal SSH hardening — do not edit",
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("password-only lines = %v, want %v", lines, want)
	}

	// Root login disabled only — comment header still present.
	lines = securityDropInLines(false, "no")
	if len(lines) != 2 || lines[0] != "PermitRootLogin no" && lines[1] != "PermitRootLogin no" {
		t.Errorf("root-only lines = %v", lines)
	}
	found := false
	for _, l := range lines {
		if l == "PermitRootLogin no" {
			found = true
		}
	}
	if !found {
		t.Errorf("PermitRootLogin no missing: %v", lines)
	}

	// Nothing managed → empty (drop-in should be removed).
	if lines := securityDropInLines(false, ""); len(lines) != 0 {
		t.Errorf("unmanaged state should produce no lines, got %v", lines)
	}

	// Both managed.
	both := securityDropInLines(true, "no")
	if strings.Count(strings.Join(both, "\n"), "no") < 3 {
		t.Errorf("expected password + kbd + root lines, got %v", both)
	}
}
