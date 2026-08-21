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
	logoURL     string

	current   atomic.Pointer[QueuedItem]
	isSilence atomic.Bool

	// playCtrlMu guards the fields below, which together implement
	// mid-track Pause/Resume/Seek/SeekBy without disrupting the shared
	// broadcast stream itself (see player.go's playLocalItem): a paused
	// station plays looping silence while holding its exact byte position
	// in the current item's file, resuming from there once unpaused.
	//
	//   - paused / positionOffsetSeconds / positionAnchor together track
	//     the current item's playback position: positionOffsetSeconds is
	//     the position as of positionAnchor, which only advances in real
	//     time while !paused (see currentPositionSeconds).
	//   - pendingSeekSeconds, if non-nil, is a seek playLocalItem hasn't
	//     applied yet (consumed by consumeSeekOffsetBytes).
	//   - skipRequested is set by Interrupt (the "genuine skip" signal --
	//     Skip/SkipTo/ClearQueue/QueueTrack's PLAY_NOW_INTERRUPT) so
	//     playLocalItem can tell a real skip apart from its own
	//     pause/seek-driven use of the same underlying stream
	//     cancellation, even while already paused.
	playCtrlMu            sync.Mutex
	paused                bool
	positionOffsetSeconds int64
	positionAnchor        time.Time
	pendingSeekSeconds    *int64
	skipRequested         bool

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

// SetMetadata updates the station's display name/description/logo
// URL/low-queue threshold in place, used both on first registration and
// when a controller re-registers an already-running station. Every field
// is fully replaced, not merged -- re-registering without a value resets
// it (e.g. omitting logoURL clears a previously set logo), same as
// name/description already do.
func (s *Station) SetMetadata(name, description, logoURL string, lowQueueThreshold int32) {
	s.mu.Lock()
	s.name = name
	s.description = description
	s.logoURL = logoURL
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

func (s *Station) LogoURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.logoURL
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
//
// This is the "genuine skip" signal (used by Skip, SkipTo, ClearQueue,
// and QueueTrack's PLAY_NOW_INTERRUPT): it unconditionally clears any
// pause/pending-seek state too, so an interrupted track actually ends
// even if it happened to be paused or mid-seek at the time. Pause/Seek
// use the lower-level cancelCurrentStream instead, precisely so they
// don't trip this.
func (s *Station) Interrupt() {
	s.playCtrlMu.Lock()
	s.paused = false
	s.pendingSeekSeconds = nil
	s.skipRequested = true
	s.playCtrlMu.Unlock()

	s.cancelCurrentStream()
}

// cancelCurrentStream cancels whichever streamFile/streamLiveRelay call is
// currently in flight, if any -- the low-level primitive shared by
// Interrupt, Pause, and Seek/SeekBy, which layer their own intent on top
// via the playCtrlMu-guarded fields.
func (s *Station) cancelCurrentStream() {
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

// IsPaused reports whether the station is currently paused -- playing
// silence in place of its current track, which keeps its exact position
// held until Resume.
func (s *Station) IsPaused() bool {
	s.playCtrlMu.Lock()
	defer s.playCtrlMu.Unlock()
	return s.paused
}

// Pause pauses the current track in place: the station falls back to the
// silence loop, same as an empty queue, until Resume. Returns false (a
// no-op) if nothing is playing, the current item is a live relay (no
// fixed position to hold -- it can only be skipped, not paused), or it's
// already paused.
func (s *Station) Pause() bool {
	cur := s.Current()
	if cur == nil || cur.IsLive() {
		return false
	}

	s.playCtrlMu.Lock()
	if s.paused {
		s.playCtrlMu.Unlock()
		return false
	}
	s.positionOffsetSeconds += int64(time.Since(s.positionAnchor).Seconds())
	s.paused = true
	s.playCtrlMu.Unlock()

	s.cancelCurrentStream()
	return true
}

// Resume resumes a paused track from exactly where it was paused. Returns
// false if the station wasn't paused.
func (s *Station) Resume() bool {
	s.playCtrlMu.Lock()
	if !s.paused {
		s.playCtrlMu.Unlock()
		return false
	}
	s.paused = false
	s.positionAnchor = time.Now()
	s.playCtrlMu.Unlock()
	return true
}

// SeekPosition jumps the current track to an absolute position, clamped
// to [0, duration] (unclamped at the top if duration is unknown/0, e.g. a
// not-yet-ready item). Returns false (not an error) if nothing seekable
// is playing -- no current item, or it's a live relay (no fixed position
// to seek within). Works the same whether or not the station is
// currently paused: seeking while paused just moves where Resume will
// pick up.
//
// Named SeekPosition rather than Seek so `go vet` doesn't mistake it for
// an io.Seeker-shaped method (it checks any method literally named Seek
// against io.Seeker's signature).
func (s *Station) SeekPosition(positionSeconds int64) (applied bool, resultSeconds int64) {
	cur := s.Current()
	if cur == nil || cur.IsLive() {
		return false, 0
	}
	if positionSeconds < 0 {
		positionSeconds = 0
	}
	if d := cur.DurationSeconds(); d > 0 && positionSeconds > d {
		positionSeconds = d
	}

	s.playCtrlMu.Lock()
	s.pendingSeekSeconds = &positionSeconds
	s.positionOffsetSeconds = positionSeconds
	s.positionAnchor = time.Now()
	paused := s.paused
	s.playCtrlMu.Unlock()

	if !paused {
		s.cancelCurrentStream()
	}
	return true, positionSeconds
}

// SeekBy jumps the current track by a signed delta from its current
// position (positive = forward, negative = backward). See SeekPosition.
func (s *Station) SeekBy(deltaSeconds int64) (applied bool, resultSeconds int64) {
	cur := s.Current()
	if cur == nil || cur.IsLive() {
		return false, 0
	}
	return s.SeekPosition(s.currentPositionSeconds() + deltaSeconds)
}

// currentPositionSeconds returns how far into the current item playback
// has reached, accounting for time spent paused and any seeks.
func (s *Station) currentPositionSeconds() int64 {
	s.playCtrlMu.Lock()
	defer s.playCtrlMu.Unlock()
	pos := s.positionOffsetSeconds
	if !s.paused {
		pos += int64(time.Since(s.positionAnchor).Seconds())
	}
	return pos
}

// consumeSeekOffsetBytes returns the pending seek's byte offset (and
// clears it) if one is pending, for playLocalItem (player.go) to apply
// once the in-flight streamFile call it cancelled returns.
// bytesPerSecond converts the pending position (stored in seconds) to a
// byte offset -- exact, since every cached clip is fixed CBR.
func (s *Station) consumeSeekOffsetBytes(bytesPerSecond int) (offsetBytes int64, ok bool) {
	s.playCtrlMu.Lock()
	defer s.playCtrlMu.Unlock()
	if s.pendingSeekSeconds == nil {
		return 0, false
	}
	offsetBytes = *s.pendingSeekSeconds * int64(bytesPerSecond)
	s.pendingSeekSeconds = nil
	return offsetBytes, true
}

// hasPendingSeek reports whether a seek is waiting to be applied, without
// consuming it -- used by playLocalItem's pause-hold loop to break out of
// the silence clip early when a seek arrives while paused.
func (s *Station) hasPendingSeek() bool {
	s.playCtrlMu.Lock()
	defer s.playCtrlMu.Unlock()
	return s.pendingSeekSeconds != nil
}

// consumeSkipRequested reports (and clears) whether Interrupt was called
// since the last check. playLocalItem needs this because Interrupt also
// clears paused/pendingSeekSeconds (so a real skip actually ends a paused
// or mid-seek track), which would otherwise look identical to "nothing to
// do" if playLocalItem only looked at those two fields.
func (s *Station) consumeSkipRequested() bool {
	s.playCtrlMu.Lock()
	defer s.playCtrlMu.Unlock()
	if !s.skipRequested {
		return false
	}
	s.skipRequested = false
	return true
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
// and the silence flag together, and resets position tracking (see
// currentPositionSeconds) for the new item.
func (s *Station) SetCurrent(item *QueuedItem) {
	s.current.Store(item)
	s.isSilence.Store(item == nil)

	s.playCtrlMu.Lock()
	s.paused = false
	s.pendingSeekSeconds = nil
	s.positionOffsetSeconds = 0
	s.positionAnchor = time.Now()
	s.playCtrlMu.Unlock()
}

// CurrentElapsedSeconds is how long the current item has been playing,
// accounting for time spent paused and any seeks, or 0 while playing
// silence (no current item).
func (s *Station) CurrentElapsedSeconds() int64 {
	if s.Current() == nil {
		return 0
	}
	return s.currentPositionSeconds()
}
