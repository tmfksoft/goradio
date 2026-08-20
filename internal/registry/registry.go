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
func (r *Registry) Register(slug, name, description string, onNew func(*playback.Station)) (st *playback.Station, reRegistered bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.stations[slug]; ok {
		existing.SetMetadata(name, description)
		return existing, true
	}

	st = playback.NewStation(slug, name, description)
	r.stations[slug] = st
	if onNew != nil {
		onNew(st)
	}
	return st, false
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
