package playback

import (
	"sync"
	"time"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
)

// eventBufferSize is small: events are low-frequency and GetStatus remains
// available as a full-snapshot fallback, so an overflowed/dropped event is
// low-stakes compared to a dropped audio chunk.
const eventBufferSize = 16

// EventBus fans a station's StationEvents out to every SubscribeEvents
// caller.
type EventBus struct {
	mu     sync.Mutex
	subs   map[uint64]chan *audioserverv1.StationEvent
	nextID uint64
}

// NewEventBus creates an empty EventBus.
func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[uint64]chan *audioserverv1.StationEvent)}
}

// Subscribe registers a new subscriber and returns a channel of events for
// it, plus an unsubscribe function the caller must call exactly once
// (typically via defer) when done.
func (b *EventBus) Subscribe() (id uint64, ch <-chan *audioserverv1.StationEvent, unsubscribe func()) {
	b.mu.Lock()
	b.nextID++
	id = b.nextID
	c := make(chan *audioserverv1.StationEvent, eventBufferSize)
	b.subs[id] = c
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(c)
			}
			b.mu.Unlock()
		})
	}

	return id, c, unsub
}

// CloseAll disconnects every currently connected subscriber by closing
// their channels. Used when a station is unregistered (see Station.Stop).
func (b *EventBus) CloseAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, c := range b.subs {
		delete(b.subs, id)
		close(c)
	}
}

// Publish fans e out to every subscriber, dropping it for any subscriber
// whose buffer is currently full rather than blocking the publisher.
func (b *EventBus) Publish(e *audioserverv1.StationEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.subs {
		select {
		case c <- e:
		default:
		}
	}
}

func newEvent(slug string, typ audioserverv1.EventType) *audioserverv1.StationEvent {
	return &audioserverv1.StationEvent{
		Slug:            slug,
		Type:            typ,
		TimestampUnixMs: time.Now().UnixMilli(),
	}
}

func newTrackStartedEvent(slug string, item *QueuedItem) *audioserverv1.StationEvent {
	e := newEvent(slug, audioserverv1.EventType_EVENT_TYPE_TRACK_STARTED)
	e.Payload = &audioserverv1.StationEvent_TrackStarted{
		TrackStarted: &audioserverv1.TrackStartedPayload{
			QueueId:         item.ID,
			Source:          item.Source,
			DurationSeconds: item.DurationSeconds(),
		},
	}
	return e
}

func newTrackEndedEvent(slug, queueID, reason string) *audioserverv1.StationEvent {
	e := newEvent(slug, audioserverv1.EventType_EVENT_TYPE_TRACK_ENDED)
	e.Payload = &audioserverv1.StationEvent_TrackEnded{
		TrackEnded: &audioserverv1.TrackEndedPayload{
			QueueId: queueID,
			Reason:  reason,
		},
	}
	return e
}

func newQueueUpdatedEvent(slug string, length int) *audioserverv1.StationEvent {
	e := newEvent(slug, audioserverv1.EventType_EVENT_TYPE_QUEUE_UPDATED)
	e.Payload = &audioserverv1.StationEvent_QueueUpdated{
		QueueUpdated: &audioserverv1.QueueUpdatedPayload{QueueLength: int32(length)},
	}
	return e
}

func newQueueLowEvent(slug string, length int, threshold int32) *audioserverv1.StationEvent {
	e := newEvent(slug, audioserverv1.EventType_EVENT_TYPE_QUEUE_LOW)
	e.Payload = &audioserverv1.StationEvent_QueueLow{
		QueueLow: &audioserverv1.QueueLowPayload{QueueLength: int32(length), Threshold: threshold},
	}
	return e
}

func newListenerCountEvent(slug string, count int) *audioserverv1.StationEvent {
	e := newEvent(slug, audioserverv1.EventType_EVENT_TYPE_LISTENER_COUNT_CHANGED)
	e.Payload = &audioserverv1.StationEvent_ListenerCountChanged{
		ListenerCountChanged: &audioserverv1.ListenerCountChangedPayload{ListenerCount: int64(count)},
	}
	return e
}

func newErrorEvent(slug, message, code string) *audioserverv1.StationEvent {
	e := newEvent(slug, audioserverv1.EventType_EVENT_TYPE_ERROR)
	e.Payload = &audioserverv1.StationEvent_Error{
		Error: &audioserverv1.ErrorPayload{Message: message, Code: code},
	}
	return e
}

func newSilenceStartedEvent(slug string) *audioserverv1.StationEvent {
	return newEvent(slug, audioserverv1.EventType_EVENT_TYPE_SILENCE_STARTED)
}

func newSilenceEndedEvent(slug string) *audioserverv1.StationEvent {
	return newEvent(slug, audioserverv1.EventType_EVENT_TYPE_SILENCE_ENDED)
}
