package playback

import "sync"

const (
	listenerBufferSize  = 64
	maxConsecutiveDrops = 5
)

// Broadcaster fans a station's transcoded audio bytes out to every
// connected HTTP listener. A listener that can't keep up (its buffer is
// full for maxConsecutiveDrops consecutive writes) is disconnected rather
// than silently corrupted with dropped chunks — its HTTP handler goroutine
// observes the closed channel and ends the response, and the client is
// free to reconnect for a fresh stream.
type Broadcaster struct {
	mu            sync.Mutex
	listeners     map[uint64]*listener
	nextID        uint64
	onCountChange func(count int)
}

type listener struct {
	ch      chan []byte
	dropped int
}

// NewBroadcaster creates an empty Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{listeners: make(map[uint64]*listener)}
}

// OnCountChange registers a callback fired (outside the broadcaster's lock)
// whenever the listener count changes. Only one callback is supported;
// later calls replace earlier ones.
func (b *Broadcaster) OnCountChange(fn func(count int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onCountChange = fn
}

// Subscribe registers a new listener and returns a channel of audio chunks
// for it, plus an unsubscribe function the caller must call exactly once
// (typically via defer) when the listener disconnects.
func (b *Broadcaster) Subscribe() (id uint64, ch <-chan []byte, unsubscribe func()) {
	b.mu.Lock()
	b.nextID++
	id = b.nextID
	l := &listener{ch: make(chan []byte, listenerBufferSize)}
	b.listeners[id] = l
	count := len(b.listeners)
	cb := b.onCountChange
	b.mu.Unlock()

	if cb != nil {
		cb(count)
	}

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.listeners[id]; ok {
				delete(b.listeners, id)
				close(l.ch)
			}
			count := len(b.listeners)
			cb := b.onCountChange
			b.mu.Unlock()
			if cb != nil {
				cb(count)
			}
		})
	}

	return id, l.ch, unsub
}

// Write fans chunk out to every connected listener without blocking on any
// single slow one.
func (b *Broadcaster) Write(chunk []byte) {
	b.mu.Lock()
	evicted := false
	for id, l := range b.listeners {
		select {
		case l.ch <- chunk:
			l.dropped = 0
		default:
			l.dropped++
			if l.dropped >= maxConsecutiveDrops {
				delete(b.listeners, id)
				close(l.ch)
				evicted = true
			}
		}
	}
	count := len(b.listeners)
	cb := b.onCountChange
	b.mu.Unlock()

	if evicted && cb != nil {
		cb(count)
	}
}

// ListenerCount returns the number of currently connected listeners.
func (b *Broadcaster) ListenerCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.listeners)
}
