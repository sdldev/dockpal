package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// HardenParams describes one SSH-hardening run: switch a server that is
// reachable with the current credential over to key-only authentication.
type HardenParams struct {
	Host string
	Port int
	User string

	// AuthType/AuthSecret are the credentials the panel already holds for this
	// instance ("password" or "key") — used for the first connection.
	AuthType   string
	AuthSecret string

	// ExpectedHostKey pins the server's SSH host key (TOFU like the installer;
	// empty prints the fingerprint for the operator to record).
	ExpectedHostKey string

	// PublicKey is the authorized_keys line of the key that stays on the
	// server; PrivateKeyPEM is its matching private key. The panel opens a
	// FRESH key-only connection with it and only disables password auth once
	// that connection succeeds — disabling first would risk a total lockout.
	PublicKey     string
	PrivateKeyPEM string

	// TestPassword is the password that used to work. After the reload the
	// panel dials again with it and REQUIRES the server to reject it. Empty
	// when the original auth was already a key (nothing to disprove).
	TestPassword string
}

// HardenSSH performs the safe hardening sequence, streaming progress to w:
//
//  1. connect with the current (password or key) credential
//  2. append PublicKey to ~/.ssh/authorized_keys (idempotent)
//  3. verify key-only login in a NEW connection
//  4. write an sshd drop-in disabling password auth, validate with sshd -t
//  5. reload sshd, then verify key login works AND password login is rejected
//
// Any failure before step 4 leaves the server unchanged except an additive
// authorized_keys line. A failure after the drop-in is rolled back over the
// still-open original connection. On success the server no longer accepts
// passwords for this user.
func HardenSSH(params HardenParams, w io.Writer) error {
	if params.Port == 0 {
		params.Port = 22
	}
	if params.User == "" {
		params.User = "root"
	}
	sudo := params.User != "root"
	step := func(format string, args ...interface{}) {
		fmt.Fprintf(w, "[Dockpal Hardening] "+format+"\n", args...)
	}

	step("Starting SSH hardening on %s:%d as user %s...", params.Host, params.Port, params.User)

	// Step 1 — connect with whatever credential the instance has now. This
	// client stays open for the whole run: if the reload ever breaks sshd it
	// is the lifeline used to roll the config back.
	client, err := dialSSH(params.Host, params.Port, params.User, params.AuthType, params.AuthSecret, params.ExpectedHostKey, w)
	if err != nil {
		return err
	}
	defer client.Close()
	step("Connected with existing %s credentials.", params.AuthType)

	if err := runCommand(client, "echo dockpal-harden-ok", io.Discard); err != nil {
		return fmt.Errorf("connected but could not run commands: %w", err)
	}

	// Step 2 — install the public key (additive, safe to leave behind even if
	// a later step fails). ~ is the login user's own home, so no sudo here.
	pubLine := strings.TrimSpace(params.PublicKey)
	if pubLine == "" {
		return fmt.Errorf("no public key to install")
	}
	step("Step 2/5: installing public key into ~/.ssh/authorized_keys...")
	keysCmd := "mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && " +
		"grep -qF '" + pubLine + "' ~/.ssh/authorized_keys || echo '" + pubLine + "' >> ~/.ssh/authorized_keys"
	if err := runCommand(client, keysCmd, io.Discard); err != nil {
		return fmt.Errorf("failed to install public key: %w", err)
	}

	// Step 3 — prove the key actually logs in before touching sshd config.
	step("Step 3/5: verifying key-only login in a fresh connection...")
	if err := verifyKeyLogin(params.Host, params.Port, params.User, params.PrivateKeyPEM, params.ExpectedHostKey); err != nil {
		return fmt.Errorf("key login verification failed — server left unchanged: %w", err)
	}
	step("Key login verified.")

	// Step 4 — write the sshd hardening config. Drop-in when sshd_config has
	// the Include (Ubuntu 20.04+, Debian 11+), otherwise a managed block.
	step("Step 4/5: disabling password authentication in sshd...")
	mode, err := configMode(client, sudo)
	if err != nil {
		return err
	}
	version := detectOpenSSHVersion(client)
	lines := hardeningConfigLines(sudo, pickKbdKeyword(version))
	if mode == "dropin" {
		if err := writeDropIn(client, sudo, dropInPath, lines); err != nil {
			return err
		}
	} else {
		if err := writeManagedBlock(client, sudo, lines); err != nil {
			return err
		}
	}

	// sshd -t rejects bad keywords/configs. Try keyword variants before
	// giving up: old OpenSSH (<8.7) only knows ChallengeResponseAuthentication.
	if err := validateSSHD(client, sudo); err != nil {
		step("sshd -t rejected the config (%v) — retrying with legacy keywords...", err)
		lines = hardeningConfigLines(sudo, "ChallengeResponseAuthentication")
		if mode == "dropin" {
			_ = writeDropIn(client, sudo, dropInPath, lines)
		} else {
			_ = writeManagedBlock(client, sudo, lines)
		}
		if err := validateSSHD(client, sudo); err != nil {
			step("still invalid (%v) — retrying with PasswordAuthentication only...", err)
			lines = hardeningConfigLines(sudo, "")
			if mode == "dropin" {
				_ = writeDropIn(client, sudo, dropInPath, lines)
			} else {
				_ = writeManagedBlock(client, sudo, lines)
			}
			if err := validateSSHD(client, sudo); err != nil {
				rollbackConfig(client, sudo, mode, w)
				return fmt.Errorf("sshd config validation failed after all variants: %w", err)
			}
		}
	}

	// Step 5 — reload (never restart: established sessions survive) and prove
	// the outcome from the outside: key in, password out.
	step("Step 5/5: reloading sshd and verifying from a fresh connection...")
	if err := reloadSSHD(client, sudo); err != nil {
		// Reload failed entirely — the drop-in may never take effect; remove
		// it so the on-disk config matches reality.
		rollbackConfig(client, sudo, mode, w)
		return fmt.Errorf("failed to reload sshd: %w", err)
	}
	time.Sleep(1 * time.Second)

	if err := verifyKeyLogin(params.Host, params.Port, params.User, params.PrivateKeyPEM, params.ExpectedHostKey); err != nil {
		// Key login must survive the reload. It didn't — restore the previous
		// config over the original connection while it is still alive.
		step("Key login BROKEN after reload — rolling back sshd config...")
		rollbackConfig(client, sudo, mode, w)
		_ = reloadSSHD(client, sudo)
		return fmt.Errorf("key login failed after reload — config rolled back: %w", err)
	}

	if params.TestPassword != "" {
		if pwErr := passwordLoginSucceeds(params.Host, params.Port, params.User, params.TestPassword, params.ExpectedHostKey); pwErr == nil {
			// Not a lockout risk — the config is correct but not yet active
			// (reload insufficient, e.g. non-systemd init). Report honestly:
			// the panel keeps the password so a rerun can finish the job.
			return fmt.Errorf("sshd still accepts password authentication after reload — hardening not applied; the key is installed, retry later")
		}
		step("Password login rejected — verified.")
	} else {
		step("Original auth was already key-based; password rejection not testable.")
	}

	// The canonical "completed" marker line is written by the caller AFTER it
	// persisted the hardened state, so the UI only reports success once the
	// record really changed.
	step("All checks passed — the server now accepts key-only authentication.")
	return nil
}

// dialSSH opens an SSH connection with the given credential, mirroring the
// installer's auth fallbacks and TOFU host-key handling.
func dialSSH(host string, port int, user, authType, secret, expectedHostKey string, w io.Writer) (*ssh.Client, error) {
	var authMethods []ssh.AuthMethod
	if authType == "key" {
		signer, err := ssh.ParsePrivateKey([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("failed to parse stored SSH private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	} else {
		authMethods = append(authMethods, ssh.Password(secret))
		authMethods = append(authMethods, ssh.KeyboardInteractive(
			func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = secret
				}
				return answers, nil
			},
		))
	}

	hostKeyCallback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		if expectedHostKey != "" {
			if fingerprint != expectedHostKey {
				return fmt.Errorf("SSH host key mismatch for %s: got %s, expected %s", hostname, fingerprint, expectedHostKey)
			}
			fmt.Fprintf(w, "[Dockpal Hardening] Host key verified: %s\n", fingerprint)
			return nil
		}
		fmt.Fprintf(w, "[Dockpal Hardening] Host key fingerprint: %s (not pinned — trust on first use)\n", fingerprint)
		return nil
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unable to authenticate") || strings.Contains(msg, "unexpected message type") {
			return nil, fmt.Errorf("SSH authentication with the stored credentials failed for %s:%d — was the password changed? (%v)", host, port, err)
		}
		return nil, fmt.Errorf("failed to connect via SSH to %s:%d: %w", host, port, err)
	}
	return client, nil
}

// verifyKeyLogin opens a fresh connection authenticating ONLY with the target
// private key and runs a trivial command — the proof that disabling passwords
// cannot lock the panel out.
func verifyKeyLogin(host string, port int, user, privateKeyPEM, expectedHostKey string) error {
	signer, err := ssh.ParsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return fmt.Errorf("failed to parse generated private key: %w", err)
	}
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyVerifier(expectedHostKey),
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return err
	}
	defer client.Close()
	return runCommand(client, "true", io.Discard)
}

// passwordLoginSucceeds reports whether the server STILL accepts the given
// password. It returns nil when the password works (bad — hardening not
// active) and an error when the server rejects it (good).
func passwordLoginSucceeds(host string, port int, user, password, expectedHostKey string) error {
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: hostKeyVerifier(expectedHostKey),
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("password rejected (expected): %w", err)
	}
	client.Close()
	return nil
}

func hostKeyVerifier(expectedHostKey string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if expectedHostKey != "" {
			if fp := ssh.FingerprintSHA256(key); fp != expectedHostKey {
				return fmt.Errorf("SSH host key mismatch: got %s, expected %s", fp, expectedHostKey)
			}
		}
		// Unpinned verification connections accept the key the first
		// connection already pinned in the operator's mind (TOFU within one
		// hardening run).
		return nil
	}
}

// configMode returns "dropin" when sshd loads /etc/ssh/sshd_config.d, else
// "block" (a managed section appended to sshd_config itself).
func configMode(client *ssh.Client, sudo bool) (string, error) {
	var out strings.Builder
	cmd := "grep -qs '^Include.*/etc/ssh/sshd_config.d' /etc/ssh/sshd_config && test -d /etc/ssh/sshd_config.d && echo dropin || echo block"
	if err := runCommandTo(client, cmd, &out); err != nil {
		return "", fmt.Errorf("failed to inspect sshd_config: %w", err)
	}
	if strings.Contains(out.String(), "dropin") {
		return "dropin", nil
	}
	return "block", nil
}

// detectOpenSSHVersion returns the remote OpenSSH version string ("8.9p1")
// or "" when undetectable.
func detectOpenSSHVersion(client *ssh.Client) string {
	var out strings.Builder
	if runCommandTo(client, "sshd -V 2>&1 || ssh -V 2>&1", &out) != nil {
		return ""
	}
	s := out.String()
	if i := strings.Index(s, "OpenSSH_"); i >= 0 {
		rest := s[i+len("OpenSSH_"):]
		if end := strings.IndexAny(rest, ",p "); end >= 0 {
			return rest[:end]
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// parseOpenSSHVersion parses "8.9" style versions into (major, minor).
func parseOpenSSHVersion(v string) (major, minor int, ok bool) {
	if v == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(v, ".", 2)
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor = 0
	if len(parts) == 2 {
		digits := []rune(parts[1])
		i := 0
		for i < len(digits) && digits[i] >= '0' && digits[i] <= '9' {
			i++
		}
		minor, _ = strconv.Atoi(string(digits[:i]))
	}
	return major, minor, true
}

// pickKbdKeyword chooses the sshd keyword for keyboard-interactive auth.
// OpenSSH 8.7 renamed ChallengeResponseAuthentication → KbdInteractive-
// Authentication (the old name stayed as an alias but newer releases drop
// deprecated names, and sshd refuses unknown keywords outright).
func pickKbdKeyword(version string) string {
	major, minor, ok := parseOpenSSHVersion(version)
	if ok && (major < 8 || (major == 8 && minor < 7)) {
		return "ChallengeResponseAuthentication"
	}
	return "KbdInteractiveAuthentication"
}

// hardeningConfigLines builds the config body. Root login stays possible but
// only with keys (prohibit-password) when the managed user is root.
func hardeningConfigLines(sudo bool, kbdKeyword string) []string {
	lines := []string{
		"# Managed by Dockpal SSH hardening — do not edit",
		"PasswordAuthentication no",
	}
	if kbdKeyword != "" {
		lines = append(lines, kbdKeyword+" no")
	}
	if sudo {
		// The panel logs in as this non-root user; root logins are a policy
		// decision left to the operator unless root is the managed user.
	} else {
		lines = append(lines, "PermitRootLogin prohibit-password")
	}
	return lines
}

// Drop-in naming matters: sshd keeps the FIRST obtained value per directive
// and reads sshd_config.d/*.conf in lexical order, so cloud images shipping
// e.g. 50-cloud-init.conf with "PasswordAuthentication yes" would beat a
// 99- file. The 00- prefix makes Dockpal's directives win. (The Include must
// also sit before any main-body directive — true for Ubuntu/Debian defaults.)
const dropInPath = "/etc/ssh/sshd_config.d/00-dockpal-hardening.conf"

// legacyDropInPath is the pre-fix name; it must be removed on re-runs so a
// stale file doesn't linger after an upgrade.
const legacyDropInPath = "/etc/ssh/sshd_config.d/99-dockpal-hardening.conf"

// writeDropIn replaces the Dockpal drop-in file with the given lines and
// removes the legacy 99- file from earlier builds.
func writeDropIn(client *ssh.Client, sudo bool, path string, lines []string) error {
	printfArgs := strings.Join(quoteAll(lines), " ")
	cmd := "rm -f " + legacyDropInPath + "; printf '%s\\n' " + printfArgs + " > " + path
	if sudo {
		cmd = "sudo sh -c " + shellQuote(cmd)
	}
	return runCommand(client, cmd, io.Discard)
}

const blockBegin = "# BEGIN DOCKPAL SSH HARDENING"
const blockEnd = "# END DOCKPAL SSH HARDENING"

// writeManagedBlock replaces the Dockpal section inside sshd_config itself
// (for distributions whose sshd_config has no Include directive). The block
// is PREPENDED, not appended: with no Include, first-value-wins means an
// appended block would lose to any earlier "PasswordAuthentication yes" in
// the main file, while a block at the top beats everything after it.
func writeManagedBlock(client *ssh.Client, sudo bool, lines []string) error {
	body := strings.Join(lines, "\n")
	printfArgs := strings.Join(quoteAll([]string{blockBegin, body, blockEnd}), " ")
	inner := "cp -n /etc/ssh/sshd_config /etc/ssh/sshd_config.dockpal-bak 2>/dev/null || true; " +
		"sed -i '/^" + blockBegin + "$/,/^" + blockEnd + "$/d' /etc/ssh/sshd_config; " +
		"{ printf '%s\\n' " + printfArgs + "; cat /etc/ssh/sshd_config; } > /etc/ssh/sshd_config.dockpal-tmp && " +
		"chmod --reference=/etc/ssh/sshd_config /etc/ssh/sshd_config.dockpal-tmp && " +
		"mv /etc/ssh/sshd_config.dockpal-tmp /etc/ssh/sshd_config"
	cmd := inner
	if sudo {
		cmd = "sudo sh -c " + shellQuote(inner)
	}
	return runCommand(client, cmd, io.Discard)
}

// rollbackConfig removes the hardening config so the server returns to its
// pre-run auth behavior (only ever called on failure paths).
func rollbackConfig(client *ssh.Client, sudo bool, mode string, w io.Writer) {
	fmt.Fprintln(w, "[Dockpal Hardening] Rolling back sshd config changes...")
	var cmd string
	if mode == "dropin" {
		cmd = "rm -f " + dropInPath + " " + legacyDropInPath
	} else {
		cmd = "sed -i '/^" + blockBegin + "$/,/^" + blockEnd + "$/d' /etc/ssh/sshd_config"
	}
	if sudo {
		cmd = "sudo sh -c " + shellQuote(cmd)
	}
	_ = runCommand(client, cmd, io.Discard)
}

// validateSSHD runs `sshd -t`, which exits non-zero on any config error.
func validateSSHD(client *ssh.Client, sudo bool) error {
	cmd := "SSHD=$(command -v sshd || echo /usr/sbin/sshd); if [ -x \"$SSHD\" ]; then"
	if sudo {
		cmd += " sudo \"$SSHD\" -t"
	} else {
		cmd += " \"$SSHD\" -t"
	}
	cmd += " || exit 1; else echo 'sshd binary not found' >&2; exit 1; fi"
	var errOut strings.Builder
	if err := runCommandTo(client, cmd, &errOut); err != nil {
		out := strings.TrimSpace(errOut.String())
		if out != "" {
			return fmt.Errorf("%s", out)
		}
		return err
	}
	return nil
}

// reloadSSHD asks sshd to re-read its config (SIGHUP semantics — existing
// sessions survive) across init systems.
func reloadSSHD(client *ssh.Client, sudo bool) error {
	s := ""
	if sudo {
		s = "sudo "
	}
	cmd := s + "systemctl reload sshd 2>/dev/null || " + s + "systemctl reload ssh 2>/dev/null || " +
		s + "/etc/init.d/ssh reload 2>/dev/null || " + s + "/etc/init.d/sshd reload 2>/dev/null"
	var errOut strings.Builder
	if err := runCommandTo(client, cmd, &errOut); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(errOut.String()))
	}
	return nil
}

// shellQuote single-quotes a string for POSIX sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// quoteAll returns each string single-quoted for use as a printf %s argument.
func quoteAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = shellQuote(l)
	}
	return out
}

// runCommandTo runs a command capturing combined output into out.
func runCommandTo(client *ssh.Client, cmd string, out *strings.Builder) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdout = out
	session.Stderr = out
	return session.Run(cmd)
}

// GenerateKeyPair creates a dedicated ed25519 keypair for one instance. The
// private key is returned in OpenSSH PEM format (what gets stored encrypted);
// the public key as a one-line authorized_keys entry.
func GenerateKeyPair(comment string) (publicKey, privateKeyPEM, fingerprint string, err error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to generate key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to build signer: %w", err)
	}
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to marshal private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(block)
	return pub, string(pemBytes), ssh.FingerprintSHA256(signer.PublicKey()), nil
}

// PublicKeyFromPrivate derives the authorized_keys line and fingerprint of an
// existing private key (used when the operator picks a saved key instead of a
// generated one).
func PublicKeyFromPrivate(privateKeyPEM string) (publicKey, fingerprint string, err error) {
	signer, err := ssh.ParsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return "", "", fmt.Errorf("failed to parse private key: %w", err)
	}
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	return pub, ssh.FingerprintSHA256(signer.PublicKey()), nil
}
