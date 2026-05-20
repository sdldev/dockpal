package update

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

// Feature: update-mechanism, Property 3: Checksum file parser round-trip.
func TestProperty3_ChecksumRoundTrip(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Values: func(values []reflect.Value, rng *rand.Rand) {
			pairs := generateChecksumPairs(rng)
			values[0] = reflect.ValueOf(pairs)
		},
	}
	prop := func(pairs []checksumPair) bool {
		if len(pairs) == 0 {
			return true
		}
		var sb strings.Builder
		for _, pair := range pairs {
			sb.WriteString(pair.Hex)
			sb.WriteString("  ")
			sb.WriteString(pair.Name)
			sb.WriteString("\n")
		}
		content := []byte(sb.String())
		for _, pair := range pairs {
			digest, err := parseChecksumFile(content, pair.Name)
			if err != nil {
				return false
			}
			if digest != strings.ToLower(pair.Hex) {
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 3 failed: %v", err)
	}
}

type checksumPair struct {
	Hex  string
	Name string
}

func generateChecksumPairs(rng *rand.Rand) []checksumPair {
	n := 1 + rng.Intn(10)
	seen := make(map[string]bool)
	var pairs []checksumPair
	for i := 0; i < n; i++ {
		const hexChars = "0123456789abcdef"
		hex := make([]byte, 64)
		for j := range hex {
			hex[j] = hexChars[rng.Intn(len(hexChars))]
		}
		nameLen := 1 + rng.Intn(20)
		name := make([]byte, nameLen)
		for j := range name {
			name[j] = byte('a' + rng.Intn(26))
		}
		nm := string(name)
		if seen[nm] {
			continue
		}
		seen[nm] = true
		pairs = append(pairs, checksumPair{Hex: string(hex), Name: nm})
	}
	return pairs
}

// Feature: update-mechanism, Property 3: Malformed line returns error.
func TestProperty3_MalformedLine(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"too short hex", "abc  file\n", true},
		{"non-hex chars", "zzzz" + strings.Repeat("0", 60) + "  file\n", true},
		{"single field", "abc123\n", true},
	}
	for _, tc := range cases {
		_, err := parseChecksumFile([]byte(tc.content), "file")
		if tc.wantErr && err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
	}
}
