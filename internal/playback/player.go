package playback

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// PlayerConfig configures a station's player loop.
type PlayerConfig struct {
	// BitrateKbps determines the byte-rate used to pace playback in real
	// time (bytes_per_second = BitrateKbps*1000/8). Must match the fixed
	// CBR MP3 format every clip is transcoded to.
	BitrateKbps int
	// SampleRate and Channels, together with BitrateKbps, are the fixed
	// target format live relays are re-encoded to, matching every other
	// clip's transcode params (internal/transcode.EncodeParams).
	SampleRate int
	Channels   int
	// FfmpegPath is used to spawn the continuous re-encode process for
	// live relays (see streamLiveRelay). Cached/one-shot transcoding uses
	// internal/transcode instead; this is only for live sources.
	FfmpegPath string
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
		s.QueueChanged()

		select {
		case <-item.Ready():
			// Already ready (the common case) — proceed immediately.
		default:
			// Not ready yet (still prefetching, or e.g. a live relay still
			// starting up) — stream silence rather than dead air while we
			// wait, re-checking readiness every chunk so we pick it up with
			// one chunk's latency rather than only once the whole silence
			// clip loops around.
			if !wasSilent {
				s.PublishSilenceStarted()
				wasSilent = true
			}
			s.streamFile(ctx, log, cfg.SilencePath, bytesPerSecond, true, func() bool {
				select {
				case <-item.Ready():
					return true
				default:
					return false
				}
			})
			if ctx.Err() != nil {
				return
			}
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

		var reason string
		if item.IsLive() {
			// A queued live stream (auto-detected — see audiosource.Resolve)
			// has no natural end: it "sticks" as the current track until
			// skipped/interrupted, or the upstream connection itself ends.
			reason = s.streamLiveRelay(ctx, log, item.LiveURL(), cfg)
		} else {
			reason = s.streamFile(ctx, log, item.LocalPath(), bytesPerSecond, false, nil)
		}

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

// liveRelayChunkBytes is the read buffer size for a live relay's ffmpeg
// stdout pipe. Unlike streamFile, chunks are forwarded to the Broadcaster
// as soon as they're read rather than paced against a clock: the upstream
// live source's own network delivery rate already paces the relay — an
// internet radio stream doesn't arrive faster than its broadcast bitrate
// — so no artificial pacing is needed on top of that.
const liveRelayChunkBytes = 32 * 1024

// streamLiveRelay continuously relays sourceURL through a long-running
// ffmpeg process, re-encoded to the station's fixed target format (so it
// splices safely with every other, transcoded clip), into the
// Broadcaster. It has no natural end: it keeps relaying until Interrupt
// cancels the stream (which kills the ffmpeg process via streamCtx) or
// the upstream connection itself drops/errors.
//
// Returns "completed" (the upstream ended or errored on its own) or
// "interrupted" (Station.Interrupt was called, e.g. via Skip or a new
// PLAY_NOW_INTERRUPT item).
func (s *Station) streamLiveRelay(ctx context.Context, log *slog.Logger, sourceURL string, cfg PlayerConfig) string {
	streamCtx, cancel := context.WithCancel(ctx)
	s.setStreamCancel(cancel)
	defer func() {
		s.setStreamCancel(nil)
		cancel()
	}()

	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-i", sourceURL,
		"-vn",
		"-ar", fmt.Sprintf("%d", cfg.SampleRate),
		"-ac", fmt.Sprintf("%d", cfg.Channels),
		"-b:a", fmt.Sprintf("%dk", cfg.BitrateKbps),
		"-f", "mp3",
		"pipe:1",
	}

	cmd := exec.CommandContext(streamCtx, cfg.FfmpegPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Warn("failed to create ffmpeg stdout pipe for live relay", "slug", s.Slug, "url", sourceURL, "error", err)
		return "interrupted"
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		log.Warn("failed to start ffmpeg for live relay", "slug", s.Slug, "url", sourceURL, "error", err)
		return "interrupted"
	}

	log.Info("live relay started", "slug", s.Slug, "url", sourceURL)

	buf := make([]byte, liveRelayChunkBytes)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.Broadcaster.Write(chunk)
		}
		if readErr != nil {
			break
		}
	}

	waitErr := cmd.Wait()
	log.Info("live relay ended", "slug", s.Slug, "url", sourceURL, "interrupted", streamCtx.Err() != nil)

	if streamCtx.Err() != nil {
		return "interrupted"
	}
	if waitErr != nil {
		log.Warn("live relay ffmpeg exited with an error", "slug", s.Slug, "url", sourceURL, "error", waitErr, "stderr", stderr.String())
	}
	return "completed"
}

// limitedBuffer is a bytes.Buffer-like io.Writer capped at a fixed size,
// so capturing a long-running ffmpeg process's stderr (which, even at
// -loglevel warning, could in principle emit a lot over a long relay
// session) can't grow unbounded.
type limitedBuffer struct {
	buf   []byte
	limit int
}

const limitedBufferMaxBytes = 4096

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit == 0 {
		b.limit = limitedBufferMaxBytes
	}
	remaining := b.limit - len(b.buf)
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		b.buf = append(b.buf, p[:remaining]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return string(b.buf) }
