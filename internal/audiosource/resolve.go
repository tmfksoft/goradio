// Package audiosource resolves a QueueTrack's TrackSource into a local
// filesystem path ready to hand to the transcoder, defending against path
// traversal for local files.
package audiosource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
	"github.com/tmfksoft/goradio/internal/fetch"
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

// Resolved is a source ready to hand to the transcoder.
type Resolved struct {
	// Path to the actual bytes to transcode.
	Path string
	// CacheKey stably identifies this source's content for the transcode
	// cache; the cache is responsible for hashing it into a filename.
	CacheKey string
	// Downloaded is true if Path is a temp file the caller must remove
	// once done with it (false for local files, which are never deleted).
	Downloaded bool
}

// Resolve resolves src into a Resolved, downloading it first if it's an
// HTTP(S) URL.
func Resolve(ctx context.Context, cfg Config, src *audioserverv1.TrackSource) (Resolved, error) {
	switch src.GetType() {
	case audioserverv1.TrackSourceType_TRACK_SOURCE_TYPE_LOCAL_FILE:
		return resolveLocal(cfg, src.GetLocation())
	case audioserverv1.TrackSourceType_TRACK_SOURCE_TYPE_HTTP_URL:
		return resolveHTTP(ctx, cfg, src.GetLocation())
	default:
		return Resolved{}, fmt.Errorf("track source has unspecified type")
	}
}

func resolveLocal(cfg Config, location string) (Resolved, error) {
	if location == "" {
		return Resolved{}, fmt.Errorf("local file location is empty")
	}

	root, err := filepath.Abs(cfg.AudioRoot)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve audio root: %w", err)
	}

	cleaned := filepath.Clean(location)
	full, err := filepath.Abs(filepath.Join(root, cleaned))
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve path: %w", err)
	}

	// Path traversal defense: the resolved path must stay inside root.
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Resolved{}, fmt.Errorf("local file %q escapes the audio root", location)
	}

	info, err := os.Stat(full)
	if err != nil {
		return Resolved{}, fmt.Errorf("stat %q: %w", location, err)
	}
	if info.IsDir() {
		return Resolved{}, fmt.Errorf("local file %q is a directory", location)
	}

	key := fmt.Sprintf("local:%s:%d:%d", cleaned, info.ModTime().UnixNano(), info.Size())
	return Resolved{Path: full, CacheKey: key}, nil
}

func resolveHTTP(ctx context.Context, cfg Config, rawURL string) (Resolved, error) {
	if rawURL == "" {
		return Resolved{}, fmt.Errorf("http url location is empty")
	}

	path, err := fetch.Download(ctx, rawURL, cfg.MaxDownloadBytes, cfg.DownloadDir)
	if err != nil {
		return Resolved{}, err
	}

	key := fmt.Sprintf("url:%s", rawURL)
	return Resolved{Path: path, CacheKey: key, Downloaded: true}, nil
}
