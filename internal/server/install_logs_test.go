package server

import (
	"sync"
	"testing"
)

// TestInstallLogsBroadcastConcurrentWithDeregister exercises the race between
// WriteLog broadcasting to a listener and that listener deregistering (which
// closes the channel). Sending outside the session lock used to panic with
// "send on closed channel", crashing the whole process. Run with -race.
func TestInstallLogsBroadcastConcurrentWithDeregister(t *testing.T) {
	const instanceID = "inst-race"

	// One shared manager: the race only occurs when a broadcaster and a
	// deregistering listener touch the same LogSession.
	mgr := NewInstallLogsManager()

	var wg sync.WaitGroup
	const workers = 8
	const cycles = 300

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < cycles; i++ {
				mgr.WriteLogf(instanceID, "line %d", i)
			}
		}()
	}

	// Listeners register and deregister continuously, interleaving close(ch)
	// with the broadcasters' sends.
	for l := 0; l < workers; l++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < cycles; i++ {
				ch, _, deregister := mgr.RegisterListener(instanceID)
				// Drain a little so buffered sends do not always block.
				select {
				case <-ch:
				default:
				}
				deregister()
			}
		}()
	}

	wg.Wait()
}

// TestInstallLogsDeregisterRemovesListener checks the deregister contract:
// after deregistering, a subsequent broadcast must not target the removed
// channel, and history is still readable.
func TestInstallLogsDeregisterRemovesListener(t *testing.T) {
	mgr := NewInstallLogsManager()
	mgr.WriteLog("inst-1", "hello")

	ch, history, deregister := mgr.RegisterListener("inst-1")
	if len(history) != 1 || history[0] != "hello" {
		t.Fatalf("expected pre-register history, got %v", history)
	}

	deregister()

	// After deregister the channel is closed and no longer in the listener set.
	mgr.WriteLog("inst-1", "world")
	select {
	case msg, ok := <-ch:
		if ok {
			t.Fatalf("deregistered channel should not receive %q", msg)
		}
	default:
		t.Fatal("deregistered channel should be closed")
	}

	// A fresh listener still receives the full history, including later writes.
	_, history2, _ := mgr.RegisterListener("inst-1")
	if len(history2) != 2 {
		t.Fatalf("expected 2 history lines, got %d", len(history2))
	}
}
