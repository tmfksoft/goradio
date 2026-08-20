// Package httpapi is the audio server's listener-facing HTTP surface:
// public, unauthenticated MP3 streaming plus a couple of ops conveniences.
package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/tmfksoft/goradio/internal/registry"
)

// NewMux builds the listener-facing HTTP handler: /stream/{slug},
// /healthz, and /stations. None of these routes require authentication —
// only the gRPC control plane is JWT-protected.
func NewMux(log *slog.Logger, reg *registry.Registry) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stream/{slug}", streamHandler(log, reg))
	mux.HandleFunc("GET /healthz", healthzHandler)
	mux.HandleFunc("GET /stations", stationsHandler(reg))
	return mux
}

func streamHandler(log *slog.Logger, reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		st, ok := reg.Get(slug)
		if !ok {
			http.NotFound(w, r)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("icy-name", st.Name())
		w.Header().Set("icy-pub", "0")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		_, ch, unsubscribe := st.Broadcaster.Subscribe()
		defer unsubscribe()

		log.Info("listener connected", "slug", slug, "remote_addr", r.RemoteAddr)
		defer log.Info("listener disconnected", "slug", slug, "remote_addr", r.RemoteAddr)

		for {
			select {
			case <-r.Context().Done():
				return
			case chunk, ok := <-ch:
				if !ok {
					// Broadcaster evicted this listener (too slow to keep up).
					return
				}
				if _, err := w.Write(chunk); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
