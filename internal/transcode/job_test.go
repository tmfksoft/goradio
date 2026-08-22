package transcode

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
	"github.com/tmfksoft/goradio/internal/audiosource"
	"github.com/tmfksoft/goradio/internal/playback"
)

// A burst of QueueTrack calls for the same huge single-file station (all
// eventually deduped by Cache's singleflight) previously stalled the
// entire pool: Prefetch just did a blocking p.jobs <- item, so once the
// buffer filled, every subsequent QueueTrack's gRPC handler hung
// indefinitely instead of returning -- which is what actually cascaded
// into unrelated stations' calls timing out too, not the extra ffmpeg
// work itself (singleflight already collapses that per source file).
func TestPrefetchNeverBlocksWhenFull(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := NewPool(log, nil, audiosource.Config{}, 0) // workerCount 0: nothing ever drains the buffer

	for i := 0; i < cap(pool.jobs); i++ {
		item := playback.NewQueuedItem(fmt.Sprintf("fill-%d", i), &audioserverv1.TrackSource{}, 0, 0)
		pool.Prefetch(item)
	}

	overflow := playback.NewQueuedItem("overflow", &audioserverv1.TrackSource{}, 0, 0)
	done := make(chan struct{})
	go func() {
		pool.Prefetch(overflow)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Prefetch blocked instead of failing fast once the job buffer was full")
	}

	select {
	case <-overflow.Ready():
	default:
		t.Fatal("overflow item was never marked ready/failed")
	}
	if overflow.Err() == nil {
		t.Fatal("expected the overflow item to have failed, got a nil error")
	}
	if overflow.ErrCode() != "PREFETCH_QUEUE_FULL" {
		t.Fatalf("ErrCode() = %q, want PREFETCH_QUEUE_FULL", overflow.ErrCode())
	}
}
