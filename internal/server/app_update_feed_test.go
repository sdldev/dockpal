package server

import (
	"sync"
	"testing"
	"time"
)

// recvWithTimeout receives a single event from ch with a bounded wait. It
// fails the test if the channel does not deliver within d. It also reports
// whether the channel is open or has been closed.
func recvWithTimeout(t *testing.T, ch <-chan AppUpdateFeedEvent, d time.Duration) (AppUpdateFeedEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(d):
		t.Fatalf("timed out waiting for event after %s", d)
		return AppUpdateFeedEvent{}, false
	}
}

// TestAppUpdateFeed_SubscribeAndReceiveOrdering verifies that events
// published after Subscribe are delivered in the order they were sent.
//
// Validates: Requirements R4.4
func TestAppUpdateFeed_SubscribeAndReceiveOrdering(t *testing.T) {
	feed := NewAppUpdateFeed()

	ch, unsubscribe := feed.Subscribe()
	defer unsubscribe()

	events := []AppUpdateFeedEvent{
		{AttemptID: "a1", App: "demo", Stage: "pulling", At: 1},
		{AttemptID: "a1", App: "demo", Stage: "recreating", At: 2},
		{AttemptID: "a1", App: "demo", Stage: "completed", At: 3},
	}

	for _, ev := range events {
		feed.Publish(ev)
	}

	for i, want := range events {
		got, ok := recvWithTimeout(t, ch, 100*time.Millisecond)
		if !ok {
			t.Fatalf("event %d: channel was closed unexpectedly", i)
		}
		if got != want {
			t.Fatalf("event %d: got %+v, want %+v", i, got, want)
		}
	}

	// No further events should be queued.
	select {
	case extra, ok := <-ch:
		if ok {
			t.Fatalf("unexpected extra event after the published sequence: %+v", extra)
		}
		t.Fatalf("channel closed unexpectedly while still subscribed")
	default:
	}
}

// TestAppUpdateFeed_PublishIsNonBlocking_DropsForSlowSubscriber verifies
// that when a subscriber's buffer is full the publisher does not block and
// that the subscriber's channel only retains up to the buffer size of
// events (the rest are dropped for it).
//
// Validates: Requirements R4.4 (Property 7: Feed non-blocking)
func TestAppUpdateFeed_PublishIsNonBlocking_DropsForSlowSubscriber(t *testing.T) {
	feed := NewAppUpdateFeed()

	// Slow subscriber: subscribe but never read until after publishing.
	slowCh, slowUnsub := feed.Subscribe()
	defer slowUnsub()

	const totalEvents = 100
	const publishDeadline = 100 * time.Millisecond

	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < totalEvents; i++ {
			feed.Publish(AppUpdateFeedEvent{
				AttemptID: "a1",
				App:       "demo",
				Stage:     "pulling",
				At:        int64(i),
			})
		}
	}()

	select {
	case <-done:
		// Publisher finished within the deadline.
	case <-time.After(publishDeadline):
		t.Fatalf("publisher blocked for more than %s; slow subscriber stalled the feed", publishDeadline)
	}
	if elapsed := time.Since(start); elapsed > publishDeadline {
		t.Fatalf("publisher took %s, expected under %s", elapsed, publishDeadline)
	}

	// Drain the slow subscriber's channel. With a buffer of
	// appUpdateFeedBufferSize and totalEvents > buffer, we expect exactly
	// buffer-size events: the first 16 fill the buffer and the remaining
	// 84 are dropped by the select-default branch in Publish.
	drained := 0
drainLoop:
	for {
		select {
		case _, ok := <-slowCh:
			if !ok {
				break drainLoop
			}
			drained++
		case <-time.After(50 * time.Millisecond):
			break drainLoop
		}
	}
	if drained != appUpdateFeedBufferSize {
		t.Fatalf("slow subscriber received %d events, expected exactly %d (buffer size)", drained, appUpdateFeedBufferSize)
	}
}

// TestAppUpdateFeed_MultipleSubscribers_OneSlowDoesNotBlockOthers verifies
// that with two subscribers, a slow one does not deprive the fast one of
// events. The fast subscriber drains promptly (using an ack channel to
// synchronize with the publisher) and must receive every published
// event; the slow subscriber must only buffer up to the per-subscriber
// capacity, with the rest dropped.
//
// Validates: Requirements R4.4
func TestAppUpdateFeed_MultipleSubscribers_OneSlowDoesNotBlockOthers(t *testing.T) {
	feed := NewAppUpdateFeed()

	fastCh, fastUnsub := feed.Subscribe()
	slowCh, slowUnsub := feed.Subscribe()
	defer slowUnsub()

	const totalEvents = 32

	// Fast subscriber drains and acknowledges each event so the publisher
	// can wait for an ack before publishing the next one. This makes the
	// "fast subscriber never falls behind" property deterministic.
	fastReceived := make([]AppUpdateFeedEvent, 0, totalEvents)
	ack := make(chan struct{})
	var fastWG sync.WaitGroup
	fastWG.Add(1)
	go func() {
		defer fastWG.Done()
		for ev := range fastCh {
			fastReceived = append(fastReceived, ev)
			ack <- struct{}{}
		}
	}()

	for i := 0; i < totalEvents; i++ {
		feed.Publish(AppUpdateFeedEvent{
			AttemptID: "a1",
			App:       "demo",
			Stage:     "pulling",
			At:        int64(i),
		})
		// Wait for the fast subscriber to consume this event before
		// publishing the next so its buffer never fills up. The slow
		// subscriber is unaffected because Publish is non-blocking on it.
		select {
		case <-ack:
		case <-time.After(time.Second):
			t.Fatalf("fast subscriber did not ack event %d in time", i)
		}
	}

	fastUnsub()
	fastWG.Wait()

	if got := len(fastReceived); got != totalEvents {
		t.Fatalf("fast subscriber received %d events, expected %d", got, totalEvents)
	}
	for i, ev := range fastReceived {
		if ev.At != int64(i) {
			t.Fatalf("fast subscriber event %d: got At=%d, want %d", i, ev.At, i)
		}
	}

	// The slow subscriber should have buffered exactly the buffer size of
	// events; the rest were dropped.
	drained := 0
drainLoop:
	for {
		select {
		case _, ok := <-slowCh:
			if !ok {
				break drainLoop
			}
			drained++
		case <-time.After(50 * time.Millisecond):
			break drainLoop
		}
	}
	if drained != appUpdateFeedBufferSize {
		t.Fatalf("slow subscriber received %d events, expected %d (buffer size)", drained, appUpdateFeedBufferSize)
	}
}

// TestAppUpdateFeed_Unsubscribe verifies that the function returned by
// Subscribe removes the subscriber and closes its channel, that further
// publishes do not panic or deliver to the closed channel, and that the
// unsubscribe function is idempotent.
//
// Validates: Requirements R4.4
func TestAppUpdateFeed_Unsubscribe(t *testing.T) {
	feed := NewAppUpdateFeed()

	ch, unsubscribe := feed.Subscribe()

	// Sanity: an event is delivered before unsubscribe.
	feed.Publish(AppUpdateFeedEvent{App: "demo", Stage: "pulling", At: 1})
	got, ok := recvWithTimeout(t, ch, 100*time.Millisecond)
	if !ok {
		t.Fatalf("channel closed before unsubscribe")
	}
	if got.Stage != "pulling" {
		t.Fatalf("got stage %q, want %q", got.Stage, "pulling")
	}

	unsubscribe()

	// After unsubscribe the channel must be closed: a receive returns the
	// zero value with ok=false.
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("expected closed channel after unsubscribe, got event %+v", ev)
		}
		if ev != (AppUpdateFeedEvent{}) {
			t.Fatalf("expected zero-value event from closed channel, got %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("channel was not closed after unsubscribe")
	}

	// Publishing more events must not panic and must not deliver to the
	// closed channel (it has been removed from the subscribers map).
	feed.Publish(AppUpdateFeedEvent{App: "demo", Stage: "completed", At: 2})

	// Calling unsubscribe again must be a no-op (must not panic, must not
	// double-close the channel).
	unsubscribe()
}

// TestAppUpdateFeed_NoSubscribers_PublishIsNoop verifies that publishing
// when there are no subscribers does not panic and is a silent no-op.
//
// Validates: Requirements R4.4
func TestAppUpdateFeed_NoSubscribers_PublishIsNoop(t *testing.T) {
	feed := NewAppUpdateFeed()

	// No subscribers attached. Publish must not block, must not panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		feed.Publish(AppUpdateFeedEvent{App: "demo", Stage: "pulling", At: 1})
		feed.Publish(AppUpdateFeedEvent{App: "demo", Stage: "completed", At: 2})
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("Publish blocked when there were no subscribers")
	}
}
