package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/ssh"
)

func TestValidateUnbanIP(t *testing.T) {
	valid := []string{"203.0.113.7", "198.51.100.255", "2001:db8::1", " 10.0.0.1 "}
	for _, in := range valid {
		if _, err := validateUnbanIP(in); err != nil {
			t.Errorf("validateUnbanIP(%q) = %v, want ok", in, err)
		}
	}
	invalid := []string{
		"",
		"   ",
		"203.0.113.0/24",          // CIDR — not a bare IP
		"203.0.113.7 10.0.0.1",    // multiple addresses
		"example.com",             // hostname
		"203.0.113.7; rm -rf /",   // injection attempt
		"999.999.999.999",
		"0x7f.1",
	}
	for _, in := range invalid {
		if _, err := validateUnbanIP(in); err == nil {
			t.Errorf("validateUnbanIP(%q) = nil error, want rejection", in)
		}
	}
}

func TestSecurityActivity_LocalInstanceRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:instance_id/security/activity",
		handleSecurityActivity(newTestDB(t), "test-jwt-secret", newSecurityActivityCache()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/instances/local/security/activity", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for local instance, got %d", w.Code)
	}
}

func TestSecurityActivity_MissingInstance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:instance_id/security/activity",
		handleSecurityActivity(newTestDB(t), "test-jwt-secret", newSecurityActivityCache()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/instances/inst-none/security/activity", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestSecurityActivity_NoStoredCredentials(t *testing.T) {
	database := newTestDB(t)
	secTestInstance(t, database, "inst-noact", false)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/instances/:instance_id/security/activity",
		handleSecurityActivity(database, "test-jwt-secret", newSecurityActivityCache()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/instances/inst-noact/security/activity", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing credentials, got %d", w.Code)
	}
}

func TestSecurityUnban_RejectsBadIP(t *testing.T) {
	database := newTestDB(t)
	secTestInstance(t, database, "inst-unban", true)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/instances/:instance_id/security/unban",
		handleSecurityUnban(database, "test-jwt-secret", newSecurityActivityCache()))
	body, err := json.Marshal(map[string]string{"ip": "203.0.113.0/24"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/instances/inst-unban/security/unban", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for CIDR input, got %d", w.Code)
	}
}

func TestActivityCache_TTLAndInvalidation(t *testing.T) {
	restore := overrideActivityTTLs(time.Hour, time.Hour)
	defer restore()

	cache := newSecurityActivityCache()
	var calls atomic.Int32
	fetch := func() (ssh.SecurityActivity, error) {
		calls.Add(1)
		return ssh.SecurityActivity{CheckedAt: int64(calls.Load())}, nil
	}

	first := cache.get("inst-x", false, fetch)
	if first.Cached || calls.Load() != 1 {
		t.Fatalf("first get: cached=%v calls=%d", first.Cached, calls.Load())
	}
	second := cache.get("inst-x", false, fetch)
	if !second.Cached || calls.Load() != 1 {
		t.Fatalf("second get should be cached within TTL: cached=%v calls=%d", second.Cached, calls.Load())
	}
	cache.invalidate("inst-x")
	third := cache.get("inst-x", false, fetch)
	if third.Cached || calls.Load() != 2 {
		t.Fatalf("get after invalidate must refetch: cached=%v calls=%d", third.Cached, calls.Load())
	}
}

func TestActivityCache_ErrorNegativeTTL(t *testing.T) {
	restore := overrideActivityTTLs(time.Hour, time.Hour)
	defer restore()

	cache := newSecurityActivityCache()
	var calls atomic.Int32
	fetch := func() (ssh.SecurityActivity, error) {
		calls.Add(1)
		return ssh.SecurityActivity{}, errors.New("ssh down")
	}
	if r := cache.get("inst-err", false, fetch); r.Err == nil {
		t.Fatal("expected the fetch error to surface")
	}
	if r := cache.get("inst-err", false, fetch); !r.Cached || calls.Load() != 1 {
		t.Fatalf("error result must be negative-cached: cached=%v calls=%d", r.Cached, calls.Load())
	}
}

// Force (the card's Refresh button) bypasses the freshness window but still
// shares the fetch with concurrent callers and refreshes the cache.
func TestActivityCache_ForceBypassesFreshness(t *testing.T) {
	restore := overrideActivityTTLs(time.Hour, time.Hour)
	defer restore()

	cache := newSecurityActivityCache()
	var calls atomic.Int32
	fetch := func() (ssh.SecurityActivity, error) {
		calls.Add(1)
		return ssh.SecurityActivity{CheckedAt: int64(calls.Load())}, nil
	}
	cache.get("inst-f", false, fetch)
	second := cache.get("inst-f", true, fetch)
	if second.Cached || calls.Load() != 2 {
		t.Fatalf("forced get must refetch: cached=%v calls=%d", second.Cached, calls.Load())
	}
	if third := cache.get("inst-f", false, fetch); !third.Cached || third.Act.CheckedAt != 2 {
		t.Fatalf("forced result must refresh the cache: cached=%v checkedAt=%d",
			third.Cached, third.Act.CheckedAt)
	}
}

// Single-flight: N concurrent gets during one slow fetch result in exactly
// one fetch call, with everyone receiving the same snapshot.
func TestActivityCache_SingleFlight(t *testing.T) {
	restore := overrideActivityTTLs(time.Hour, time.Hour)
	defer restore()

	cache := newSecurityActivityCache()
	release := make(chan struct{})
	var calls atomic.Int32
	fetch := func() (ssh.SecurityActivity, error) {
		if calls.Add(1) > 1 {
			t.Error("fetch called more than once during single-flight")
		}
		<-release
		return ssh.SecurityActivity{CheckedAt: 42}, nil
	}

	const viewers = 4
	var wg sync.WaitGroup
	results := make([]activityResult, viewers)
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = cache.get("inst-sf", false, fetch)
		}(i)
	}
	// All viewers are now blocked in the fetch (calls == 1); let it finish.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, r := range results {
		if r.Act.CheckedAt != 42 {
			t.Errorf("viewer %d got CheckedAt %d, want the shared snapshot 42", i, r.Act.CheckedAt)
		}
	}
}

func TestActivityCache_ConcurrentInstancesIndependent(t *testing.T) {
	restore := overrideActivityTTLs(time.Hour, time.Hour)
	defer restore()

	cache := newSecurityActivityCache()
	var callsA, callsB atomic.Int32
	fetchA := func() (ssh.SecurityActivity, error) { callsA.Add(1); return ssh.SecurityActivity{CheckedAt: 1}, nil }
	fetchB := func() (ssh.SecurityActivity, error) { callsB.Add(1); return ssh.SecurityActivity{CheckedAt: 2}, nil }

	done := make(chan struct{})
	for i := 0; i < 3; i++ {
		go func() { cache.get("inst-a", false, fetchA); done <- struct{}{} }()
		go func() { cache.get("inst-b", false, fetchB); done <- struct{}{} }()
	}
	for i := 0; i < 6; i++ {
		<-done
	}
	if callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("per-instance single-flight: A=%d B=%d, want 1/1", callsA.Load(), callsB.Load())
	}
}

// overrideActivityTTLs swaps the cache TTL vars for a test and returns a
// restore func (tests run sequentially per package, but stay polite anyway).
func overrideActivityTTLs(ok, errTTL time.Duration) func() {
	oldOk, oldErr := activityTTL, activityErrTTL
	activityTTL, activityErrTTL = ok, errTTL
	return func() { activityTTL, activityErrTTL = oldOk, oldErr }
}
