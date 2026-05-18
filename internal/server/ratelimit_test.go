package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimiter_AllowWithinLimit(t *testing.T) {
	rl := NewRateLimiter()

	for i := 0; i < rateLimitMax; i++ {
		allowed, _ := rl.Allow("192.168.1.1")
		if !allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlockAfterLimit(t *testing.T) {
	rl := NewRateLimiter()

	// Use up the limit
	for i := 0; i < rateLimitMax; i++ {
		rl.Allow("192.168.1.1")
	}

	// 6th request should be blocked
	allowed, retryAfter := rl.Allow("192.168.1.1")
	if allowed {
		t.Fatal("6th request should be blocked")
	}
	if retryAfter <= 0 {
		t.Fatal("retryAfter should be positive")
	}
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	rl := NewRateLimiter()

	// Fill up limit for IP1
	for i := 0; i < rateLimitMax; i++ {
		rl.Allow("192.168.1.1")
	}

	// IP2 should still be allowed
	allowed, _ := rl.Allow("192.168.1.2")
	if !allowed {
		t.Fatal("different IP should not be affected")
	}
}

func TestRateLimiter_WindowExpiration(t *testing.T) {
	rl := NewRateLimiter()

	// Manually inject old timestamps
	rl.mu.Lock()
	entry := &rateLimitEntry{
		timestamps: make([]time.Time, rateLimitMax),
	}
	// Set all timestamps to 2 minutes ago (outside the window)
	for i := range entry.timestamps {
		entry.timestamps[i] = time.Now().Add(-2 * time.Minute)
	}
	rl.entries["192.168.1.1"] = entry
	rl.mu.Unlock()

	// Should be allowed since old timestamps are outside window
	allowed, _ := rl.Allow("192.168.1.1")
	if !allowed {
		t.Fatal("request should be allowed after window expires")
	}
}

func TestRateLimitMiddleware_Returns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := NewRateLimiter()

	router := gin.New()
	router.POST("/api/login", RateLimitMiddleware(rl), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Make 5 successful requests
	for i := 0; i < rateLimitMax; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/login", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// 6th request should get 429
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}

	// Check Retry-After header is present
	retryAfter := w.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("Retry-After header should be present")
	}
}
