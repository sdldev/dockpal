// Package security provides security-related utilities for dockpal.
package security

import (
	"fmt"
	"net"
	"os"
)

// IsRemoteHost checks if the current host appears to be remotely accessible.
// It returns true when the machine has a non-loopback, non-link-local IP
// address assigned to one of its network interfaces. This is a best-effort
// heuristic — it cannot detect NAT or firewall rules, so a "false" result
// should not be treated as a guarantee of local-only access.
func IsRemoteHost() (bool, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false, fmt.Errorf("failed to list network interfaces: %w", err)
	}

	for _, iface := range ifaces {
		// Skip down interfaces and loopback
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}

			// Skip loopback, link-local, and unspecified addresses
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				continue
			}

			// Found a real IP — this host is remotely reachable
			return true, nil
		}
	}

	return false, nil
}

// RequireInitialPassword enforces that DOCKPAL_INITIAL_ADMIN_PASSWORD is set
// when dockpal is being installed on a remote host. On localhost, an
// auto-generated password is acceptable. On remote hosts, a generated password
// could be exposed in logs or terminal history before the operator has a
// chance to change it.
//
// Returns nil when:
//   - The host appears to be local (no remote interfaces), or
//   - The host is remote AND DOCKPAL_INITIAL_ADMIN_PASSWORD is set.
//
// Returns an error when the host is remote AND the env var is empty.
func RequireInitialPassword() error {
	isRemote, err := IsRemoteHost()
	if err != nil {
		fmt.Fprintln(os.Stderr, "⚠️  WARNING: Could not determine if running on remote host:", err)
		fmt.Fprintln(os.Stderr, "Please set DOCKPAL_INITIAL_ADMIN_PASSWORD:")
		fmt.Fprintln(os.Stderr, "  sudo systemctl set-environment DOCKPAL_INITIAL_ADMIN_PASSWORD=your-secure-password")
		return nil // Don't block on uncertainty
	}

	if !isRemote {
		return nil // localhost is safe
	}

	passwordEnv := os.Getenv("DOCKPAL_INITIAL_ADMIN_PASSWORD")
	if passwordEnv == "" {
		return fmt.Errorf(
			"remote installation requires DOCKPAL_INITIAL_ADMIN_PASSWORD\n" +
				"To fix:\n" +
				"  sudo systemctl set-environment DOCKPAL_INITIAL_ADMIN_PASSWORD=your-secure-password\n" +
				"  sudo systemctl daemon-reload\n" +
				"  sudo systemctl restart dockpal\n" +
				"Or set it in your environment before running:\n" +
				"  export DOCKPAL_INITIAL_ADMIN_PASSWORD=your-secure-password")
	}

	return nil
}

// WarnIfRemote prints a security warning when the server is about to start
// on a remote host without TLS enabled.
func WarnIfRemote(tls bool) {
	isRemote, err := IsRemoteHost()
	if err != nil || !isRemote {
		return
	}

	if !tls {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "⚠️  SECURITY WARNING: Running on a remote host without TLS.")
		fmt.Fprintln(os.Stderr, "   All traffic (including credentials) is sent in plaintext.")
		fmt.Fprintln(os.Stderr, "   Consider enabling TLS with DOCKPAL_TLS=true or use a reverse proxy.")
		fmt.Fprintln(os.Stderr, "")
	}
}
