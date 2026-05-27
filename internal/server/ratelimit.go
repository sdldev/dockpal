package server

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	rateLimitWindow          = 1 * time.Minute
	rateLimitMax             = 5
	rateLimitCleanupInterval = 1 * time.Minute
)

var (
	LoginRateLimit    = RateLimitPolicy{Window: rateLimitWindow, MaxRequests: 5}
	WebhookRateLimit  = RateLimitPolicy{Window: rateLimitWindow, MaxRequests: 10}
	ReadRateLimit     = RateLimitPolicy{Window: rateLimitWindow, MaxRequests: 60}
	MutationRateLimit = RateLimitPolicy{Window: rateLimitWindow, MaxRequests: 10}
)

type RateLimitPolicy struct {
	Window      time.Duration
	MaxRequests int
}

type rateLimitEntry struct {
	timestamps []time.Time
}

// RateLimiter implements a sliding window rate limiter that tracks
// request counts per IP address within a configurable time window.
type RateLimiter struct {
	mu          sync.Mutex
	entries     map[string]*rateLimitEntry
	lastCleanup time.Time
	window      time.Duration
	maxRequests int
}

// NewRateLimiter creates a new RateLimiter with the default login policy.
func NewRateLimiter() *RateLimiter {
	return NewRateLimiterWithPolicy(LoginRateLimit)
}

func NewRateLimiterWithPolicy(policy RateLimitPolicy) *RateLimiter {
	if policy.Window <= 0 {
		policy.Window = rateLimitWindow
	}
	if policy.MaxRequests <= 0 {
		policy.MaxRequests = rateLimitMax
	}
	return &RateLimiter{
		entries:     make(map[string]*rateLimitEntry),
		window:      policy.Window,
		maxRequests: policy.MaxRequests,
	}
}

// Allow checks if the given IP is within rate limits.
// Returns (allowed bool, retryAfter time.Duration).
func (rl *RateLimiter) cleanupExpired(now time.Time) {
	if !rl.lastCleanup.IsZero() && now.Sub(rl.lastCleanup) < rateLimitCleanupInterval {
		return
	}
	rl.lastCleanup = now
	cutoff := now.Add(-rl.window)
	for ip, entry := range rl.entries {
		valid := entry.timestamps[:0]
		for _, t := range entry.timestamps {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(rl.entries, ip)
			continue
		}
		entry.timestamps = valid
	}
}

func (rl *RateLimiter) Allow(ip string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rl.cleanupExpired(now)
	entry, exists := rl.entries[ip]
	if !exists {
		entry = &rateLimitEntry{}
		rl.entries[ip] = entry
	}

	// Prune timestamps outside window
	cutoff := now.Add(-rl.window)
	valid := entry.timestamps[:0]
	for _, t := range entry.timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	entry.timestamps = valid
	if len(entry.timestamps) == 0 {
		delete(rl.entries, ip)
		entry = &rateLimitEntry{}
		rl.entries[ip] = entry
	}

	if len(entry.timestamps) >= rl.maxRequests {
		oldest := entry.timestamps[0]
		retryAfter := oldest.Add(rl.window).Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return false, retryAfter
	}

	entry.timestamps = append(entry.timestamps, now)
	return true, 0
}

// RateLimitMiddleware returns a Gin handler that applies rate limiting
// based on client IP address. Returns HTTP 429 with Retry-After header
// when the rate limit is exceeded.
func RateLimitMiddleware(rl *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := rateLimitKey(c)
		allowed, retryAfter := rl.Allow(key)
		if !allowed {
			c.Header("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func rateLimitKey(c *gin.Context) string {
	return c.ClientIP() + " " + c.Request.Method + " " + c.FullPath()
}

func methodRateLimit(readLimit, mutationLimit gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isMutationMethod(c.Request.Method) {
			mutationLimit(c)
			return
		}
		readLimit(c)
	}
}

func isMutationMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
