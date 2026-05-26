package docker

import (
	"strings"
	"testing"
	"time"
)

// localTimeAt builds a time.Time anchored in the host's local time zone for
// the given clock face (HH:MM). The actual date is irrelevant to
// parseWindow — only the hour and minute of the wall clock matter — so we
// fix it on a stable day in 2026.
func localTimeAt(hour, minute int) time.Time {
	return time.Date(2026, time.January, 1, hour, minute, 0, 0, time.Local)
}

// TestParseWindow_TableDriven exercises every accepted and rejected shape
// of the `dockpal.auto-update.window` label per task 3.8 / R2.5.
func TestParseWindow_TableDriven(t *testing.T) {
	t.Parallel()

	type sample struct {
		hour    int
		minute  int
		allowed bool
	}

	cases := []struct {
		name           string
		spec           string
		wantErrSubstr  string // empty -> no error expected
		samples        []sample
		alwaysAllowed  bool // when true, the predicate must return true for every sample
		alwaysDeniedAt []sample
	}{
		{
			name:          "empty spec means any time",
			spec:          "",
			alwaysAllowed: true,
			samples: []sample{
				{0, 0, true},
				{12, 0, true},
				{23, 59, true},
			},
		},
		{
			name: "same-day window 09:00-17:00",
			spec: "09:00-17:00",
			samples: []sample{
				{12, 0, true},   // mid-window
				{9, 0, true},    // inclusive start
				{16, 59, true},  // last minute inside
				{17, 0, false},  // exclusive end
				{18, 0, false},  // after end
				{6, 0, false},   // before start
				{8, 59, false},  // just before start
				{23, 59, false}, // late night
			},
		},
		{
			name: "overnight window 22:00-04:00",
			spec: "22:00-04:00",
			samples: []sample{
				{23, 0, true},   // late evening
				{2, 0, true},    // early morning
				{22, 0, true},   // inclusive start
				{3, 59, true},   // last minute inside
				{4, 0, false},   // exclusive end
				{12, 0, false},  // afternoon
				{21, 59, false}, // just before start
				{0, 0, true},    // midnight
			},
		},
		{
			name: "trims surrounding whitespace",
			spec: "  09:00-17:00  ",
			samples: []sample{
				{10, 0, true},
				{20, 0, false},
			},
		},
		{
			name:          "zero duration spec is rejected",
			spec:          "09:00-09:00",
			wantErrSubstr: "zero duration",
		},
		{
			name:          "missing dash is unsupported",
			spec:          "09:00",
			wantErrSubstr: "unsupported window spec",
		},
		{
			name:          "single-digit hour is unsupported",
			spec:          "9:00-17:00",
			wantErrSubstr: "unsupported window spec",
		},
		{
			name:          "cron-style spec is unsupported",
			spec:          "0 9 * * *",
			wantErrSubstr: "unsupported window spec",
		},
		{
			name:          "out-of-range hour",
			spec:          "25:00-26:00",
			wantErrSubstr: "invalid time component",
		},
		{
			name:          "out-of-range minute",
			spec:          "09:60-10:00",
			wantErrSubstr: "invalid time component",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			allowed, err := parseWindow(tc.spec)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("parseWindow(%q): expected error containing %q, got nil", tc.spec, tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("parseWindow(%q): expected error containing %q, got %q",
						tc.spec, tc.wantErrSubstr, err.Error())
				}
				if allowed != nil {
					t.Fatalf("parseWindow(%q): expected nil predicate on error, got %T", tc.spec, allowed)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWindow(%q): unexpected error: %v", tc.spec, err)
			}
			if allowed == nil {
				t.Fatalf("parseWindow(%q): expected non-nil predicate", tc.spec)
			}

			for _, s := range tc.samples {
				got := allowed(localTimeAt(s.hour, s.minute))
				want := s.allowed
				if tc.alwaysAllowed {
					want = true
				}
				if got != want {
					t.Errorf("parseWindow(%q) at %02d:%02d: got allowed=%v, want %v",
						tc.spec, s.hour, s.minute, got, want)
				}
			}
		})
	}
}

// TestParseWindow_UsesLocalTime guards against accidentally comparing UTC
// hours when the host is in a non-UTC zone. The predicate must convert the
// supplied time.Time into the host's local zone before extracting the
// minute-of-day, so passing the same instant expressed in UTC and in
// time.Local must yield the same answer.
func TestParseWindow_UsesLocalTime(t *testing.T) {
	t.Parallel()

	allowed, err := parseWindow("09:00-17:00")
	if err != nil {
		t.Fatalf("parseWindow: unexpected error: %v", err)
	}

	noon := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.Local)
	if !allowed(noon) {
		t.Fatalf("expected noon local time to be inside 09:00-17:00")
	}
	if !allowed(noon.UTC()) {
		t.Fatalf("expected the same instant expressed in UTC to be inside 09:00-17:00 after local conversion")
	}
}
