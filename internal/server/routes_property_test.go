package server

import (
	"net/http"
	"net/url"
	"testing"
	"testing/quick"
)

// Property 1: WebSocket Origin Validation
// **Validates: Requirements 2.1, 2.2, 2.3, 2.4**

// TestProperty_CheckOrigin_AllowsMatchingHosts verifies that when the Origin
// header's host matches the request Host, the upgrade is allowed.
func TestProperty_CheckOrigin_AllowsMatchingHosts(t *testing.T) {
	f := func(host string) bool {
		// Filter to valid hosts (non-empty, no whitespace, no control chars)
		if host == "" {
			return true // skip empty
		}
		for _, c := range host {
			if c <= 32 || c == 127 {
				return true // skip invalid hosts
			}
		}

		// Ensure the host is a valid URL host
		u, err := url.Parse("https://" + host)
		if err != nil || u.Host != host {
			return true // skip invalid/unparseable hosts
		}

		// Build a request with matching origin host
		r, _ := http.NewRequest("GET", "/ws", nil)
		r.Host = host
		r.Header.Set("Origin", "https://"+host)

		return checkOrigin(r) == true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("Property violated - matching origin host was rejected: %v", err)
	}
}

// TestProperty_CheckOrigin_RejectsEmptyOrigin verifies that requests with
// empty or missing Origin headers are always rejected.
func TestProperty_CheckOrigin_RejectsEmptyOrigin(t *testing.T) {
	f := func(host string) bool {
		if host == "" {
			return true // skip
		}
		for _, c := range host {
			if c <= 32 || c == 127 {
				return true // skip invalid
			}
		}

		r, _ := http.NewRequest("GET", "/ws", nil)
		r.Host = host
		// No Origin header set

		return checkOrigin(r) == false
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("Property violated - empty origin was accepted: %v", err)
	}
}

// TestProperty_CheckOrigin_RejectsMismatchedHosts verifies that when the
// Origin header's host differs from the request Host, the upgrade is rejected.
func TestProperty_CheckOrigin_RejectsMismatchedHosts(t *testing.T) {
	f := func(originHost, requestHost string) bool {
		// Filter to valid non-empty hosts
		if originHost == "" || requestHost == "" {
			return true
		}
		for _, c := range originHost {
			if c <= 32 || c == 127 {
				return true
			}
		}
		for _, c := range requestHost {
			if c <= 32 || c == 127 {
				return true
			}
		}

		// Only test when hosts actually differ
		// Parse the origin to get what url.Parse would see as Host
		u, err := url.Parse("https://" + originHost)
		if err != nil || u.Host == "" {
			return true // skip unparseable
		}
		if u.Host == requestHost {
			return true // skip matching hosts
		}

		r, _ := http.NewRequest("GET", "/ws", nil)
		r.Host = requestHost
		r.Header.Set("Origin", "https://"+originHost)

		return checkOrigin(r) == false
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("Property violated - mismatched origin was accepted: %v", err)
	}
}

// TestProperty_CheckOrigin_RejectsInvalidOriginURL verifies that origins
// which cannot be parsed as valid URLs are rejected.
func TestProperty_CheckOrigin_RejectsInvalidOriginURL(t *testing.T) {
	f := func(origin string) bool {
		if origin == "" {
			return true // covered by empty test
		}

		// Check if this would actually be parseable with a host
		u, err := url.Parse(origin)
		if err == nil && u.Host != "" {
			return true // skip valid parseable origins
		}

		r, _ := http.NewRequest("GET", "/ws", nil)
		r.Host = "example.com"
		r.Header.Set("Origin", origin)

		return checkOrigin(r) == false
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("Property violated - invalid origin URL was accepted: %v", err)
	}
}

// TestProperty_CheckOrigin_OriginHostEquality verifies the core invariant:
// checkOrigin returns true if and only if the parsed Origin host equals the
// request Host (and Origin is non-empty and parseable).
func TestProperty_CheckOrigin_OriginHostEquality(t *testing.T) {
	f := func(scheme, originHost, requestHost string) bool {
		// Constrain scheme to valid URL schemes
		schemes := []string{"http", "https", "ws", "wss"}
		if len(scheme) == 0 {
			scheme = "https"
		} else {
			scheme = schemes[int(scheme[0])%len(schemes)]
		}

		// Filter to printable non-empty hosts
		if originHost == "" || requestHost == "" {
			return true
		}
		for _, c := range originHost {
			if c <= 32 || c == 127 {
				return true
			}
		}
		for _, c := range requestHost {
			if c <= 32 || c == 127 {
				return true
			}
		}

		origin := scheme + "://" + originHost
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			return true // skip unparseable
		}

		r, _ := http.NewRequest("GET", "/ws", nil)
		r.Host = requestHost
		r.Header.Set("Origin", origin)

		result := checkOrigin(r)
		expected := (u.Host == requestHost)

		return result == expected
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Property violated - checkOrigin result doesn't match host equality: %v", err)
	}
}
