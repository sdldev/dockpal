package server

import (
	"net/http"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		host     string
		expected bool
	}{
		{
			name:     "matching origin and host",
			origin:   "https://example.com",
			host:     "example.com",
			expected: true,
		},
		{
			name:     "matching origin with port",
			origin:   "https://example.com:8080",
			host:     "example.com:8080",
			expected: true,
		},
		{
			name:     "mismatched origin host",
			origin:   "https://evil.com",
			host:     "example.com",
			expected: false,
		},
		{
			name:     "empty origin header",
			origin:   "",
			host:     "example.com",
			expected: false,
		},
		{
			name:     "origin without host portion",
			origin:   "not-a-url",
			host:     "example.com",
			expected: false,
		},
		{
			name:     "origin with different port",
			origin:   "https://example.com:9090",
			host:     "example.com:8080",
			expected: false,
		},
		{
			name:     "http scheme matching host",
			origin:   "http://localhost:3000",
			host:     "localhost:3000",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/ws", nil)
			r.Host = tt.host
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}

			result := checkOrigin(r)
			if result != tt.expected {
				t.Errorf("checkOrigin() = %v, want %v (origin=%q, host=%q)", result, tt.expected, tt.origin, tt.host)
			}
		})
	}
}
