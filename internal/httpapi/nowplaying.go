package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	audioserverv1 "github.com/goradioserver/goradio/gen/go/audioserver/v1"
	"github.com/goradioserver/goradio/internal/auth"
	"github.com/goradioserver/goradio/internal/playback"
	"github.com/goradioserver/goradio/internal/registry"
)

// nowPlayingTrack mirrors QueuedItemStatus for JSON consumers (a web
// embed, a Discord bot, etc.) that don't want to speak gRPC. The
// underlying data is exactly what GetStatus/SubscribeEvents already
// expose over gRPC -- this is just an HTTP-native way to read the same
// thing. QueueID/Location/Mode are only populated for an authenticated
// caller (see nowPlayingHandler) since Location can be a raw filesystem
// path or an upstream URL (which may itself embed a secret, e.g. a query
// string token) -- not something to hand out to anyone who knows a slug.
type nowPlayingTrack struct {
	QueueID         string `json:"queue_id,omitempty"`
	Location        string `json:"location,omitempty"`
	Title           string `json:"title,omitempty"`
	Artist          string `json:"artist,omitempty"`
	CoverArtUrl     string `json:"cover_art_url,omitempty"`
	Mode            string `json:"mode,omitempty"`
	DurationSeconds int64  `json:"duration_seconds"` // 0 = unknown/indefinite (live relay)
}

// nowPlayingHistoryEntry mirrors HistoryEntryStatus. Unlike current_track
// and queue, the whole history array is withheld from the public
// (unauthenticated) response rather than just having queue_id/location/
// mode stripped from it -- a full play-log is more revealing than a
// single current/pending snapshot, so it gets the same bar as those
// per-item fields rather than the lower one current_track/queue use.
type nowPlayingHistoryEntry struct {
	QueueID         string `json:"queue_id,omitempty"`
	Location        string `json:"location,omitempty"`
	Title           string `json:"title,omitempty"`
	Artist          string `json:"artist,omitempty"`
	CoverArtUrl     string `json:"cover_art_url,omitempty"`
	Mode            string `json:"mode,omitempty"`
	DurationSeconds int64  `json:"duration_seconds"`
	Reason          string `json:"reason"`
	EndedAtUnixMs   int64  `json:"ended_at_unix_ms"`
}

type nowPlayingResponse struct {
	Slug                       string                   `json:"slug"`
	Name                       string                   `json:"name"`
	IsSilence                  bool                     `json:"is_silence"`
	IsPaused                   bool                     `json:"is_paused"`
	LogoUrl                    string                   `json:"logo_url,omitempty"`
	Metadata                   map[string]string        `json:"metadata,omitempty"`
	CurrentTrack               *nowPlayingTrack         `json:"current_track,omitempty"`
	CurrentTrackElapsedSeconds int64                    `json:"current_track_elapsed_seconds,omitempty"`
	ListenerCount              int64                    `json:"listener_count"`
	Queue                      []nowPlayingTrack        `json:"queue"`
	History                    []nowPlayingHistoryEntry `json:"history,omitempty"`
}

// nowPlayingHandler implements GET /stations/{slug}/now-playing: a JSON
// snapshot of what GetStatus already returns over gRPC, for consumers
// where standing up a gRPC(-web) client isn't worth it -- pairing a plain
// HTML/JS radio player with a progress bar, a Discord bot, etc.
//
// Public by default, like /stream/{slug} and /stations -- title, artist,
// cover art, duration, elapsed time, pause state, logo, metadata, and
// listener count carry no more information than you could already get by
// listening to the (already public, unauthenticated) stream itself, or
// are just descriptive station-level info the operator set to be shown.
// Present a valid bearer token (any token authorized for this slug,
// including a read-only one -- this never mutates anything) to
// additionally get:
//   - queue_id, the raw location, and mode on current_track/queue items,
//     which can reveal filesystem layout or upstream URLs and so aren't
//     handed out for free.
//   - history at all -- a full recently-played log is more revealing
//     than the current/pending snapshot, so it's withheld entirely from
//     the public response rather than just having those same fields
//     stripped from it.
//
// An Authorization header that's present but invalid is rejected outright
// rather than silently downgraded to the public view, so a typo'd token
// fails loudly.
func nowPlayingHandler(reg *registry.Registry, jwtSecret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		st, ok := reg.Get(slug)
		if !ok {
			http.NotFound(w, r)
			return
		}

		claims, present, err := tryAuthenticateBearer(r, jwtSecret)
		if present && err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		detailed := false
		if present {
			if !claims.HasSlug(slug) {
				http.Error(w, "token not authorized for this station", http.StatusForbidden)
				return
			}
			detailed = true
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildNowPlaying(st, detailed))
	}
}

func buildNowPlaying(st *playback.Station, detailed bool) nowPlayingResponse {
	resp := nowPlayingResponse{
		Slug:          st.Slug,
		Name:          st.Name(),
		IsSilence:     st.IsSilence(),
		IsPaused:      st.IsPaused(),
		LogoUrl:       st.LogoURL(),
		Metadata:      st.Metadata(),
		ListenerCount: int64(st.Broadcaster.ListenerCount()),
		Queue:         []nowPlayingTrack{},
	}

	if cur := st.Current(); cur != nil {
		t := nowPlayingTrackOf(cur.ID, cur.Source, cur.Mode.String(), cur.DurationSeconds(), detailed)
		resp.CurrentTrack = &t
		resp.CurrentTrackElapsedSeconds = st.CurrentElapsedSeconds()
	}

	for _, item := range st.Queue.Snapshot() {
		resp.Queue = append(resp.Queue, nowPlayingTrackOf(item.ID, item.Source, item.Mode.String(), item.DurationSeconds(), detailed))
	}

	if detailed {
		for _, h := range st.History() {
			resp.History = append(resp.History, nowPlayingHistoryEntry{
				QueueID:         h.Item.ID,
				Location:        h.Item.Source.GetLocation(),
				Title:           h.Item.Source.GetDisplayTitle(),
				Artist:          h.Item.Source.GetDisplayArtist(),
				CoverArtUrl:     h.Item.Source.GetCoverArtUrl(),
				Mode:            h.Item.Mode.String(),
				DurationSeconds: h.Item.DurationSeconds(),
				Reason:          h.Reason,
				EndedAtUnixMs:   h.EndedAt.UnixMilli(),
			})
		}
	}

	return resp
}

func nowPlayingTrackOf(queueID string, source *audioserverv1.TrackSource, mode string, durationSeconds int64, detailed bool) nowPlayingTrack {
	t := nowPlayingTrack{
		Title:           source.GetDisplayTitle(),
		Artist:          source.GetDisplayArtist(),
		CoverArtUrl:     source.GetCoverArtUrl(),
		DurationSeconds: durationSeconds,
	}
	if detailed {
		t.QueueID = queueID
		t.Location = source.GetLocation()
		t.Mode = mode
	}
	return t
}

// tryAuthenticateBearer verifies an optional bearer token: present=false
// means no Authorization header was sent at all (fine -- the public
// view applies); present=true with a non-nil err means one was sent but
// is invalid, which callers should reject outright.
func tryAuthenticateBearer(r *http.Request, secret []byte) (claims *auth.Claims, present bool, err error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, false, nil
	}

	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return nil, true, fmt.Errorf("malformed authorization header (expected %q)", prefix+"<token>")
	}
	token := strings.TrimPrefix(h, prefix)
	if token == "" {
		return nil, true, fmt.Errorf("missing bearer token")
	}

	c, verr := auth.Verify(secret, token)
	if verr != nil {
		return nil, true, fmt.Errorf("invalid token: %w", verr)
	}
	return c, true, nil
}
