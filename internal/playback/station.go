package playback

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Station holds one registered station's live state: its queue, listener
// fan-out, event bus, and (once running, see player.go) currently playing
// item. Registration is in-memory/ephemeral — a server restart clears it
// and controllers are expected to re-register.
type Station struct {
	Slug         string
	RegisteredAt time.Time

	Queue       *Queue
	Broadcaster *Broadcaster
	Events      *EventBus

	mu          sync.RWMutex
	name        string
	description string

	current   atomic.Pointer[QueuedItem]
	isSilence atomic.Bool
	// currentStartedAtUnixNano is 0 while playing silence (no current
	// item), otherwise the moment SetCurrent(item) was last called with a
	// non-nil item -- used to compute CurrentElapsedSeconds for GetStatus.
	currentStartedAtUnixNano atomic.Int64

	// lowQueueThreshold and wasQueueLow implement the edge-triggered
	// EVENT_TYPE_QUEUE_LOW event: 0 disables it, otherwise QueueChanged
	// fires it once when the queue length drops to or below the
	// threshold, and won't fire again until it rises back above and dips.
	lowQueueThreshold atomic.Int32
	wasQueueLow       atomic.Bool

	// streamCancelMu guards streamCancel, which cancels whichever
	// streamFile call (see player.go) is currently in flight, if any. It is
	// set at the start of each streamFile call and cleared when it returns,
	// so Interrupt() only ever affects the clip actually playing at the
	// moment it's called — never a clip queued moments later.
	streamCancelMu sync.Mutex
	streamCancel   context.CancelFunc

	// runCancelMu guards runCancel, which cancels the ctx passed to Run
	// (see player.go), stopping the player goroutine entirely. Set by
	// whoever starts the goroutine (registry.Register's onNew callback);
	// used by Stop() on unregistration.
	runCancelMu sync.Mutex
	runCancel   context.CancelFunc

	// historyMu guards history, the bounded list of most recently finished
	// queue items (see RecordHistory/History). Written by the player
	// goroutine (player.go), read by GetStatus (grpcapi).
	historyMu sync.Mutex
	history   []*HistoryEntry
}

// historyMaxEntries caps how many finished items Station.History retains --
// enough to seed a dashboard's recently-played view on load, not a full
// play log (see GetStatusResponse.history's doc: keep it current afterward
// from TRACK_ENDED events instead of re-polling).
const historyMaxEntries = 20

// HistoryEntry is one finished queue item, as recorded by RecordHistory.
type HistoryEntry struct {
	Item    *QueuedItem
	Reason  string // "completed" or "interrupted", same as TrackEndedPayload.Reason
	EndedAt time.Time
}

// NewStation constructs a Station and wires its Broadcaster's listener
// count changes into its EventBus. The player goroutine is started
// separately (see player.go / registry.Register).
func NewStation(slug, name, description string) *Station {
	s := &Station{
		Slug:         slug,
		name:         name,
		description:  description,
		RegisteredAt: time.Now(),
		Queue:        NewQueue(),
		Broadcaster:  NewBroadcaster(),
		Events:       NewEventBus(),
	}
	s.isSilence.Store(true)
	s.Broadcaster.OnCountChange(func(count int) {
		s.Events.Publish(newListenerCountEvent(slug, count))
	})
	return s
}

// SetMetadata updates the station's display name/description/low-queue
// threshold in place, used both on first registration and when a
// controller re-registers an already-running station.
func (s *Station) SetMetadata(name, description string, lowQueueThreshold int32) {
	s.mu.Lock()
	s.name = name
	s.description = description
	s.mu.Unlock()
	s.lowQueueThreshold.Store(lowQueueThreshold)
}

func (s *Station) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

func (s *Station) Description() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.description
}

// Current returns the currently playing queue item, or nil if the station
// is playing silence.
func (s *Station) Current() *QueuedItem {
	return s.current.Load()
}

func (s *Station) IsSilence() bool {
	return s.isSilence.Load()
}

// Interrupt cancels whichever clip the player goroutine is currently
// streaming, if any, so it can move on to the new front-of-queue item
// (QUEUE_MODE_PLAY_NOW_INTERRUPT). If nothing is currently streaming
// (e.g. the player is between queue items), this is a no-op — the pushed
// item will simply play next regardless.
func (s *Station) Interrupt() {
	s.streamCancelMu.Lock()
	cancel := s.streamCancel
	s.streamCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// setStreamCancel registers (or clears, via nil) the cancel func for the
// clip currently being streamed. Only player.go's streamFile calls this.
func (s *Station) setStreamCancel(cancel context.CancelFunc) {
	s.streamCancelMu.Lock()
	s.streamCancel = cancel
	s.streamCancelMu.Unlock()
}

// SetRunCancel registers the cancel func for this station's Run goroutine,
// so a later Stop() call can shut it down. The caller that starts the
// goroutine (e.g. StationStarter) is responsible for calling this with the
// cancel func of the context it passed to Run.
func (s *Station) SetRunCancel(cancel context.CancelFunc) {
	s.runCancelMu.Lock()
	s.runCancel = cancel
	s.runCancelMu.Unlock()
}

// Stop cancels this station's Run goroutine, if one was started (see
// SetRunCancel), causing it to exit after its current chunk/clip, and
// disconnects every currently connected HTTP listener and SubscribeEvents
// stream so they don't hang open indefinitely against a station that will
// never produce audio or events again.
func (s *Station) Stop() {
	s.runCancelMu.Lock()
	cancel := s.runCancel
	s.runCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.Broadcaster.CloseAll()
	s.Events.CloseAll()
}

func (s *Station) Uptime() time.Duration {
	return time.Since(s.RegisteredAt)
}

// The PublishX methods below are the sole way other packages (grpcapi,
// the player loop, transcode prefetch) raise events for this station,
// keeping StationEvent's wire shape encapsulated in this package.

// QueueChanged publishes QUEUE_UPDATED, and additionally publishes the
// edge-triggered QUEUE_LOW event if a low-queue threshold is configured
// (see RegisterStationRequest.low_queue_threshold) and the queue length
// just crossed at or below it. Call this after any operation that changes
// the queue's length: QueueTrack, RemoveFromQueue, ClearQueue, and the
// player consuming an item.
func (s *Station) QueueChanged() {
	length := s.Queue.Len()
	s.Events.Publish(newQueueUpdatedEvent(s.Slug, length))

	threshold := s.lowQueueThreshold.Load()
	if threshold <= 0 {
		return
	}

	isLow := length <= int(threshold)
	wasLow := s.wasQueueLow.Swap(isLow)
	if isLow && !wasLow {
		s.Events.Publish(newQueueLowEvent(s.Slug, length, threshold))
	}
}

func (s *Station) PublishTrackStarted(item *QueuedItem) {
	s.Events.Publish(newTrackStartedEvent(s.Slug, item))
}

func (s *Station) PublishTrackEnded(queueID, reason string) {
	s.Events.Publish(newTrackEndedEvent(s.Slug, queueID, reason))
}

// RecordHistory appends a finished queue item to this station's bounded
// history, evicting the oldest entry past historyMaxEntries. Only
// player.go's Run loop calls this, right after a track ends.
func (s *Station) RecordHistory(item *QueuedItem, reason string) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	s.history = append(s.history, &HistoryEntry{Item: item, Reason: reason, EndedAt: time.Now()})
	if len(s.history) > historyMaxEntries {
		s.history = s.history[len(s.history)-historyMaxEntries:]
	}
}

// History returns a copy of the most recently finished queue items, oldest
// first, for GetStatus.
func (s *Station) History() []*HistoryEntry {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	out := make([]*HistoryEntry, len(s.history))
	copy(out, s.history)
	return out
}

func (s *Station) PublishError(message, code string) {
	s.Events.Publish(newErrorEvent(s.Slug, message, code))
}

func (s *Station) PublishSilenceStarted() {
	s.Events.Publish(newSilenceStartedEvent(s.Slug))
}

func (s *Station) PublishSilenceEnded() {
	s.Events.Publish(newSilenceEndedEvent(s.Slug))
}

// SetCurrent updates the currently playing item (nil while playing silence)
// and the silence flag together.
func (s *Station) SetCurrent(item *QueuedItem) {
	s.current.Store(item)
	s.isSilence.Store(item == nil)
	if item != nil {
		s.currentStartedAtUnixNano.Store(time.Now().UnixNano())
	} else {
		s.currentStartedAtUnixNano.Store(0)
	}
}

// CurrentElapsedSeconds is how long the current item has been playing, or
// 0 while playing silence (no current item).
func (s *Station) CurrentElapsedSeconds() int64 {
	startedAt := s.currentStartedAtUnixNano.Load()
	if startedAt == 0 {
		return 0
	}
	return int64(time.Since(time.Unix(0, startedAt)).Seconds())
}
