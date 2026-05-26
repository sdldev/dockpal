package server

import "sync"

// AppUpdateFeedEvent is a single event broadcast on the App_Update_Feed.
// It mirrors the JSON shape consumed by the UI over SSE.
type AppUpdateFeedEvent struct {
	AttemptID  string `json:"attempt_id"`
	InstanceID string `json:"instance_id"`
	App        string `json:"app"`
	Stage      string `json:"stage"`
	ErrorCode  string `json:"error_code,omitempty"`
	Message    string `json:"message,omitempty"`
	At         int64  `json:"at"`
}

// AppUpdateFeed is a fan-out broadcaster for AppUpdateFeedEvent values.
//
// Publish is non-blocking: each subscriber owns a buffered channel and an
// event is dropped for any subscriber whose buffer is full. This guarantees
// the publisher (the AutoUpdateWorker) is never stalled by a slow SSE client.
//
// Subscribers receive events through a read-only channel returned by
// Subscribe. The unsubscribe function removes the subscription under the
// write lock and closes the channel; closing on unsubscribe lets receivers
// detect the end of the stream via a `range` loop or a closed-channel check.
type AppUpdateFeed struct {
	mu   sync.RWMutex
	subs map[chan AppUpdateFeedEvent]struct{}
}

// appUpdateFeedBufferSize is the per-subscriber buffer size. When a
// subscriber falls behind by more than this, events are dropped for it.
const appUpdateFeedBufferSize = 16

// NewAppUpdateFeed returns a ready-to-use AppUpdateFeed with no subscribers.
func NewAppUpdateFeed() *AppUpdateFeed {
	return &AppUpdateFeed{
		subs: make(map[chan AppUpdateFeedEvent]struct{}),
	}
}

// Publish delivers ev to every current subscriber without blocking. If a
// subscriber's buffer is full, the event is dropped for that subscriber.
func (f *AppUpdateFeed) Publish(ev AppUpdateFeedEvent) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for sub := range f.subs {
		select {
		case sub <- ev:
		default:
			// Buffer full: drop the event for this subscriber. The UI is
			// expected to re-fetch full state via GET /apps/:name/updates
			// when it needs to recover.
		}
	}
}

// Subscribe registers a new subscriber and returns its receive channel along
// with an unsubscribe function. Calling the unsubscribe function more than
// once is safe; subsequent calls are no-ops. After unsubscribe returns the
// channel is closed.
func (f *AppUpdateFeed) Subscribe() (<-chan AppUpdateFeedEvent, func()) {
	ch := make(chan AppUpdateFeedEvent, appUpdateFeedBufferSize)

	f.mu.Lock()
	f.subs[ch] = struct{}{}
	f.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			f.mu.Lock()
			if _, ok := f.subs[ch]; ok {
				delete(f.subs, ch)
				close(ch)
			}
			f.mu.Unlock()
		})
	}
	return ch, unsubscribe
}
