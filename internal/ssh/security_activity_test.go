package ssh

import (
	"strings"
	"testing"
)

// Built from double-quoted lines: the fail2ban tree decorations themselves
// contain backticks, which cannot appear inside a raw string literal.
var sampleFail2banStatus = strings.Join([]string{
	"Status for the jail: sshd",
	"|- Filter",
	"|  |- Currently failed:\t1",
	"|  |- Total failed:\t7",
	"|  `- Journal matches:\t0",
	"`- Actions",
	"   |- Currently banned:\t2",
	"   |- Total banned:\t8",
	"   `- Banned IP list:\t203.0.113.7 198.51.100.2",
	"",
}, "\n")

func TestParseFail2banStatus(t *testing.T) {
	cur, total, failed, ips, ok := parseFail2banStatus(sampleFail2banStatus)
	if !ok {
		t.Fatal("expected the jail block to be recognized")
	}
	if cur != 2 {
		t.Errorf("currently banned = %d, want 2", cur)
	}
	if total != 8 {
		t.Errorf("total banned = %d, want 8", total)
	}
	if failed != 7 {
		t.Errorf("total failed = %d, want 7", failed)
	}
	if len(ips) != 2 || ips[0] != "203.0.113.7" || ips[1] != "198.51.100.2" {
		t.Errorf("banned ips = %v, want [203.0.113.7 198.51.100.2]", ips)
	}
}

// Older fail2ban releases align columns with spaces instead of tabs.
func TestParseFail2banStatus_SpaceSeparated(t *testing.T) {
	cur, _, _, ips, ok := parseFail2banStatus(
		"Status for the jail: sshd\n" +
			"|- Filter\n" +
			"|  |- Currently failed:  0\n" +
			"|  |- Total failed:      0\n" +
			"`- Actions\n" +
			"   |- Currently banned:  1\n" +
			"   |- Total banned:      1\n" +
			"   `- Banned IP list:    192.0.2.9\n")
	if !ok || cur != 1 || len(ips) != 1 || ips[0] != "192.0.2.9" {
		t.Fatalf("ok=%v cur=%d ips=%v", ok, cur, ips)
	}
}

// The list line doubles as the count when the counter line is missing.
func TestParseFail2banStatus_CounterFromList(t *testing.T) {
	cur, _, _, ips, ok := parseFail2banStatus("`- Banned IP list:\t10.0.0.1 10.0.0.2 10.0.0.3\n")
	if !ok || cur != 3 || len(ips) != 3 {
		t.Fatalf("ok=%v cur=%d ips=%v", ok, cur, ips)
	}
}

func TestParseFail2banStatus_Empty(t *testing.T) {
	_, _, _, _, ok := parseFail2banStatus("")
	if ok {
		t.Fatal("empty output must not be recognized as jail status")
	}
}

const sampleUFWStatus = "Status: active\n" +
	"Logging: on (low)\n" +
	"Default: deny (incoming), allow (outgoing), disabled (routed)\n" +
	"New profiles: skip\n" +
	"\n" +
	"To                         Action      From\n" +
	"--                         ------      ----\n" +
	"22/tcp                     LIMIT IN    Anywhere\n" +
	"443/tcp                    ALLOW IN    Anywhere\n"

func TestParseFirewallStatus_UFW(t *testing.T) {
	tool, status, ok := parseFirewallStatus("tool: ufw\n" + sampleUFWStatus)
	if !ok || tool != "ufw" {
		t.Fatalf("tool=%q ok=%v", tool, ok)
	}
	if !strings.Contains(status, "22/tcp") || !strings.HasPrefix(status, "Status: active") {
		t.Errorf("status text lost content: %q", status)
	}
	if act := ufwActive(status); act != "active" {
		t.Errorf("ufwActive = %q, want active", act)
	}
}

func TestParseFirewallStatus_FirewalldAndNone(t *testing.T) {
	tool, status, ok := parseFirewallStatus("tool: firewalld\nrunning\n")
	if !ok || tool != "firewalld" || strings.TrimSpace(status) != "running" {
		t.Fatalf("tool=%q status=%q ok=%v", tool, status, ok)
	}
	tool, _, ok = parseFirewallStatus("tool: none\n")
	if !ok || tool != "none" {
		t.Fatalf("tool=%q ok=%v", tool, ok)
	}
	if _, _, ok := parseFirewallStatus(""); ok {
		t.Fatal("empty output must not parse")
	}
	if _, _, ok := parseFirewallStatus("sudo: a password is required"); ok {
		t.Fatal("non-detection output must not parse")
	}
}

func TestParseFirewallStatus_CapsRuleTable(t *testing.T) {
	var b strings.Builder
	b.WriteString("tool: ufw\nStatus: active\n")
	for i := 0; i < 200; i++ {
		b.WriteString("3000/tcp                    ALLOW IN    Anywhere\n")
	}
	_, status, ok := parseFirewallStatus(b.String())
	if !ok {
		t.Fatal("expected parse ok")
	}
	if got := len(strings.Split(status, "\n")); got != fwStatusLineCap {
		t.Errorf("status lines = %d, want capped at %d", got, fwStatusLineCap)
	}
}

func TestUFWActive_Variants(t *testing.T) {
	if got := ufwActive("Status: inactive\nLogging: off"); got != "inactive" {
		t.Errorf("inactive → %q", got)
	}
	if got := ufwActive(""); got != "unknown" {
		t.Errorf("empty → %q", got)
	}
	if got := ufwActive("ERROR: Permission denied"); got != "unknown" {
		t.Errorf("error text → %q", got)
	}
}

func TestCapLines_PrefersTail(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	got := capLines(lines, 2)
	if len(got) != 2 || got[0] != "d" || got[1] != "e" {
		t.Errorf("capLines = %v, want tail [d e]", got)
	}
	if got := capLines(lines, 10); len(got) != 5 {
		t.Errorf("capLines under cap = %v", got)
	}
}
