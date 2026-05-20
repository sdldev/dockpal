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

type rateLimitEntry struct {
	timestamps []time.Time
}

// RateLimiter implements a sliding window rate limiter that tracks
// request counts per IP address within a configurable time window.
type RateLimiter struct {
	mu          sync.Mutex
	entries     map[string]*rateLimitEntry
	lastCleanup time.Time
}

// NewRateLimiter creates a new RateLimiter with an empty entry map.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		entries: make(map[string]*rateLimitEntry),
	}
}

// Allow checks if the given IP is within rate limits.
// Returns (allowed bool, retryAfter time.Duration).
func (rl *RateLimiter) cleanupExpired(now time.Time) {
	if !rl.lastCleanup.IsZero() && now.Sub(rl.lastCleanup) < rateLimitCleanupInterval {
		return
	}
	rl.lastCleanup = now
	cutoff := now.Add(-rateLimitWindow)
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
	cutoff := now.Add(-rateLimitWindow)
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

	if len(entry.timestamps) >= rateLimitMax {
		oldest := entry.timestamps[0]
		retryAfter := oldest.Add(rateLimitWindow).Sub(now)
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
		ip := c.ClientIP()
		allowed, retryAfter := rl.Allow(ip)
		if !allowed {
			c.Header("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		c.Next()
	}
}
