package luastation

import (
	"context"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	audioserverv1 "github.com/goradioserver/goradio/gen/go/audioserver/v1"
)

// StartControlAPI starts the station's own optional local control HTTP API
// if enabled in its config. This phase ships only a placeholder GET
// /status route, gated by the configured API key — real control endpoints
// (e.g. external "queue this now" triggers) arrive with the future
// queue-API redesign.
func (e *Engine) StartControlAPI() {
	if !e.cfg.API.Enabled {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", e.controlStatusHandler)

	server := &http.Server{
		Addr:    e.cfg.API.BindHost,
		Handler: requireAPIKey(e.cfg.API.APIKey, mux),
	}

	go func() {
		e.log.Info("control api listening", "addr", e.cfg.API.BindHost)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			e.log.Error("control api server error", "error", err)
		}
	}()
}

func requireAPIKey(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Api-Key")
		if got == "" {
			got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if apiKey == "" || got != apiKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (e *Engine) controlStatusHandler(w http.ResponseWriter, r *http.Request) {
	slug := e.getRegisteredSlug()
	if slug == "" {
		http.Error(w, "station not registered yet", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp, err := e.client.GetStatus(ctx, &audioserverv1.GetStatusRequest{Slug: slug})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	data, err := protojson.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
