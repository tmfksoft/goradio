// Package registry is the audio server's in-memory station registry.
package registry

import (
	"sync"

	"github.com/tmfksoft/goradio/internal/playback"
)

// Registry maps station slugs to their live playback state. It is the only
// mutating entry point for station registration; a station's own queue,
// player, and broadcaster manage their own internal state independently
// once registered.
type Registry struct {
	mu       sync.RWMutex
	stations map[string]*playback.Station
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{stations: make(map[string]*playback.Station)}
}

// Register registers slug if it's new, starting its player goroutine via
// onNew, or updates the metadata of an already-registered slug in place
// (supporting controller reconnect/retry without disrupting playback).
// reRegistered reports which case occurred.
func (r *Registry) Register(slug, name, description, logoURL string, lowQueueThreshold int32, onNew func(*playback.Station)) (st *playback.Station, reRegistered bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.stations[slug]; ok {
		existing.SetMetadata(name, description, logoURL, lowQueueThreshold)
		return existing, true
	}

	st = playback.NewStation(slug, name, description)
	st.SetMetadata(name, description, logoURL, lowQueueThreshold)
	r.stations[slug] = st
	if onNew != nil {
		onNew(st)
	}
	return st, false
}

// Unregister removes slug from the registry, if present, and returns the
// station that was removed. It does not stop the station's player
// goroutine itself -- callers are expected to call Station.Stop() on the
// returned station.
func (r *Registry) Unregister(slug string) (*playback.Station, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.stations[slug]
	if !ok {
		return nil, false
	}
	delete(r.stations, slug)
	return st, true
}

// Get returns the station registered under slug, if any.
func (r *Registry) Get(slug string) (*playback.Station, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st, ok := r.stations[slug]
	return st, ok
}

// List returns every currently registered station.
func (r *Registry) List() []*playback.Station {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*playback.Station, 0, len(r.stations))
	for _, st := range r.stations {
		out = append(out, st)
	}
	return out
}
