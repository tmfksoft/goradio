// Package audiosource resolves a QueueTrack's TrackSource into either a
// local filesystem path ready to hand to the transcoder, or a live stream
// URL — auto-detected from the HTTP response, defending against path
// traversal for local files.
package audiosource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	audioserverv1 "github.com/goradioserver/goradio/gen/go/audioserver/v1"
	"github.com/goradioserver/goradio/internal/fetch"
)

// Config configures how sources are resolved.
type Config struct {
	AudioRoot        string
	MaxDownloadBytes int64
	// DownloadDir is where HTTP(S) sources are downloaded before
	// transcoding, typically the transcode cache's tmp subdirectory so the
	// eventual rename into the cache is a same-filesystem move.
	DownloadDir string
}

// Resolved is a source ready to hand to the transcoder, or a live stream
// ready to hand to the player's live-relay path.
type Resolved struct {
	// IsLive is true if this HTTP(S) source was auto-detected as a
	// continuous live stream (e.g. an Icecast/Shoutcast mountpoint) rather
	// than a finite downloadable file — see fetch.IsLiveStream. When true,
	// only LiveURL is set; the source bypasses the transcode cache
	// entirely and is instead relayed continuously (internal/playback's
	// live-relay player path).
	IsLive  bool
	LiveURL string

	// Path to the actual bytes to transcode. Unset when IsLive.
	Path string
	// CacheKey stably identifies this source's content for the transcode
	// cache; the cache is responsible for hashing it into a filename.
	// Unset when IsLive.
	CacheKey string
	// Downloaded is true if Path is a temp file the caller must remove
	// once done with it (false for local files, which are never deleted).
	Downloaded bool
}

// Resolver resolves TrackSources, remembering (for the life of the
// process) which HTTP(S) URLs were classified as live streams so repeat
// QueueTrack calls for the same URL skip re-classifying it.
type Resolver struct {
	cfg Config

	liveMu    sync.RWMutex
	liveCache map[string]bool // url -> isLive
}

// NewResolver constructs a Resolver.
func NewResolver(cfg Config) *Resolver {
	return &Resolver{cfg: cfg, liveCache: make(map[string]bool)}
}

// Resolve resolves src, downloading and classifying it first if it's an
// HTTP(S) URL not already known to be live.
func (r *Resolver) Resolve(ctx context.Context, src *audioserverv1.TrackSource) (Resolved, error) {
	switch src.GetType() {
	case audioserverv1.TrackSourceType_TRACK_SOURCE_TYPE_LOCAL_FILE:
		return resolveLocal(r.cfg, src.GetLocation())
	case audioserverv1.TrackSourceType_TRACK_SOURCE_TYPE_HTTP_URL:
		return r.resolveHTTP(ctx, src.GetLocation())
	default:
		return Resolved{}, fmt.Errorf("track source has unspecified type")
	}
}

// SafeRelPath cleans location, resolves it against root, and rejects
// anything that escapes root (whether via "../" segments or an absolute
// path, both of which filepath.Join+Clean would otherwise happily fold
// into a path still confined to root, but which filepath.Rel then flags
// via the leading ".." check below). Returns the "/"-separated path
// relative to root -- the same form TrackSource.location and
// DirectoryEntry.path use -- and the absolute filesystem path.
//
// Shared by resolveLocal (which stats the result and requires a file) and
// grpcapi's QueueTrack/ListDirectory handlers (which need the identical
// resolution to run synchronously, before prefetch, so a directory-scope
// authorization check can never disagree with what the file access itself
// will later resolve to).
func SafeRelPath(root, location string) (relPath, absPath string, err error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve audio root: %w", err)
	}

	cleaned := filepath.Clean(location)
	full, err := filepath.Abs(filepath.Join(rootAbs, cleaned))
	if err != nil {
		return "", "", fmt.Errorf("resolve path: %w", err)
	}

	rel, err := filepath.Rel(rootAbs, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q escapes the audio root", location)
	}
	if rel == "." {
		rel = ""
	}

	return filepath.ToSlash(rel), full, nil
}

func resolveLocal(cfg Config, location string) (Resolved, error) {
	if location == "" {
		return Resolved{}, fmt.Errorf("local file location is empty")
	}

	_, full, err := SafeRelPath(cfg.AudioRoot, location)
	if err != nil {
		return Resolved{}, err
	}

	info, err := os.Stat(full)
	if err != nil {
		return Resolved{}, fmt.Errorf("stat %q: %w", location, err)
	}
	if info.IsDir() {
		return Resolved{}, fmt.Errorf("local file %q is a directory", location)
	}

	key := fmt.Sprintf("local:%s:%d:%d", filepath.Clean(location), info.ModTime().UnixNano(), info.Size())
	return Resolved{Path: full, CacheKey: key}, nil
}

func (r *Resolver) resolveHTTP(ctx context.Context, rawURL string) (Resolved, error) {
	if rawURL == "" {
		return Resolved{}, fmt.Errorf("http url location is empty")
	}

	if isLive, known := r.getLiveCache(rawURL); known {
		if isLive {
			return Resolved{IsLive: true, LiveURL: rawURL}, nil
		}
		// Known finite: fall through to a normal download below (no point
		// caching "not live" beyond avoiding a second classification --
		// the transcode cache already dedupes repeat downloads by URL).
	}

	resp, err := fetch.Open(ctx, rawURL)
	if err != nil {
		return Resolved{}, err
	}
	defer resp.Body.Close()

	isLive := fetch.IsLiveStream(resp)
	r.setLiveCache(rawURL, isLive)

	if isLive {
		return Resolved{IsLive: true, LiveURL: rawURL}, nil
	}

	path, err := fetch.SaveToFile(resp, r.cfg.MaxDownloadBytes, r.cfg.DownloadDir)
	if err != nil {
		return Resolved{}, err
	}

	key := fmt.Sprintf("url:%s", rawURL)
	return Resolved{Path: path, CacheKey: key, Downloaded: true}, nil
}

func (r *Resolver) getLiveCache(url string) (isLive bool, known bool) {
	r.liveMu.RLock()
	defer r.liveMu.RUnlock()
	isLive, known = r.liveCache[url]
	return isLive, known
}

func (r *Resolver) setLiveCache(url string, isLive bool) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	r.liveCache[url] = isLive
}
