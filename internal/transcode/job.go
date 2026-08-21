package transcode

import (
	"context"
	"log/slog"
	"os"

	"github.com/tmfksoft/goradio/internal/audiosource"
	"github.com/tmfksoft/goradio/internal/playback"
)

// Pool is a bounded worker pool that resolves and transcodes queued items
// in the background, starting the moment they're queued (not when they're
// about to play) so downloads/transcodes are ready well ahead of playback.
// It implements grpcapi.Prefetcher.
type Pool struct {
	log      *slog.Logger
	cache    *Cache
	resolver *audiosource.Resolver
	jobs     chan *playback.QueuedItem
}

// NewPool starts workerCount background workers.
func NewPool(log *slog.Logger, cache *Cache, srcCfg audiosource.Config, workerCount int) *Pool {
	p := &Pool{
		log:      log,
		cache:    cache,
		resolver: audiosource.NewResolver(srcCfg),
		jobs:     make(chan *playback.QueuedItem, 64),
	}
	for i := 0; i < workerCount; i++ {
		go p.worker()
	}
	return p
}

func (p *Pool) worker() {
	for item := range p.jobs {
		p.process(item)
	}
}

func (p *Pool) process(item *playback.QueuedItem) {
	ctx := context.Background()

	resolved, err := p.resolver.Resolve(ctx, item.Source)
	if err != nil {
		p.log.Warn("failed to resolve track source", "error", err, "location", item.Source.GetLocation())
		item.MarkReady("", 0, err)
		return
	}

	if resolved.IsLive {
		p.log.Info("track source auto-detected as a live stream", "location", item.Source.GetLocation())
		item.MarkLive(resolved.LiveURL)
		return
	}

	if resolved.Downloaded {
		defer os.Remove(resolved.Path)
	}

	path, err := p.cache.GetOrTranscode(ctx, resolved.Path, resolved.CacheKey)
	if err != nil {
		p.log.Warn("failed to transcode track", "error", err, "location", item.Source.GetLocation())
		item.MarkReady("", 0, err)
		return
	}

	item.MarkReady(path, p.durationSeconds(path), nil)
}

// durationSeconds computes a cached file's duration from its size, since
// every cached file is a fixed CBR bitrate -- no need to probe the file
// itself (e.g. via ffprobe). Returns 0 (unknown) if the file can't be
// stat'd or the configured bitrate is 0.
func (p *Pool) durationSeconds(path string) int64 {
	bytesPerSecond := p.cache.Params().BitrateKbps * 1000 / 8
	if bytesPerSecond <= 0 {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size() / int64(bytesPerSecond)
}

// Prefetch enqueues item for background resolve+transcode. Dispatch blocks
// the caller only if the job buffer (64 deep) is full, which bounds how
// many concurrent ffmpeg processes a burst of QueueTrack calls can spawn.
func (p *Pool) Prefetch(item *playback.QueuedItem) {
	p.jobs <- item
}
