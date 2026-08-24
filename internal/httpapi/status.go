package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/goradioserver/goradio/internal/registry"
)

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

type stationSummary struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	ListenerCount int    `json:"listener_count"`
}

func stationsHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list := reg.List()
		out := make([]stationSummary, 0, len(list))
		for _, st := range list {
			out = append(out, stationSummary{
				Slug:          st.Slug,
				Name:          st.Name(),
				ListenerCount: st.Broadcaster.ListenerCount(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
