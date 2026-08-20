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
	log    *slog.Logger
	cache  *Cache
	srcCfg audiosource.Config
	jobs   chan *playback.QueuedItem
}

// NewPool starts workerCount background workers.
func NewPool(log *slog.Logger, cache *Cache, srcCfg audiosource.Config, workerCount int) *Pool {
	p := &Pool{
		log:    log,
		cache:  cache,
		srcCfg: srcCfg,
		jobs:   make(chan *playback.QueuedItem, 64),
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

	resolved, err := audiosource.Resolve(ctx, p.srcCfg, item.Source)
	if err != nil {
		p.log.Warn("failed to resolve track source", "error", err, "location", item.Source.GetLocation())
		item.MarkReady("", err)
		return
	}
	if resolved.Downloaded {
		defer os.Remove(resolved.Path)
	}

	path, err := p.cache.GetOrTranscode(ctx, resolved.Path, resolved.CacheKey)
	if err != nil {
		p.log.Warn("failed to transcode track", "error", err, "location", item.Source.GetLocation())
		item.MarkReady("", err)
		return
	}

	item.MarkReady(path, nil)
}

// Prefetch enqueues item for background resolve+transcode. Dispatch blocks
// the caller only if the job buffer (64 deep) is full, which bounds how
// many concurrent ffmpeg processes a burst of QueueTrack calls can spawn.
func (p *Pool) Prefetch(item *playback.QueuedItem) {
	p.jobs <- item
}
