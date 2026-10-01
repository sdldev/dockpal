package server

// WS tickets: short-lived, single-use tokens for WebSocket upgrades.
//
// Browser WS handshakes can't set headers, so the JWT otherwise rides in the
// URL query string (?token=) and lands in reverse-proxy access logs and
// browser history (audit-auth L1). A ticket is accepted in the same ?token=
// slot but expires after 60s, carries the issuing user's identity, and is
// consumed on first use — a leaked ticket is almost always worthless.

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const wsTicketTTL = 60 * time.Second

type wsTicket struct {
	userID   string
	username string
	role     string
	expires  time.Time
}

var wsTickets = struct {
	mu      sync.Mutex
	entries map[string]wsTicket // sha256(ticket) → ticket data
}{entries: make(map[string]wsTicket)}

// handleWSTicket issues a single-use WS ticket for the authenticated caller.
// Registered under baseProtected (any authenticated user).
func handleWSTicket(c *gin.Context) {
	ticket, err := randomHex(32)
	if err != nil {
		internalError(c, err)
		return
	}
	sum := sha256.Sum256([]byte(ticket))
	wsTickets.mu.Lock()
	wsTickets.entries[hex.EncodeToString(sum[:])] = wsTicket{
		userID:   c.GetString("user_id"),
		username: c.GetString("username"),
		role:     c.GetString("role"),
		expires:  time.Now().Add(wsTicketTTL),
	}
	wsTickets.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"ticket": ticket, "expires_in": int(wsTicketTTL.Seconds())})
}

// consumeWSTicket atomically validates and consumes a ticket: it must exist,
// be unexpired, and is deleted on use. Expired entries are swept lazily.
func consumeWSTicket(ticket string) (wsTicket, bool) {
	sum := sha256.Sum256([]byte(ticket))
	key := hex.EncodeToString(sum[:])
	now := time.Now()
	wsTickets.mu.Lock()
	defer wsTickets.mu.Unlock()
	// Lazy sweep so the map can't grow unboundedly from never-used tickets.
	if len(wsTickets.entries) > 1024 {
		for k, v := range wsTickets.entries {
			if now.After(v.expires) {
				delete(wsTickets.entries, k)
			}
		}
	}
	entry, ok := wsTickets.entries[key]
	if !ok {
		return wsTicket{}, false
	}
	delete(wsTickets.entries, key)
	if now.After(entry.expires) {
		return wsTicket{}, false
	}
	return entry, true
}
