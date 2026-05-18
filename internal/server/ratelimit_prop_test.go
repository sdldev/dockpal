package server

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"time"
)

// **Validates: Requirements 4.2, 4.4**

// Property 3: Rate Limiter Enforcement — requests 6+ within window are rejected
// **Validates: Requirements 4.2**

func TestProperty3_RateLimiterEnforcement(t *testing.T) {
	// Property: For any N in [6, 50], making N requests from the same IP
	// within the window results in requests 6+ being rejected.
	prop := func(n requestCount) bool {
		rl := NewRateLimiter()
		ip := "10.0.0.1"

		count := int(n)

		// Make 'count' requests from the same IP
		for i := 0; i < count; i++ {
			allowed, retryAfter := rl.Allow(ip)

			if i < rateLimitMax {
				// First 5 requests should be allowed
				if !allowed {
					return false
				}
				if retryAfter != 0 {
					return false
				}
			} else {
				// Requests 6+ should be rejected
				if allowed {
					return false
				}
				if retryAfter <= 0 {
					return false
				}
			}
		}
		return true
	}

	cfg := &quick.Config{
		MaxCount: 500,
		Values:   requestCountGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 3 failed: %v", err)
	}
}

// requestCount represents a number of requests in [6, 50]
type requestCount int

func requestCountGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate request counts from 6 to 50
	n := requestCount(6 + rng.Intn(45))
	values[0] = reflect.ValueOf(n)
}

// Property 4: Rate Limiter Window Expiration — requests allowed after window elapses
// **Validates: Requirements 4.4**

func TestProperty4_RateLimiterWindowExpiration(t *testing.T) {
	// Property: After all timestamps in the window have expired (older than 1 minute),
	// subsequent requests from the same IP are allowed again.
	prop := func(params windowExpirationParams) bool {
		rl := NewRateLimiter()
		ip := "10.0.0.1"

		// Inject old timestamps that are outside the window (expired)
		rl.mu.Lock()
		entry := &rateLimitEntry{
			timestamps: make([]time.Time, params.filledSlots),
		}
		// Set all timestamps to be outside the window (past expiration)
		expiredTime := time.Now().Add(-rateLimitWindow - params.extraExpiry)
		for i := range entry.timestamps {
			entry.timestamps[i] = expiredTime
		}
		rl.entries[ip] = entry
		rl.mu.Unlock()

		// After window expiration, request should be allowed
		allowed, retryAfter := rl.Allow(ip)
		if !allowed {
			return false
		}
		if retryAfter != 0 {
			return false
		}
		return true
	}

	cfg := &quick.Config{
		MaxCount: 500,
		Values:   windowExpirationGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 4 failed: %v", err)
	}
}

// windowExpirationParams represents the test parameters for window expiration
type windowExpirationParams struct {
	filledSlots int           // how many slots were filled (1-5, at or above the limit)
	extraExpiry time.Duration // extra time past window expiration
}

func windowExpirationGenerator(values []reflect.Value, rng *rand.Rand) {
	params := windowExpirationParams{
		// Fill between 1 and rateLimitMax slots (simulate various usage levels at limit)
		filledSlots: 1 + rng.Intn(rateLimitMax),
		// Extra time past expiration: 1 second to 5 minutes
		extraExpiry: time.Duration(1+rng.Intn(300)) * time.Second,
	}
	values[0] = reflect.ValueOf(params)
}
