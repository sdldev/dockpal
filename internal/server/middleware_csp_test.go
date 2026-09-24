package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSecurityHeaders_CSPHardened guards the Content-Security-Policy header.
// script-src must be 'self' only — the Vite production build emits external
// hashed chunks, so 'unsafe-inline'/'unsafe-eval' are not needed and must not
// creep back in. style-src keeps 'unsafe-inline' because the SPA uses dynamic
// inline style attributes (e.g. Dashboard width gauges) that break without it.
func TestSecurityHeaders_CSPHardened(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeadersMiddleware())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}

	for _, forbidden := range []string{"unsafe-inline", "unsafe-eval"} {
		if sc := scriptSrcDirective(csp); sc != "" && strings.Contains(sc, forbidden) {
			t.Fatalf("script-src must not allow %q, got: %s", forbidden, sc)
		}
	}

	if sc := scriptSrcDirective(csp); !strings.Contains(sc, `'self'`) {
		t.Fatalf("script-src must include 'self', got: %s", sc)
	}

	style := styleSrcDirective(csp)
	if style == "" || !strings.Contains(style, "unsafe-inline") {
		t.Fatalf("style-src must keep 'unsafe-inline' (SPA uses inline styles), got: %s", style)
	}
}

func TestSecurityHeaders_OtherSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeadersMiddleware())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}
}

// scriptSrcDirective extracts the value of the script-src directive.
func scriptSrcDirective(csp string) string {
	return srcDirective(csp, "script-src")
}

// styleSrcDirective extracts the value of the style-src directive.
func styleSrcDirective(csp string) string {
	return srcDirective(csp, "style-src")
}

func srcDirective(csp, name string) string {
	for _, part := range strings.Split(csp, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 && fields[0] == name {
			return strings.Join(fields[1:], " ")
		}
	}
	return ""
}
