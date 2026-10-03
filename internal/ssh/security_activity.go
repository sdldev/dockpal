package ssh

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	cryptossh "golang.org/x/crypto/ssh"
)

// Security activity monitoring (fail2ban + firewall): a bounded read-only
// snapshot over the panel's stored SSH credential. Every remote command is
// wrapped in `timeout`, and the whole fetch is fenced by an overall watchdog
// — the detail page polls this on demand, so a slow or broken server must
// never pin a request indefinitely.

const (
	// activityOverallTimeout fences the whole fetch (connect included). The
	// dial timeout is 15s and the remote commands carry their own timeouts
	// (8–10s); the watchdog closes the connection so the probe goroutine
	// cannot outlive the request by more than a moment.
	activityOverallTimeout = 25 * time.Second
	f2bStatusTimeout       = 8
	fwStatusTimeout        = 8
	blockLogTimeout        = 10
	eventLogTimeout        = 10

	blockLinesFetchCap     = 200 // kernel lines fetched (journalctl tail)
	blockLinesDisplayCap   = 50  // lines returned to the UI
	fwStatusLineCap        = 40  // `ufw status` rule table lines kept
	eventLinesCap          = 50  // fail2ban event lines returned
)

// SecurityActivity is the per-server snapshot returned by the monitoring
// endpoint. All fields fail soft: unreadable pieces stay "unknown"/zero so
// one broken subsystem never blanks the whole card.
type SecurityActivity struct {
	Fail2ban  Fail2banActivity `json:"fail2ban"`
	Firewall  FirewallActivity `json:"firewall"`
	CheckedAt int64            `json:"checked_at"`
}

// Fail2banActivity aggregates the sshd jail counters plus the tail of the
// fail2ban event log.
type Fail2banActivity struct {
	Active          string   `json:"active"` // "active" | "inactive" | "unknown"
	CurrentlyBanned int      `json:"currently_banned"`
	TotalBanned     int      `json:"total_banned"`
	TotalFailed     int      `json:"total_failed"`
	BannedIPs       []string `json:"banned_ips"`
	// Events are the last fail2ban log lines (journald preferred, the
	// fail2ban.log file as fallback).
	Events []string `json:"events"`
	// ReadError is set when the daemon runs but `fail2ban-client` output
	// could not be read/parsed.
	ReadError string `json:"read_error,omitempty"`
}

// FirewallActivity reports the firewall manager found on the server and a
// best-effort tail of its block log.
type FirewallActivity struct {
	// Tool is "ufw" | "firewalld" | "none" | "unknown".
	Tool   string `json:"tool"`
	Active string `json:"active"` // "active" | "inactive" | "unknown"
	// Status is the trimmed `ufw status` / firewalld state text (capped).
	Status string `json:"status"`
	// Blocks24h counts kernel lines tagged [UFW …] in the last 24h
	// (journalctl), capped at blockLinesFetchCap. The /var/log/ufw.log
	// fallback (hosts without journald) may include older lines.
	Blocks24h int      `json:"blocks_24h"`
	BlockLines []string `json:"block_lines"`
	ReadError string   `json:"read_error,omitempty"`
}

// FetchSecurityActivity dials the server once and collects the snapshot.
// Failure of an individual probe leaves that section "unknown"/zero; only a
// dial failure or the overall watchdog returns an error.
func FetchSecurityActivity(host string, port int, user, authType, secret, expectedHostKey string, w io.Writer) (SecurityActivity, error) {
	if port == 0 {
		port = 22
	}
	if user == "" {
		user = "root"
	}
	client, err := dialSSH(host, port, user, authType, secret, expectedHostKey, w)
	if err != nil {
		return defaultActivity(), err
	}
	defer client.Close()

	type result struct {
		act SecurityActivity
	}
	done := make(chan result, 1)
	go func() {
		act := defaultActivity()
		collectActivity(client, user != "root", &act)
		done <- result{act: act}
	}()
	select {
	case r := <-done:
		r.act.CheckedAt = time.Now().Unix()
		return r.act, nil
	case <-time.After(activityOverallTimeout):
		// Closing the client unblocks the probe goroutine (its sessions die
		// with the connection); it then sends into the buffered channel and
		// exits — nothing leaks past the remote `timeout` wrappers.
		client.Close()
		return defaultActivity(), fmt.Errorf("security activity fetch timed out after %s", activityOverallTimeout)
	}
}

// UnbanIP lifts one fail2ban ban over SSH. The IP is re-validated here so
// the remote command can never receive anything but a canonical literal.
func UnbanIP(host string, port int, user, authType, secret, expectedHostKey, rawIP string, w io.Writer) error {
	parsed := net.ParseIP(strings.TrimSpace(rawIP))
	if parsed == nil {
		return fmt.Errorf("%q is not a valid IP address", rawIP)
	}
	if port == 0 {
		port = 22
	}
	if user == "" {
		user = "root"
	}
	client, err := dialSSH(host, port, user, authType, secret, expectedHostKey, w)
	if err != nil {
		return err
	}
	defer client.Close()

	var out strings.Builder
	cmd := sudoWrap(user != "root",
		fmt.Sprintf("timeout 8 fail2ban-client set sshd unbanip %s 2>&1", shellQuote(parsed.String())))
	if err := runCommandTo(client, cmd, &out); err != nil {
		// Real failures (unknown jail, rejected input) exit non-zero with a
		// message on the same stream — surface it instead of the bare error.
		if msg := strings.TrimSpace(out.String()); msg != "" {
			return fmt.Errorf("unban failed: %s", msg)
		}
		return fmt.Errorf("unban command failed: %w", err)
	}
	// Exit 0 is success. The output is the count of removed bans (fail2ban
	// 0.11+ answers "1"), the echoed IP (older releases), or empty — every
	// variant already means the IP is no longer banned, so nothing to parse.
	return nil
}

// defaultActivity seeds every soft-failing field with its unknown value.
func defaultActivity() SecurityActivity {
	return SecurityActivity{
		Fail2ban: Fail2banActivity{
			Active:    "unknown",
			BannedIPs: []string{},
			Events:    []string{},
		},
		Firewall: FirewallActivity{
			Tool:       "unknown",
			Active:     "unknown",
			BlockLines: []string{},
		},
	}
}

// collectActivity runs the read-only probes over an established connection.
// Sequential on purpose: one session at a time keeps the load on the target
// server trivial, and each command is bounded by a remote `timeout`.
func collectActivity(client *cryptossh.Client, sudo bool, act *SecurityActivity) {
	// 1. fail2ban daemon — process check, same as DetectSecurity (a textual
	// service status false-positives on "fail2ban is NOT running").
	var f2b strings.Builder
	if err := runCommandTo(client, "pgrep -x fail2ban-server >/dev/null && echo active || echo inactive", &f2b); err == nil {
		act.Fail2ban.Active = normalizeActive(f2b.String())
	}

	// 2. sshd jail counters — only meaningful while the daemon runs.
	if act.Fail2ban.Active == "active" {
		var st strings.Builder
		cmd := sudoWrap(sudo, fmt.Sprintf("timeout %d fail2ban-client status sshd 2>/dev/null; true", f2bStatusTimeout))
		if err := runCommandTo(client, cmd, &st); err == nil {
			if cur, total, failed, ips, ok := parseFail2banStatus(st.String()); ok {
				act.Fail2ban.CurrentlyBanned = cur
				act.Fail2ban.TotalBanned = total
				act.Fail2ban.TotalFailed = failed
				act.Fail2ban.BannedIPs = ips
			} else {
				act.Fail2ban.ReadError = "the sshd jail status could not be read"
			}
		} else {
			act.Fail2ban.ReadError = "the sshd jail status could not be read"
		}

		// 3. fail2ban event tail: journald preferred, fail2ban.log fallback.
		var ev strings.Builder
		eventsScript := "j=$(timeout " + strconv.Itoa(eventLogTimeout) + " journalctl -u fail2ban -n " +
			strconv.Itoa(eventLinesCap) + " --no-pager -o short-iso 2>/dev/null); " +
			"if [ -n \"$j\" ]; then printf '%s\\n' \"$j\"; else timeout 5 tail -n " +
			strconv.Itoa(eventLinesCap) + " /var/log/fail2ban.log 2>/dev/null; fi; true"
		if err := runCommandTo(client, sudoWrap(sudo, eventsScript), &ev); err == nil {
			act.Fail2ban.Events = capLines(nonEmptyLines(ev.String()), eventLinesCap)
		}
	}

	// 4. firewall manager: ufw first (Ubuntu), firewalld second (RHEL family).
	var fw strings.Builder
	fwScript := "if command -v ufw >/dev/null 2>&1; then echo 'tool: ufw'; timeout " + strconv.Itoa(fwStatusTimeout) + " ufw status 2>/dev/null; " +
		"elif command -v firewall-cmd >/dev/null 2>&1; then echo 'tool: firewalld'; timeout " + strconv.Itoa(fwStatusTimeout) + " firewall-cmd --state 2>/dev/null; " +
		"else echo 'tool: none'; fi; true"
	if err := runCommandTo(client, sudoWrap(sudo, fwScript), &fw); err != nil {
		act.Firewall.ReadError = "the firewall could not be detected"
		return
	}
	tool, status, ok := parseFirewallStatus(fw.String())
	if !ok {
		act.Firewall.ReadError = "the firewall could not be detected"
		return
	}
	act.Firewall.Tool = tool
	act.Firewall.Status = status
	switch tool {
	case "ufw":
		act.Firewall.Active = ufwActive(status)
	case "firewalld":
		if strings.TrimSpace(status) == "running" {
			act.Firewall.Active = "active"
		} else {
			act.Firewall.Active = "inactive"
		}
	case "none":
		act.Firewall.Active = "inactive"
	}

	// 5. blocked-packet tail — only worth reading when ufw owns the firewall.
	if tool == "ufw" {
		var bl strings.Builder
		blocksScript := "j=$(timeout " + strconv.Itoa(blockLogTimeout) + " journalctl -k --since '-24 hours' --no-pager 2>/dev/null | grep -F '[UFW' | tail -n " +
			strconv.Itoa(blockLinesFetchCap) + "); " +
			"if [ -n \"$j\" ]; then printf '%s\\n' \"$j\"; else timeout 5 tail -n " + strconv.Itoa(blockLinesFetchCap) +
			" /var/log/ufw.log 2>/dev/null | grep -F '[UFW'; fi; true"
		if err := runCommandTo(client, sudoWrap(sudo, blocksScript), &bl); err == nil {
			lines := nonEmptyLines(bl.String())
			if lines == nil {
				lines = []string{}
			}
			act.Firewall.Blocks24h = len(lines)
			if len(lines) > blockLinesDisplayCap {
				lines = lines[len(lines)-blockLinesDisplayCap:]
			}
			act.Firewall.BlockLines = lines
		} else {
			act.Firewall.ReadError = "the firewall block log could not be read"
		}
	}
}

// sudoWrap runs script via sh -c, elevating with sudo for non-root users —
// the same NOPASSWD assumption as the hardening flow. sh -c on both paths
// keeps the remote quoting identical with and without elevation.
func sudoWrap(sudo bool, script string) string {
	if sudo {
		return "sudo sh -c " + shellQuote(script)
	}
	return "sh -c " + shellQuote(script)
}

// normalizeActive maps pgrep output onto the tri-state vocabulary.
func normalizeActive(out string) string {
	switch strings.TrimSpace(out) {
	case "active":
		return "active"
	case "inactive":
		return "inactive"
	default:
		return "unknown"
	}
}

// parseFail2banStatus reads `fail2ban-client status sshd` output:
//
//	Status for the jail: sshd
//	|- Filter
//	|  |- Currently failed:	1
//	|  |- Total failed:	7
//	|  `- Journal matches:	0
//	`- Actions
//	   |- Currently banned:	2
//	   |- Total banned:	8
//	   `- Banned IP list:	1.2.3.4 5.6.7.8
//
// The tree decorations vary with the value separator (tab or spaces), so the
// labels are matched by substring. ok reports whether the jail block was
// recognized at all.
func parseFail2banStatus(out string) (currently, total, failed int, ips []string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		if v, found := afterLabel(line, "Currently failed:"); found {
			failed = atoiDefault(v)
			ok = true
		}
		if v, found := afterLabel(line, "Currently banned:"); found {
			currently = atoiDefault(v)
			ok = true
		}
		if v, found := afterLabel(line, "Total banned:"); found {
			total = atoiDefault(v)
			ok = true
		}
		if v, found := afterLabel(line, "Total failed:"); found {
			failed = atoiDefault(v)
			ok = true
		}
		if v, found := afterLabel(line, "Banned IP list:"); found {
			ips = strings.Fields(v)
			if ips == nil {
				ips = []string{}
			}
			ok = true
		}
	}
	if currently == 0 && len(ips) > 0 {
		currently = len(ips)
	}
	return currently, total, failed, ips, ok
}

// afterLabel returns the trimmed remainder of line after the first occurrence
// of label ("|- Currently banned:\t2" with label "Currently banned:" → "2").
func afterLabel(line, label string) (string, bool) {
	idx := strings.Index(line, label)
	if idx < 0 {
		return "", false
	}
	return strings.TrimSpace(line[idx+len(label):]), true
}

func atoiDefault(s string) int {
	n, err := strconv.Atoi(strings.Fields(s)[0])
	if err != nil {
		return 0
	}
	return n
}

// parseFirewallStatus splits the detection script output: the first line is
// "tool: <name>", the rest is the status text (`ufw status` rule table or the
// single-word firewalld state). The status text is capped at fwStatusLineCap
// lines so rule-heavy servers cannot bloat the payload.
func parseFirewallStatus(out string) (tool, status string, ok bool) {
	lines := nonEmptyLines(out)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "tool:") {
		return "", "", false
	}
	tool = strings.TrimSpace(strings.TrimPrefix(lines[0], "tool:"))
	rest := lines[1:]
	if len(rest) > fwStatusLineCap {
		rest = rest[:fwStatusLineCap]
	}
	return tool, strings.Join(rest, "\n"), true
}

// ufwActive reads the "Status: active|inactive" header of `ufw status`.
func ufwActive(status string) string {
	first := ""
	if lines := strings.Split(status, "\n"); len(lines) > 0 {
		first = strings.TrimSpace(lines[0])
	}
	switch first {
	case "Status: active":
		return "active"
	case "Status: inactive":
		return "inactive"
	default:
		return "unknown"
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// capLines keeps at most n lines, preferring the tail (logs read bottom-up).
func capLines(lines []string, n int) []string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
