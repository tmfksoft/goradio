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
}
