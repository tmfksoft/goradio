// Package playback is the audio server's per-station playback engine: the
// queue, player, listener fan-out, and event bus. It is deliberately named
// "playback" rather than "station" to avoid clashing with the unrelated
// Lua "station controller" concept in internal/luastation.
package playback

import (
	"sync"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
)

// QueuedItem is one entry in a station's playback queue. It is created
// immediately on QueueTrack and its Ready channel closes once the
// transcode/prefetch job (internal/transcode) has resolved a locally
// playable file, so downloads/transcodes finish well ahead of playback.
type QueuedItem struct {
	ID         string
	Source     *audioserverv1.TrackSource
	Mode       audioserverv1.QueueMode
	Transition audioserverv1.Transition

	ready           chan struct{}
	localPath       string
	durationSeconds int64
	isLive          bool
	liveURL         string
	err             error
}

// NewQueuedItem creates a not-yet-ready queue item. Call MarkReady once
// prefetch/transcode completes.
func NewQueuedItem(id string, source *audioserverv1.TrackSource, mode audioserverv1.QueueMode, transition audioserverv1.Transition) *QueuedItem {
	return &QueuedItem{
		ID:         id,
		Source:     source,
		Mode:       mode,
		Transition: transition,
		ready:      make(chan struct{}),
	}
}

// MarkReady completes prefetch for this item: either localPath is set to
// the cached, transcoded audio file to play (with its duration, computed
// from the fixed-CBR cached file's size), or err explains why not.
func (q *QueuedItem) MarkReady(localPath string, durationSeconds int64, err error) {
	q.localPath = localPath
	q.durationSeconds = durationSeconds
	q.err = err
	close(q.ready)
}

// MarkLive completes prefetch for this item as a live stream (auto-
// detected — see audiosource.Resolve): it bypasses the transcode cache
// entirely and is relayed continuously by the player instead, rather than
// played from a cached file.
func (q *QueuedItem) MarkLive(url string) {
	q.isLive = true
	q.liveURL = url
	close(q.ready)
}

// Ready is closed once prefetch has completed (successfully or not).
func (q *QueuedItem) Ready() <-chan struct{} { return q.ready }

// LocalPath returns the cached, transcoded file to play. Only valid after
// Ready is closed, Err is nil, and IsLive is false.
func (q *QueuedItem) LocalPath() string { return q.localPath }

// DurationSeconds is the track's length, or 0 if unknown/indefinite (a
// live relay, or an item whose prefetch hasn't finished yet).
func (q *QueuedItem) DurationSeconds() int64 { return q.durationSeconds }

// IsLive reports whether this item is a live stream to relay continuously
// rather than a finite cached file. Only valid after Ready is closed.
func (q *QueuedItem) IsLive() bool { return q.isLive }

// LiveURL is the upstream URL to relay. Only valid when IsLive is true.
func (q *QueuedItem) LiveURL() string { return q.liveURL }

// Err explains why prefetch failed, if it did. Only valid after Ready is
// closed.
func (q *QueuedItem) Err() error { return q.err }

// Queue is a station's thread-safe FIFO of QueuedItems.
type Queue struct {
	mu    sync.Mutex
	items []*QueuedItem
}

// NewQueue creates an empty queue.
func NewQueue() *Queue {
	return &Queue{}
}

// Append adds item to the end of the queue (QUEUE_MODE_APPEND).
func (q *Queue) Append(item *QueuedItem) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, item)
	return len(q.items) - 1
}

// PushFront adds item to the front of the queue (QUEUE_MODE_PLAY_NEXT and
// QUEUE_MODE_PLAY_NOW_INTERRUPT; the latter additionally requires signaling
// the player to abandon its in-flight clip, which callers do separately via
// Station.Interrupt).
func (q *Queue) PushFront(item *QueuedItem) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append([]*QueuedItem{item}, q.items...)
}

// PopFront removes and returns the item at the front of the queue, if any.
func (q *Queue) PopFront() (*QueuedItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

// Remove removes the pending item with the given id, if present. It cannot
// remove whatever the player has already popped and is currently playing.
func (q *Queue) Remove(queueID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, item := range q.items {
		if item.ID == queueID {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return true
		}
	}
	return false
}

// SkipTo drops every pending item ahead of queueID, making it the new
// front of the queue, and returns how many items were dropped. found is
// false (a no-op) if queueID isn't a pending item.
func (q *Queue) SkipTo(queueID string) (removedCount int, found bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, item := range q.items {
		if item.ID == queueID {
			q.items = q.items[i:]
			return i, true
		}
	}
	return 0, false
}

// Clear removes every pending item and returns how many were removed.
func (q *Queue) Clear() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.items)
	q.items = nil
	return n
}

// Snapshot returns a copy of the current queue contents, for GetStatus.
func (q *Queue) Snapshot() []*QueuedItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*QueuedItem, len(q.items))
	copy(out, q.items)
	return out
}

// Len returns the current queue length.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
