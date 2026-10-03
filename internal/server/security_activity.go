package server

// Per-server security activity monitoring (fail2ban + firewall): a bounded
// read over the panel's stored SSH credential. SSH is deliberately tight —
// the detail page fetches on demand and the results sit behind a short TTL
// cache with single-flight, so any number of viewers costs at most one SSH
// connection per server per TTL window, and a dead server is re-probed no
// faster than the error TTL.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/registry"
	"github.com/sdldev/dockpal/internal/ssh"
)

var (
	activityTTL    = 60 * time.Second // successful snapshot freshness
	activityErrTTL = 30 * time.Second // negative cache: how long a failed probe suppresses retries
)

type activityEntry struct {
	act       ssh.SecurityActivity
	err       error
	fetchedAt time.Time
}

type activityResult struct {
	Act    ssh.SecurityActivity
	Err    error
	Cached bool
}

// securityActivityCache memoizes one SSH probe per instance. Concurrency is
// two-level: a global mutex guards the maps, a per-instance mutex serializes
// actual fetches (single-flight — the second waiter re-checks the cache and
// is served by the first caller's result).
type securityActivityCache struct {
	mu      sync.Mutex
	entries map[string]*activityEntry
	fetchMu map[string]*sync.Mutex
}

func newSecurityActivityCache() *securityActivityCache {
	return &securityActivityCache{
		entries: map[string]*activityEntry{},
		fetchMu: map[string]*sync.Mutex{},
	}
}

func (c *securityActivityCache) instanceLock(id string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.fetchMu[id]
	if !ok {
		l = &sync.Mutex{}
		c.fetchMu[id] = l
	}
	return l
}

// get returns the cached snapshot when fresh. force skips the freshness read
// (an explicit user refresh) while keeping single-flight and cache update —
// concurrent viewers still share one SSH connection.
func (c *securityActivityCache) get(id string, force bool, fetch func() (ssh.SecurityActivity, error)) activityResult {
	instMu := c.instanceLock(id)
	instMu.Lock()
	defer instMu.Unlock()

	if !force {
		c.mu.Lock()
		if e, ok := c.entries[id]; ok {
			ttl := activityTTL
			if e.err != nil {
				ttl = activityErrTTL
			}
			if time.Since(e.fetchedAt) < ttl {
				c.mu.Unlock()
				return activityResult{Act: e.act, Err: e.err, Cached: true}
			}
		}
		c.mu.Unlock()
	}

	act, err := fetch()
	c.mu.Lock()
	c.entries[id] = &activityEntry{act: act, err: err, fetchedAt: time.Now()}
	c.mu.Unlock()
	return activityResult{Act: act, Err: err}
}

func (c *securityActivityCache) invalidate(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// handleSecurityActivity returns the cached-or-fresh fail2ban + firewall
// snapshot. The response is always 200 (with an "error" field when the probe
// itself failed) so the UI can render last-known/unknown state instead of
// blanking — same contract as handleDetectSecurity.
func handleSecurityActivity(database *db.DB, jwtSecret string, cache *securityActivityCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")
		if id == "local" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the local instance is not managed over SSH"})
			return
		}
		inst, err := database.GetInstance(id)
		if err != nil {
			if errors.Is(err, db.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get instance"})
			return
		}
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive decryption key"})
			return
		}
		authType, secret, err := resolveInstanceSSHCreds(cryptoKey, inst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		host, port, user, err := resolveInstanceSSHTarget(inst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// force=true comes from the card's Refresh button — an explicit user
		// action, so it may bypass the freshness window.
		res := cache.get(id, c.Query("force") == "true", func() (ssh.SecurityActivity, error) {
			return ssh.FetchSecurityActivity(host, port, user, authType, secret, "", io.Discard)
		})
		resp := gin.H{"activity": res.Act, "cached": res.Cached}
		if res.Err != nil {
			resp["error"] = res.Err.Error()
		}
		c.JSON(http.StatusOK, resp)
	}
}

// AuditActionSecurityUnban is the audit action for UI-driven unbans.
const AuditActionSecurityUnban = "instance.security.unban"

// UnbanIPRequest names one banned IP to lift from the sshd jail.
type UnbanIPRequest struct {
	IP string `json:"ip" binding:"required"`
}

// handleSecurityUnban lifts one fail2ban ban synchronously — the remote
// command answers in well under its 8s timeout, so no job/log session is
// warranted. Success and failure both invalidate the activity cache so the
// next read reflects reality.
func handleSecurityUnban(database *db.DB, jwtSecret string, cache *securityActivityCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("instance_id")
		if id == "local" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the local instance is not managed over SSH"})
			return
		}
		var req UnbanIPRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request: %v", err)})
			return
		}
		ip, err := validateUnbanIP(req.IP)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		inst, err := database.GetInstance(id)
		if err != nil {
			if errors.Is(err, db.ErrInstanceNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get instance"})
			return
		}
		cryptoKey, err := registry.DeriveKey(jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive decryption key"})
			return
		}
		authType, secret, err := resolveInstanceSSHCreds(cryptoKey, inst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		host, port, user, err := resolveInstanceSSHTarget(inst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := ssh.UnbanIP(host, port, user, authType, secret, "", ip.String(), io.Discard); err != nil {
			LogAudit(c, database, AuditActionSecurityUnban, inst.Name, "failure",
				fmt.Sprintf(`{"ip":%q,"error":%q}`, ip.String(), err.Error()))
			cache.invalidate(id)
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		LogAudit(c, database, AuditActionSecurityUnban, inst.Name, "success", fmt.Sprintf(`{"ip":%q}`, ip.String()))
		cache.invalidate(id)
		c.JSON(http.StatusOK, gin.H{"message": ip.String() + " has been unbanned from the sshd jail"})
	}
}

// validateUnbanIP accepts only a bare IPv4/IPv6 literal. CIDR notation,
// whitespace inside, hostnames, and anything else fail2ban would reject are
// refused before the value ever reaches a shell argument.
func validateUnbanIP(raw string) (net.IP, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 45 || strings.ContainsAny(s, "/ ") {
		return nil, fmt.Errorf("%q is not a valid IP address", raw)
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("%q is not a valid IP address", raw)
	}
	return ip, nil
}
