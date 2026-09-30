package security

import (
	"testing"
)

func TestIsRemoteHost(t *testing.T) {
	// This test validates the function runs without error.
	// The result depends on the actual host network configuration.
	remote, err := IsRemoteHost()
	if err != nil {
		t.Fatalf("IsRemoteHost() returned error: %v", err)
	}

	// On a machine with only loopback, should return false.
	// On a machine with real network interfaces, should return true.
	t.Logf("IsRemoteHost() = %v", remote)
}

func TestRequireInitialPassword_LocalhostOK(t *testing.T) {
	// When the host has no remote interfaces (or we can't determine),
	// RequireInitialPassword should not block.
	// This is a best-effort test since it depends on the test environment.
	remote, err := IsRemoteHost()
	if err != nil {
		t.Skipf("Cannot determine remote status: %v", err)
	}

	if remote {
		t.Skip("Running on remote host — this test requires localhost")
	}

	// Clear env var to simulate fresh install
	t.Setenv("DOCKPAL_INITIAL_ADMIN_PASSWORD", "")

	err = RequireInitialPassword()
	if err != nil {
		t.Errorf("RequireInitialPassword() on localhost should not error, got: %v", err)
	}
}

func TestRequireInitialPassword_RemoteRequiresPassword(t *testing.T) {
	remote, err := IsRemoteHost()
	if err != nil {
		t.Skipf("Cannot determine remote status: %v", err)
	}

	if !remote {
		t.Skip("Running on localhost — this test requires a remote host")
	}

	// Clear env var — should fail
	t.Setenv("DOCKPAL_INITIAL_ADMIN_PASSWORD", "")
	err = RequireInitialPassword()
	if err == nil {
		t.Error("RequireInitialPassword() on remote host without password should error")
	}

	// Set env var — should pass
	t.Setenv("DOCKPAL_INITIAL_ADMIN_PASSWORD", "secure-password-123")
	err = RequireInitialPassword()
	if err != nil {
		t.Errorf("RequireInitialPassword() with password set should not error, got: %v", err)
	}
}

func TestWarnIfRemote(t *testing.T) {
	// Just verify it doesn't panic
	WarnIfRemote(false)
	WarnIfRemote(true)
}
