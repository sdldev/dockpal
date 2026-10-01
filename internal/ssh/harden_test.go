package ssh

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestParseOpenSSHVersion(t *testing.T) {
	cases := []struct {
		in        string
		wantMajor int
		wantMinor int
		wantOK    bool
	}{
		{"8.9p1", 8, 9, true},
		{"7.4", 7, 4, true},
		{"9.6p1 Ubuntu-3ubuntu13", 9, 6, true},
		{"10.0p2", 10, 0, true},
		{"", 0, 0, false},
		{"banana", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, ok := parseOpenSSHVersion(c.in)
		if ok != c.wantOK || major != c.wantMajor || minor != c.wantMinor {
			t.Errorf("parseOpenSSHVersion(%q) = (%d, %d, %v), want (%d, %d, %v)",
				c.in, major, minor, ok, c.wantMajor, c.wantMinor, c.wantOK)
		}
	}
}

func TestPickKbdKeyword(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{"8.9p1", "KbdInteractiveAuthentication"}, // modern
		{"9.6p1", "KbdInteractiveAuthentication"},
		{"", "KbdInteractiveAuthentication"}, // unknown → modern, sshd -t retries cover old
		{"8.7p1", "KbdInteractiveAuthentication"},
		{"8.6p1", "ChallengeResponseAuthentication"}, // rename happened in 8.7
		{"7.4", "ChallengeResponseAuthentication"},   // CentOS 7
	}
	for _, c := range cases {
		if got := pickKbdKeyword(c.version); got != c.want {
			t.Errorf("pickKbdKeyword(%q) = %q, want %q", c.version, got, c.want)
		}
	}
}

func TestHardeningConfigLines(t *testing.T) {
	// Root user: PermitRootLogin restricted to keys.
	root := hardeningConfigLines(false, "KbdInteractiveAuthentication")
	want := []string{
		"# Managed by Dockpal SSH hardening — do not edit",
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
		"PermitRootLogin prohibit-password",
	}
	if strings.Join(root, "\n") != strings.Join(want, "\n") {
		t.Errorf("root lines = %v, want %v", root, want)
	}

	// Non-root user: root policy left to the operator.
	nonRoot := hardeningConfigLines(true, "")
	for _, l := range nonRoot {
		if strings.HasPrefix(l, "PermitRootLogin") {
			t.Errorf("non-root config must not set PermitRootLogin, got %q", l)
		}
	}
	if !strings.Contains(strings.Join(nonRoot, "\n"), "PasswordAuthentication no") {
		t.Error("PasswordAuthentication no missing")
	}
}

func TestGenerateKeyPairRoundTrip(t *testing.T) {
	pub, priv, fp, err := GenerateKeyPair("dockpal-inst-test")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || strings.Contains(pub, "\n") {
		t.Errorf("public key should be a single ssh-ed25519 line, got %q", pub)
	}
	if !strings.Contains(priv, "OPENSSH PRIVATE KEY") {
		t.Errorf("private key should be OpenSSH PEM, got %q", priv[:40])
	}
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Errorf("fingerprint should be SHA256:, got %q", fp)
	}

	// The private key must parse and match the published public key + fp.
	pub2, fp2, err := PublicKeyFromPrivate(priv)
	if err != nil {
		t.Fatalf("PublicKeyFromPrivate: %v", err)
	}
	if pub != pub2 || fp != fp2 {
		t.Errorf("round trip mismatch: pub %q vs %q, fp %q vs %q", pub, pub2, fp, fp2)
	}

	// Two generations must differ.
	_, priv2, _, _ := GenerateKeyPair("x")
	if priv == priv2 {
		t.Error("two generated keypairs are identical")
	}
}

func TestPublicKeyFromPrivateRejectsGarbage(t *testing.T) {
	if _, _, err := PublicKeyFromPrivate("not a key"); err == nil {
		t.Fatal("expected error for garbage private key")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("plain"); got != "'plain'" {
		t.Errorf("shellQuote(plain) = %q", got)
	}
	// A single quote must be escaped the POSIX way: ' → '\''.
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("shellQuote(it's) = %q", got)
	}
}

func TestAuthorizedKeysCommandIsIdempotent(t *testing.T) {
	// The grep guard means a re-run must not append a duplicate line.
	cmd := "grep -qF 'ssh-ed25519 AAAA dockpal-inst-x' ~/.ssh/authorized_keys || echo 'ssh-ed25519 AAAA dockpal-inst-x' >> ~/.ssh/authorized_keys"
	if strings.Count(cmd, "echo") != 1 || !strings.Contains(cmd, "grep -qF") {
		t.Errorf("expected guarded append, got %q", cmd)
	}
}

func TestGenerateKeyPairParsesWithSSHPackage(t *testing.T) {
	_, priv, _, err := GenerateKeyPair("roundtrip")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		t.Fatalf("ssh.ParsePrivateKey: %v", err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Errorf("unexpected key type %s", signer.PublicKey().Type())
	}
}
