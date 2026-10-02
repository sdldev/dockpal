package ssh

import (
	"fmt"
	"io"
	"strings"

	cryptossh "golang.org/x/crypto/ssh"
)

// Server security management (Vito-parity): detect the EFFECTIVE sshd state
// (sshd -T, not config files) and toggle password auth / root login /
// fail2ban. All commands run over the panel's stored SSH credential.

// SecurityState is the detected security posture of one server.
type SecurityState struct {
	PasswordAuth string `json:"password_auth"` // "yes" | "no" | "unknown"
	RootLogin    string `json:"root_login"`    // "yes" | "no" | "prohibit-password" | "without-password" | "unknown"
	Fail2ban     string `json:"fail2ban"`      // "active" | "inactive" | "unknown"
}

// DetectSecurity connects with the given credential and reads the effective
// configuration. Fail-closed like Vito: anything unreadable reports
// "unknown", which the UI treats as NOT secured.
func DetectSecurity(host string, port int, user, authType, secret, expectedHostKey string, w io.Writer) (SecurityState, error) {
	state := SecurityState{PasswordAuth: "unknown", RootLogin: "unknown", Fail2ban: "unknown"}
	client, err := dialSSH(host, port, user, authType, secret, expectedHostKey, w)
	if err != nil {
		return state, err
	}
	defer client.Close()
	sudo := user != "root"

	// Effective sshd config — reflects reality regardless of which file or
	// drop-in set it. sshd -T needs root (host key readability).
	// $SSHD must expand in the CALLER's shell and only the binary path cross
	// into sudo: wrapping the whole line in `sudo sh -c` lost the variable
	// (root's shell has no SSHD set), so non-root servers always reported
	// unknown — exactly what happened on vps-media (user ubuntu).
	var out strings.Builder
	prefix := ""
	if sudo {
		prefix = "sudo "
	}
	cmd := "SSHD=$(command -v sshd || echo /usr/sbin/sshd); " +
		"if [ -x \"$SSHD\" ]; then " + prefix + "\"$SSHD\" -T 2>/dev/null; fi"
	if err := runCommandTo(client, cmd, &out); err == nil {
		state.PasswordAuth = parseSSHDValue(out.String(), "passwordauthentication")
		state.RootLogin = parseSSHDValue(out.String(), "permitrootlogin")
	}
	if state.PasswordAuth == "" {
		state.PasswordAuth = "unknown"
	}
	if state.RootLogin == "" {
		state.RootLogin = "unknown"
	}

	// fail2ban: active only when the daemon process is actually running.
	// A textual `service status | grep running` false-positives on
	// "fail2ban is NOT running" — check the process instead.
	var f2b strings.Builder
	f2bCmd := "pgrep -x fail2ban-server >/dev/null && echo active || echo inactive"
	if err := runCommandTo(client, f2bCmd, &f2b); err == nil {
		v := strings.TrimSpace(f2b.String())
		if v == "active" {
			state.Fail2ban = "active"
		} else if v == "inactive" {
			state.Fail2ban = "inactive"
		}
	}
	return state, nil
}

// parseSSHDValue extracts the value of `key value` from sshd -T output.
func parseSSHDValue(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == key {
			// sshd -T prints the canonical alias "without-password" for
			// prohibit-password — normalize so the UI sees one name.
			if key == "permitrootlogin" && fields[1] == "without-password" {
				return "prohibit-password"
			}
			return fields[1]
		}
	}
	return ""
}

// SecurityUpdate describes the DESIRED end state of the three controls plus
// the keys to keep on the server (the UI's unified toggle model). Only
// differences vs the current state are applied, in one sshd rewrite + one
// reload where possible.
type SecurityUpdate struct {
	PasswordAuth bool // true = passwords allowed, false = disabled (key-only)
	RootLogin    bool // true = unmanaged (server default), false = PermitRootLogin no
	Fail2ban     bool // true = installed and running, false = stopped
	// PanelKeyPEM is the private key the panel authenticates with after the
	// run (its public half MUST be in PublicKeys, or the panel locks itself
	// out). Empty when the panel already connects by key — then PublicKeys
	// still get installed but nothing is persisted.
	PanelKeyPEM string
	// PublicKeys are the authorized_keys lines to ensure present on the
	// server (panel key + the operator's own keys). Additive + idempotent.
	PublicKeys []string
	// TestPassword, when disabling password auth and the panel knows the old
	// password, is used to verify rejection from the outside post-reload.
	TestPassword string
}

// ApplySecurity connects and applies one control change, streaming progress.
// password_auth: enabled=false → disable via drop-in (harden); enabled=true →
// remove the Dockpal drop-in so the server's own config decides again.
// root_login: enabled=false → PermitRootLogin no; enabled=true → stop
// managing it (remove our line, server default applies).
// fail2ban: install+enable, or stop+disable (package stays installed).
func ApplySecurity(host string, port int, user, authType, secret, expectedHostKey string, update SecurityUpdate, w io.Writer) error {
	if port == 0 {
		port = 22
	}
	if user == "" {
		user = "root"
	}
	sudo := user != "root"
	step := func(format string, args ...interface{}) {
		fmt.Fprintf(w, "[Dockpal Security] "+format+"\n", args...)
	}

	step("Connecting to %s:%d as %s...", host, port, user)
	client, err := dialSSH(host, port, user, authType, secret, expectedHostKey, w)
	if err != nil {
		return err
	}
	defer client.Close()
	step("Connected.")

	// Establish what the server looks like NOW so we only change what the
	// operator actually toggled.
	current, err := detectEffective(client, sudo)
	if err != nil {
		return fmt.Errorf("failed to read the current security state: %w", err)
	}
	step("Current: password_auth=%s root_login=%s fail2ban=%s", current.PasswordAuth, current.RootLogin, current.Fail2ban)
	mode, err := configMode(client, sudo)
	if err != nil {
		return err
	}

	// --- keys first (additive + idempotent), then prove they work ---
	if len(update.PublicKeys) > 0 {
		step("Ensuring %d public key(s) in ~/.ssh/authorized_keys...", len(update.PublicKeys))
		if err := runCommand(client, authorizedKeysInstallCommand(update.PublicKeys), io.Discard); err != nil {
			return fmt.Errorf("failed to install public keys: %w", err)
		}
		if update.PanelKeyPEM != "" {
			if err := verifyKeyLogin(host, port, user, update.PanelKeyPEM, expectedHostKey); err != nil {
				return fmt.Errorf("panel key login verification failed — server unchanged (except authorized_keys, which is safe): %w", err)
			}
			step("Panel key login verified.")
		}
	}

	// --- sshd controls (password auth + root login share one drop-in) ---
	currentPWOff := current.PasswordAuth == "no"
	wantPWOff := !update.PasswordAuth
	// Root: current "no" maps to managed-off; anything else (yes / prohibit /
	// unknown) is treated as not-managed-by-us.
	currentRootOff := current.RootLogin == "no"
	wantRootOff := !update.RootLogin
	if currentPWOff != wantPWOff || currentRootOff != wantRootOff {
		// Off → manage with PermitRootLogin no; on → unmanage ("" removes
		// the line so the server's own default applies).
		rootLine := "no"
		if !wantRootOff {
			rootLine = ""
		}
		lines := securityDropInLines(wantPWOff, rootLine)
		if err := applyDropInState(client, sudo, mode, lines, w); err != nil {
			return err
		}
		if wantPWOff {
			// Prove the outcome end-to-end when we know the old password —
			// same rule as hardening: no claim without outside evidence.
			if update.TestPassword != "" {
				if pwErr := passwordLoginSucceeds(host, port, user, update.TestPassword, expectedHostKey); pwErr == nil {
					return fmt.Errorf("sshd still accepts password authentication after reload — not applied; retry later")
				}
				step("Password login rejected — verified.")
			}
		}
	} else {
		step("sshd already in the desired state — no config change needed.")
	}

	// --- fail2ban ---
	if update.Fail2ban && current.Fail2ban != "active" {
		if err := enableFail2ban(client, sudo, w); err != nil {
			return err
		}
	} else if !update.Fail2ban && current.Fail2ban == "active" {
		if err := disableFail2ban(client, sudo, w); err != nil {
			return err
		}
	} else {
		step("fail2ban already in the desired state.")
	}

	state, derr := detectEffective(client, sudo)
	if derr == nil {
		step("Detected now: password_auth=%s root_login=%s fail2ban=%s", state.PasswordAuth, state.RootLogin, state.Fail2ban)
	}
	step("Update completed successfully.")
	return nil
}

// detectEffective reads the effective state over an established connection
// (same commands as DetectSecurity, no new dial).
func detectEffective(client *cryptossh.Client, sudo bool) (SecurityState, error) {
	state := SecurityState{PasswordAuth: "unknown", RootLogin: "unknown", Fail2ban: "unknown"}
	// $SSHD must expand in the CALLER's shell and only the binary path cross
	// into sudo: wrapping the whole line in `sudo sh -c` lost the variable
	// (root's shell has no SSHD set), so non-root servers always reported
	// unknown — exactly what happened on vps-media (user ubuntu).
	var out strings.Builder
	prefix := ""
	if sudo {
		prefix = "sudo "
	}
	cmd := "SSHD=$(command -v sshd || echo /usr/sbin/sshd); " +
		"if [ -x \"$SSHD\" ]; then " + prefix + "\"$SSHD\" -T 2>/dev/null; fi"
	if err := runCommandTo(client, cmd, &out); err == nil {
		state.PasswordAuth = parseSSHDValue(out.String(), "passwordauthentication")
		state.RootLogin = parseSSHDValue(out.String(), "permitrootlogin")
	}
	if state.PasswordAuth == "" {
		state.PasswordAuth = "unknown"
	}
	if state.RootLogin == "" {
		state.RootLogin = "unknown"
	}
	var f2b strings.Builder
	if err := runCommandTo(client, "pgrep -x fail2ban-server >/dev/null && echo active || echo inactive", &f2b); err == nil {
		if v := strings.TrimSpace(f2b.String()); v == "active" {
			state.Fail2ban = "active"
		} else {
			state.Fail2ban = "inactive"
		}
	}
	return state, nil
}

// securityDropInLines builds the drop-in body from the desired control state.
// passwordAuthDisabled → PasswordAuthentication no + keyboard-interactive no.
// rootLogin: "no" → PermitRootLogin no; "" → unmanaged (no line).
func securityDropInLines(passwordAuthDisabled bool, rootLogin string) []string {
	var lines []string
	if passwordAuthDisabled {
		lines = append(lines,
			"# Managed by Dockpal SSH hardening — do not edit",
			"PasswordAuthentication no",
			pickKbdKeyword("")+" no",
		)
	}
	if rootLogin != "" {
		if len(lines) == 0 {
			lines = append(lines, "# Managed by Dockpal SSH hardening — do not edit")
		}
		lines = append(lines, "PermitRootLogin "+rootLogin)
	}
	return lines
}

// applyDropInState writes the desired drop-in (or removes it when the desired
// state manages nothing), validates with sshd -t, checks the effective config
// for password auth when relevant, and reloads. Keyword variants are retried
// exactly like hardening for old OpenSSH releases.
func applyDropInState(client *cryptossh.Client, sudo bool, mode string, lines []string, w io.Writer) error {
	if len(lines) == 0 {
		fmt.Fprintln(w, "[Dockpal Security] Removing the Dockpal sshd drop-in (server defaults apply)...")
		if err := removeDropIn(client, sudo, mode); err != nil {
			return fmt.Errorf("failed to remove drop-in: %w", err)
		}
	} else {
		if mode == "dropin" {
			if err := writeDropIn(client, sudo, dropInPath, lines); err != nil {
				return fmt.Errorf("failed to write drop-in: %w", err)
			}
		} else {
			if err := writeManagedBlock(client, sudo, lines); err != nil {
				return fmt.Errorf("failed to write managed block: %w", err)
			}
		}
	}
	if err := validateSSHD(client, sudo); err != nil {
		// Retry with the legacy keyboard-interactive keyword before failing.
		fixed := make([]string, len(lines))
		for i, l := range lines {
			fixed[i] = strings.Replace(l, "KbdInteractiveAuthentication", "ChallengeResponseAuthentication", 1)
		}
		if mode == "dropin" {
			_ = writeDropIn(client, sudo, dropInPath, fixed)
		} else {
			_ = writeManagedBlock(client, sudo, fixed)
		}
		if err2 := validateSSHD(client, sudo); err2 != nil {
			rollbackConfig(client, sudo, mode, w)
			return fmt.Errorf("sshd config validation failed: %w", err2)
		}
	}
	if err := reloadSSHD(client, sudo); err != nil {
		return fmt.Errorf("failed to reload sshd: %w", err)
	}
	return nil
}

func removeDropIn(client *cryptossh.Client, sudo bool, mode string) error {
	var cmd string
	if mode == "dropin" {
		cmd = "rm -f " + dropInPath + " " + legacyDropInPath
	} else {
		cmd = "sed -i '/^" + blockBegin + "$/,/^" + blockEnd + "$/d' /etc/ssh/sshd_config"
	}
	if sudo {
		cmd = "sudo sh -c " + shellQuote(cmd)
	}
	return runCommand(client, cmd, io.Discard)
}

const fail2banJailPath = "/etc/fail2ban/jail.d/dockpal-sshd.local"

// enableFail2ban installs fail2ban (apt/dnf/yum), writes an sshd jail and
// enables the service. The jail uses the systemd backend when systemd exists
// — Debian 12/Ubuntu 22.04+ often lack /var/log/auth.log, where the default
// backend fails.
func enableFail2ban(client *cryptossh.Client, sudo bool, w io.Writer) error {
	s := ""
	if sudo {
		s = "sudo "
	}
	fmt.Fprintln(w, "[Dockpal Security] Installing fail2ban (apt/dnf/yum)...")
	// Real if/elif/else — a `A && B || C && D` chain is NOT an else-if: after
	// a successful apt install the `&&` fell through into `dnf install`,
	// which does not exist on Ubuntu → exit 1 → the whole job failed even
	// though fail2ban had just installed fine (seen live on vps-media).
	install := s + "sh -c " + shellQuote(
		"if command -v apt-get >/dev/null 2>&1; then apt-get install -y fail2ban; "+
			"elif command -v dnf >/dev/null 2>&1; then dnf install -y fail2ban; "+
			"elif command -v yum >/dev/null 2>&1; then yum install -y fail2ban; "+
			"else echo 'no supported package manager found (apt/dnf/yum)' >&2; exit 1; fi")
	if err := runCommand(client, install, w); err != nil {
		return fmt.Errorf("failed to install fail2ban: %w", err)
	}
	fmt.Fprintln(w, "[Dockpal Security] Writing the Dockpal sshd jail...")
	// Backend selection decides whether fail2ban can even start:
	//   - systemd (read journald) when journald answers — the standard case
	//     on real servers (Ubuntu 20.04+/Debian 11+ with rsyslog often gone);
	//   - polling on /var/log/auth.log otherwise (containers without
	//     systemd): backend "auto" still validates the DEFAULT logpath at
	//     startup and dies with "Have not found any log file for sshd jail"
	//     when /var/log/auth.log does not exist — forcing the log file here
	//     is what keeps non-systemd hosts workable when rsyslog runs.
	backend := "polling\nlogpath = /var/log/auth.log"
	var sysd strings.Builder
	if runCommandTo(client, "journalctl -n 1 -q --no-pager >/dev/null 2>&1 && echo yes", &sysd) == nil &&
		strings.Contains(sysd.String(), "yes") {
		backend = "systemd"
	}
	jail := "[sshd]\nenabled = true\nbackend = " + backend + "\nmaxretry = 5\nbantime = 10m\nfindtime = 10m"
	printfArgs := strings.Join(quoteAll(strings.Split(jail, "\n")), " ")
	cmd := "mkdir -p /etc/fail2ban/jail.d && printf '%s\\n' " + printfArgs + " > " + fail2banJailPath
	if sudo {
		cmd = "sudo sh -c " + shellQuote(cmd)
	}
	if err := runCommand(client, cmd, io.Discard); err != nil {
		return fmt.Errorf("failed to write the fail2ban jail: %w", err)
	}
	fmt.Fprintln(w, "[Dockpal Security] Enabling fail2ban...")
	enable := s + "systemctl enable --now fail2ban 2>/dev/null || " + s + "service fail2ban start"
	if err := runCommand(client, enable, w); err != nil {
		// The daemon may already run from an earlier enable — verify below.
		fmt.Fprintln(w, "[Dockpal Security] start command failed, verifying the process anyway...")
	}
	var st strings.Builder
	if err := runCommandTo(client, "pgrep -x fail2ban-server >/dev/null && echo active || echo inactive", &st); err == nil &&
		strings.TrimSpace(st.String()) == "active" {
		fmt.Fprintln(w, "[Dockpal Security] fail2ban is active (sshd jail: maxretry=5, bantime=10m).")
		return nil
	}
	return fmt.Errorf("fail2ban did not become active — check the service logs on the server")
}

// disableFail2ban stops and disables the service (package stays installed).
func disableFail2ban(client *cryptossh.Client, sudo bool, w io.Writer) error {
	s := ""
	if sudo {
		s = "sudo "
	}
	fmt.Fprintln(w, "[Dockpal Security] Disabling fail2ban...")
	// `service stop` exits 1 when the daemon is already down (non-systemd
	// hosts) — treat stopping as best-effort and verify by process.
	cmd := s + "systemctl disable --now fail2ban 2>/dev/null; " + s + "service fail2ban stop >/dev/null 2>&1; exit 0"
	if err := runCommand(client, cmd, w); err != nil {
		return fmt.Errorf("failed to run the disable commands: %w", err)
	}
	var st strings.Builder
	if err := runCommandTo(client, "pgrep -x fail2ban-server >/dev/null && echo active || echo inactive", &st); err == nil &&
		strings.TrimSpace(st.String()) == "active" {
		return fmt.Errorf("fail2ban is still running after the disable attempt")
	}
	fmt.Fprintln(w, "[Dockpal Security] fail2ban stopped.")
	return nil
}
