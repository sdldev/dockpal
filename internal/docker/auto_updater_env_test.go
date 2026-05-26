package docker

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

// unsetEnv removes an env var for the duration of the test, restoring its
// previous value (if any) on cleanup. Used to exercise the LookupEnv "not
// set" branch, which `t.Setenv("KEY", "")` does NOT exercise (Setenv with
// "" still leaves the key set to empty string).
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unsetenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// captureLogs swaps the standard logger's writer for a bytes.Buffer for the
// duration of the test, restoring the previous writer on cleanup. The
// returned buffer accumulates every log line emitted while the swap is
// active. Used to assert "logs a single warning" for invalid env values.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// =============================================================================
// DOCKPAL_AUTO_UPDATE_ENABLED
// =============================================================================

func TestReadAutoUpdateEnabled_Missing_UsesDefault(t *testing.T) {
	unsetEnv(t, envAutoUpdateEnabled)
	buf := captureLogs(t)
	if got := readAutoUpdateEnabled(); got != defaultAutoUpdateEnabled {
		t.Fatalf("missing env: got %v, want default %v", got, defaultAutoUpdateEnabled)
	}
	if strings.Contains(buf.String(), "not recognized") {
		t.Fatalf("unexpected warning when env is missing; logs: %s", buf.String())
	}
}

func TestReadAutoUpdateEnabled_Empty_UsesDefault(t *testing.T) {
	t.Setenv(envAutoUpdateEnabled, "")
	buf := captureLogs(t)
	if got := readAutoUpdateEnabled(); got != defaultAutoUpdateEnabled {
		t.Fatalf("empty env: got %v, want default %v", got, defaultAutoUpdateEnabled)
	}
	if strings.Contains(buf.String(), "not recognized") {
		t.Fatalf("unexpected warning for empty value; logs: %s", buf.String())
	}
}

func TestReadAutoUpdateEnabled_Valid(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"no", false},
		{"off", false},
		{"  true  ", true},
		{"  false  ", false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv(envAutoUpdateEnabled, tc.raw)
			buf := captureLogs(t)
			got := readAutoUpdateEnabled()
			if got != tc.want {
				t.Fatalf("raw=%q: got %v, want %v", tc.raw, got, tc.want)
			}
			if strings.Contains(buf.String(), "not recognized") {
				t.Fatalf("unexpected warning for valid value %q: %s", tc.raw, buf.String())
			}
		})
	}
}

func TestReadAutoUpdateEnabled_Invalid_FallsBackAndLogsSingleWarning(t *testing.T) {
	t.Setenv(envAutoUpdateEnabled, "maybe")
	buf := captureLogs(t)

	got := readAutoUpdateEnabled()
	if got != defaultAutoUpdateEnabled {
		t.Fatalf("invalid value: got %v, want default %v", got, defaultAutoUpdateEnabled)
	}
	out := buf.String()
	if !strings.Contains(out, envAutoUpdateEnabled) || !strings.Contains(out, "not recognized") {
		t.Fatalf("missing warning for invalid value; logs: %s", out)
	}
	if n := strings.Count(out, "not recognized"); n != 1 {
		t.Fatalf("expected a single warning, got %d; logs: %s", n, out)
	}
}

// =============================================================================
// DOCKPAL_AUTO_UPDATE_COOLDOWN_MINUTES
// =============================================================================

func TestReadAutoUpdateCooldown_MissingOrEmpty_UsesDefault(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		unsetEnv(t, envAutoUpdateCooldownMinutes)
		buf := captureLogs(t)
		got := readAutoUpdateCooldown()
		if got != defaultAutoUpdateCooldown {
			t.Fatalf("missing env: got %s, want %s", got, defaultAutoUpdateCooldown)
		}
		if strings.Contains(buf.String(), "invalid") {
			t.Fatalf("unexpected warning for missing env; logs: %s", buf.String())
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Setenv(envAutoUpdateCooldownMinutes, "")
		buf := captureLogs(t)
		got := readAutoUpdateCooldown()
		if got != defaultAutoUpdateCooldown {
			t.Fatalf("empty env: got %s, want %s", got, defaultAutoUpdateCooldown)
		}
		if strings.Contains(buf.String(), "invalid") {
			t.Fatalf("unexpected warning for empty env; logs: %s", buf.String())
		}
	})
}

func TestReadAutoUpdateCooldown_Valid(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"1", 1 * time.Minute},
		{"30", 30 * time.Minute},
		{"120", 120 * time.Minute},
		{"  45  ", 45 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv(envAutoUpdateCooldownMinutes, tc.raw)
			buf := captureLogs(t)
			got := readAutoUpdateCooldown()
			if got != tc.want {
				t.Fatalf("raw=%q: got %s, want %s", tc.raw, got, tc.want)
			}
			if strings.Contains(buf.String(), "invalid") {
				t.Fatalf("unexpected warning for valid value %q: %s", tc.raw, buf.String())
			}
		})
	}
}

func TestReadAutoUpdateCooldown_OutOfRange_FallsBackAndLogsSingleWarning(t *testing.T) {
	cases := []string{
		"0",       // below min
		"-5",      // negative
		"abc",     // unparsable
		"1.5",     // non-integer
		"   bad ", // unparsable with whitespace
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(envAutoUpdateCooldownMinutes, raw)
			buf := captureLogs(t)

			got := readAutoUpdateCooldown()
			if got != defaultAutoUpdateCooldown {
				t.Fatalf("raw=%q: got %s, want default %s", raw, got, defaultAutoUpdateCooldown)
			}
			out := buf.String()
			if !strings.Contains(out, envAutoUpdateCooldownMinutes) || !strings.Contains(out, "invalid") {
				t.Fatalf("missing warning for raw=%q; logs: %s", raw, out)
			}
			if n := strings.Count(out, "invalid"); n != 1 {
				t.Fatalf("expected a single warning, got %d; logs: %s", n, out)
			}
		})
	}
}

// =============================================================================
// DOCKPAL_AUTO_UPDATE_CONCURRENCY (default 2, clamped to [1, 8])
// =============================================================================

func TestReadAutoUpdateConcurrency_MissingOrEmpty_UsesDefault(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		unsetEnv(t, envAutoUpdateConcurrency)
		buf := captureLogs(t)
		got := readAutoUpdateConcurrency()
		if got != defaultAutoUpdateConcurrency {
			t.Fatalf("missing env: got %d, want %d", got, defaultAutoUpdateConcurrency)
		}
		if strings.Contains(buf.String(), "invalid") {
			t.Fatalf("unexpected warning for missing env; logs: %s", buf.String())
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Setenv(envAutoUpdateConcurrency, "")
		buf := captureLogs(t)
		got := readAutoUpdateConcurrency()
		if got != defaultAutoUpdateConcurrency {
			t.Fatalf("empty env: got %d, want %d", got, defaultAutoUpdateConcurrency)
		}
		if strings.Contains(buf.String(), "invalid") {
			t.Fatalf("unexpected warning for empty env; logs: %s", buf.String())
		}
	})
}

func TestReadAutoUpdateConcurrency_Valid(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"1", 1},
		{"2", 2},
		{"4", 4},
		{"8", 8},
		{"  3  ", 3},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv(envAutoUpdateConcurrency, tc.raw)
			buf := captureLogs(t)
			got := readAutoUpdateConcurrency()
			if got != tc.want {
				t.Fatalf("raw=%q: got %d, want %d", tc.raw, got, tc.want)
			}
			if strings.Contains(buf.String(), "invalid") {
				t.Fatalf("unexpected warning for valid value %q: %s", tc.raw, buf.String())
			}
		})
	}
}

func TestReadAutoUpdateConcurrency_OutOfRange_FallsBackAndLogsSingleWarning(t *testing.T) {
	cases := []string{
		"0",   // below min
		"-1",  // negative
		"9",   // above max (R7.3 says max 8)
		"100", // way above max
		"abc", // unparsable
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(envAutoUpdateConcurrency, raw)
			buf := captureLogs(t)

			got := readAutoUpdateConcurrency()
			if got != defaultAutoUpdateConcurrency {
				t.Fatalf("raw=%q: got %d, want default %d", raw, got, defaultAutoUpdateConcurrency)
			}
			out := buf.String()
			if !strings.Contains(out, envAutoUpdateConcurrency) || !strings.Contains(out, "invalid") {
				t.Fatalf("missing warning for raw=%q; logs: %s", raw, out)
			}
			if n := strings.Count(out, "invalid"); n != 1 {
				t.Fatalf("expected a single warning, got %d; logs: %s", n, out)
			}
		})
	}
}

// =============================================================================
// DOCKPAL_AUTO_UPDATE_HEALTH_GRACE_SECONDS
// =============================================================================

func TestReadAutoUpdateGrace_MissingOrEmpty_UsesDefault(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		unsetEnv(t, envAutoUpdateGraceSeconds)
		buf := captureLogs(t)
		got := readAutoUpdateGrace()
		if got != defaultAutoUpdateGrace {
			t.Fatalf("missing env: got %s, want %s", got, defaultAutoUpdateGrace)
		}
		if strings.Contains(buf.String(), "invalid") {
			t.Fatalf("unexpected warning for missing env; logs: %s", buf.String())
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Setenv(envAutoUpdateGraceSeconds, "")
		buf := captureLogs(t)
		got := readAutoUpdateGrace()
		if got != defaultAutoUpdateGrace {
			t.Fatalf("empty env: got %s, want %s", got, defaultAutoUpdateGrace)
		}
		if strings.Contains(buf.String(), "invalid") {
			t.Fatalf("unexpected warning for empty env; logs: %s", buf.String())
		}
	})
}

func TestReadAutoUpdateGrace_Valid(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"5", 5 * time.Second},
		{"30", 30 * time.Second},
		{"60", 60 * time.Second},
		{"  120  ", 120 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv(envAutoUpdateGraceSeconds, tc.raw)
			buf := captureLogs(t)
			got := readAutoUpdateGrace()
			if got != tc.want {
				t.Fatalf("raw=%q: got %s, want %s", tc.raw, got, tc.want)
			}
			if strings.Contains(buf.String(), "invalid") {
				t.Fatalf("unexpected warning for valid value %q: %s", tc.raw, buf.String())
			}
		})
	}
}

func TestReadAutoUpdateGrace_OutOfRange_FallsBackAndLogsSingleWarning(t *testing.T) {
	cases := []string{
		"0",   // below min (5s)
		"4",   // below min
		"-10", // negative
		"abc", // unparsable
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(envAutoUpdateGraceSeconds, raw)
			buf := captureLogs(t)

			got := readAutoUpdateGrace()
			if got != defaultAutoUpdateGrace {
				t.Fatalf("raw=%q: got %s, want default %s", raw, got, defaultAutoUpdateGrace)
			}
			out := buf.String()
			if !strings.Contains(out, envAutoUpdateGraceSeconds) || !strings.Contains(out, "invalid") {
				t.Fatalf("missing warning for raw=%q; logs: %s", raw, out)
			}
			if n := strings.Count(out, "invalid"); n != 1 {
				t.Fatalf("expected a single warning, got %d; logs: %s", n, out)
			}
		})
	}
}

// =============================================================================
// Boundary checks: ensure the documented constants line up with R7.3 (max 8).
// =============================================================================

func TestAutoUpdateConcurrencyMaxIsEight(t *testing.T) {
	if maxAutoUpdateConcurrency != 8 {
		t.Fatalf("requirement R7.3 caps concurrency at 8; constant = %d", maxAutoUpdateConcurrency)
	}
}

func TestAutoUpdateDefaultsMatchRequirements(t *testing.T) {
	if defaultAutoUpdateCooldown != 60*time.Minute {
		t.Fatalf("R7.2 default 60 minutes; got %s", defaultAutoUpdateCooldown)
	}
	if defaultAutoUpdateConcurrency != 2 {
		t.Fatalf("R7.3 default 2; got %d", defaultAutoUpdateConcurrency)
	}
	if defaultAutoUpdateGrace != 60*time.Second {
		t.Fatalf("R7.4 default 60 seconds; got %s", defaultAutoUpdateGrace)
	}
	if defaultAutoUpdateEnabled != true {
		t.Fatalf("R7.1 default true; got %v", defaultAutoUpdateEnabled)
	}
}
