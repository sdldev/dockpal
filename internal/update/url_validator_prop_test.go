package update

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Feature: update-mechanism, Property 1: URL validation accepts only safe download sources.
func TestProperty1_URLValidation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		scheme := rapid.SampledFrom([]string{"https", "http", "ftp", "file"}).Draw(t, "scheme")
		host := rapid.SampledFrom([]string{
			"github.com",
			"api.github.com",
			"objects.githubusercontent.com",
			"raw.githubusercontent.com",
			"example.com",
			"evil.github.com.evil.com",
		}).Draw(t, "host")
		hasUserinfo := rapid.Bool().Draw(t, "hasUserinfo")
		resolveToPrivate := rapid.Bool().Draw(t, "resolveToPrivate")

		var userinfo string
		if hasUserinfo {
			userinfo = "user:pass@"
		}
		rawURL := fmt.Sprintf("%s://%s%s/path", scheme, userinfo, host)

		resolver := newStubResolver()
		if resolveToPrivate {
			resolver.AddHost(host, []net.IP{net.ParseIP("192.168.1.1")})
		} else {
			resolver.AddHost(host, []net.IP{net.ParseIP("140.82.121.4")})
		}

		err := ValidateDownloadURLWithResolver(rawURL, resolver)

		shouldAccept := scheme == "https" &&
			isAllowedHost(host) &&
			!hasUserinfo &&
			!resolveToPrivate

		if shouldAccept && err != nil {
			t.Fatalf("expected accept for %s, got: %v", rawURL, err)
		}
		if !shouldAccept && err == nil {
			t.Fatalf("expected reject for %s, got nil", rawURL)
		}
		if !shouldAccept && err != nil {
			// Verify correct error code
			msg := err.Error()
			switch {
			case scheme != "https":
				if !strings.Contains(msg, ErrURLSchemeNotHTTPS) {
					t.Fatalf("expected %s error, got: %v", ErrURLSchemeNotHTTPS, err)
				}
			case hasUserinfo:
				if !strings.Contains(msg, ErrURLCredentialsPresent) {
					t.Fatalf("expected %s error, got: %v", ErrURLCredentialsPresent, err)
				}
			case !isAllowedHost(host):
				if !strings.Contains(msg, ErrURLHostNotAllowed) {
					t.Fatalf("expected %s error, got: %v", ErrURLHostNotAllowed, err)
				}
			case resolveToPrivate:
				if !strings.Contains(msg, ErrURLResolvesPrivateIP) {
					t.Fatalf("expected %s error, got: %v", ErrURLResolvesPrivateIP, err)
				}
			}
		}
	})
}

func isAllowedHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || strings.HasSuffix(host, ".github.com") ||
		host == "objects.githubusercontent.com" ||
		strings.HasSuffix(host, ".githubusercontent.com")
}
