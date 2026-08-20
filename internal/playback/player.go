package playback

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"
)

// PlayerConfig configures a station's player loop.
type PlayerConfig struct {
	// BitrateKbps determines the byte-rate used to pace playback in real
	// time (bytes_per_second = BitrateKbps*1000/8). Must match the fixed
	// CBR MP3 format every clip is transcoded to.
	BitrateKbps int
	// SilencePath is the pre-generated looping silence clip played
	// whenever the queue is empty.
	SilencePath string
}

const streamChunkDuration = 200 * time.Millisecond

// Run is the station's player loop: pull the next ready queue item, stream
// it to the Broadcaster in real-time-paced chunks, repeat; fall back to
// looping silence when the queue is empty. It blocks until ctx is
// cancelled, so callers run it in its own goroutine (one per station).
func (s *Station) Run(ctx context.Context, log *slog.Logger, cfg PlayerConfig) {
	bytesPerSecond := cfg.BitrateKbps * 1000 / 8
	wasSilent := false

	for ctx.Err() == nil {
		item, ok := s.Queue.PopFront()
		if !ok {
			if !wasSilent {
				s.PublishSilenceStarted()
				wasSilent = true
			}
			// Loop the silence clip, re-checking the queue every chunk so a
			// newly queued track starts within one chunk's latency rather
			// than waiting for the whole silence clip to finish.
			s.streamFile(ctx, log, cfg.SilencePath, bytesPerSecond, true, func() bool {
				return s.Queue.Len() > 0
			})
			continue
		}

		select {
		case <-item.Ready():
		case <-ctx.Done():
			return
		}

		if item.Err() != nil {
			log.Warn("skipping queue item that failed to prefetch", "slug", s.Slug, "queue_id", item.ID, "error", item.Err())
			s.PublishError(item.Err().Error(), "TRANSCODE_FAILED")
			continue
		}

		if wasSilent {
			s.PublishSilenceEnded()
			wasSilent = false
		}

		s.SetCurrent(item)
		s.PublishTrackStarted(item)

		reason := s.streamFile(ctx, log, item.LocalPath(), bytesPerSecond, false, nil)

		s.SetCurrent(nil)
		s.PublishTrackEnded(item.ID, reason)
	}
}

// streamFile streams path's bytes to the Broadcaster at bytesPerSecond,
// pacing writes against a monotonic clock (drift-corrected: each chunk's
// sleep target is computed from total bytes sent so far, not accumulated
// per-chunk sleeps) so a full file isn't dumped to listeners instantly.
//
// If loop is true, playback restarts from the beginning on EOF instead of
// returning, until interrupted — used for the silence clip. checkStop, if
// non-nil, is polled once per chunk to allow early exit (used to break out
// of the silence loop as soon as something is queued).
//
// Returns "completed" or "interrupted".
func (s *Station) streamFile(ctx context.Context, log *slog.Logger, path string, bytesPerSecond int, loop bool, checkStop func() bool) string {
	streamCtx, cancel := context.WithCancel(ctx)
	s.setStreamCancel(cancel)
	defer func() {
		s.setStreamCancel(nil)
		cancel()
	}()

	f, err := os.Open(path)
	if err != nil {
		log.Warn("failed to open clip for streaming", "slug", s.Slug, "path", path, "error", err)
		return "interrupted"
	}
	defer f.Close()

	chunkSize := int(float64(bytesPerSecond) * streamChunkDuration.Seconds())
	if chunkSize < 1 {
		chunkSize = 4096
	}
	buf := make([]byte, chunkSize)

	start := time.Now()
	var bytesSent int64

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.Broadcaster.Write(chunk)
			bytesSent += int64(n)

			targetElapsed := time.Duration(float64(bytesSent) / float64(bytesPerSecond) * float64(time.Second))
			if wait := targetElapsed - time.Since(start); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-timer.C:
				case <-streamCtx.Done():
					timer.Stop()
					return "interrupted"
				}
			}
		}

		select {
		case <-streamCtx.Done():
			return "interrupted"
		default:
		}
		if checkStop != nil && checkStop() {
			return "interrupted"
		}

		if readErr == io.EOF {
			if !loop {
				return "completed"
			}
			if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
				log.Warn("failed to loop clip", "slug", s.Slug, "path", path, "error", seekErr)
				return "completed"
			}
			start = time.Now()
			bytesSent = 0
			continue
		}
		if readErr != nil {
			log.Warn("error reading clip", "slug", s.Slug, "path", path, "error", readErr)
			return "interrupted"
		}
	}
}
