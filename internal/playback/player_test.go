package playback

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeTestClip(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func newTestItem(t *testing.T, localPath string, durationSeconds int64) *QueuedItem {
	t.Helper()
	item := NewQueuedItem("q1", &audioserverv1.TrackSource{}, audioserverv1.QueueMode_QUEUE_MODE_APPEND, audioserverv1.Transition_TRANSITION_HARD_CUT)
	item.MarkReady(localPath, durationSeconds, nil)
	return item
}

func TestPlayLocalItemPauseHoldsPosition(t *testing.T) {
	dir := t.TempDir()
	const bytesPerSecond = 2000
	trackPath := writeTestClip(t, dir, "track.mp3", bytesPerSecond*3)
	silencePath := writeTestClip(t, dir, "silence.mp3", bytesPerSecond*10)

	st := NewStation("test", "Test", "")
	item := newTestItem(t, trackPath, 3)
	st.SetCurrent(item)

	cfg := PlayerConfig{SilencePath: silencePath}
	log := discardLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan string, 1)
	go func() { done <- st.playLocalItem(ctx, log, item, cfg, bytesPerSecond) }()

	time.Sleep(300 * time.Millisecond)
	if !st.Pause() {
		t.Fatal("Pause() returned false while a track was playing")
	}
	if !st.IsPaused() {
		t.Fatal("expected IsPaused() true after Pause()")
	}
	posAtPause := st.CurrentElapsedSeconds()

	time.Sleep(300 * time.Millisecond)
	if got := st.CurrentElapsedSeconds(); got != posAtPause {
		t.Fatalf("position advanced while paused: was %d, now %d", posAtPause, got)
	}

	if !st.Resume() {
		t.Fatal("Resume() returned false")
	}

	select {
	case reason := <-done:
		if reason != "completed" {
			t.Fatalf("expected playLocalItem to return \"completed\", got %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("playLocalItem did not finish after Resume")
	}
}

func TestPlayLocalItemSeek(t *testing.T) {
	dir := t.TempDir()
	const bytesPerSecond = 2000
	trackPath := writeTestClip(t, dir, "track.mp3", bytesPerSecond*3)
	silencePath := writeTestClip(t, dir, "silence.mp3", bytesPerSecond*10)

	st := NewStation("test", "Test", "")
	item := newTestItem(t, trackPath, 3)
	st.SetCurrent(item)

	cfg := PlayerConfig{SilencePath: silencePath}
	log := discardLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan string, 1)
	go func() { done <- st.playLocalItem(ctx, log, item, cfg, bytesPerSecond) }()

	time.Sleep(100 * time.Millisecond)
	applied, pos := st.SeekPosition(2)
	if !applied || pos != 2 {
		t.Fatalf("SeekPosition(2) = (%v, %d), want (true, 2)", applied, pos)
	}
	if got := st.CurrentElapsedSeconds(); got != 2 {
		t.Fatalf("CurrentElapsedSeconds() right after seek = %d, want 2", got)
	}

	select {
	case reason := <-done:
		if reason != "completed" {
			t.Fatalf("expected \"completed\", got %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("playLocalItem did not finish after seek")
	}
}

func TestPlayLocalItemSeekClampsToDuration(t *testing.T) {
	dir := t.TempDir()
	const bytesPerSecond = 2000
	trackPath := writeTestClip(t, dir, "track.mp3", bytesPerSecond*3)

	st := NewStation("test", "Test", "")
	item := newTestItem(t, trackPath, 3)
	st.SetCurrent(item)

	if applied, pos := st.SeekPosition(-5); !applied || pos != 0 {
		t.Fatalf("SeekPosition(-5) = (%v, %d), want (true, 0)", applied, pos)
	}
	if applied, pos := st.SeekPosition(999); !applied || pos != 3 {
		t.Fatalf("SeekPosition(999) = (%v, %d), want (true, 3)", applied, pos)
	}
}

// TestPlayLocalItemSkipWhilePaused guards against the specific bug this
// pause/seek design has to avoid: Interrupt (a genuine Skip/SkipTo/
// ClearQueue) arriving while the station is paused must still end the
// track, not get silently absorbed by the pause-hold loop.
func TestPlayLocalItemSkipWhilePaused(t *testing.T) {
	dir := t.TempDir()
	const bytesPerSecond = 2000
	trackPath := writeTestClip(t, dir, "track.mp3", bytesPerSecond*5)
	silencePath := writeTestClip(t, dir, "silence.mp3", bytesPerSecond*10)

	st := NewStation("test", "Test", "")
	item := newTestItem(t, trackPath, 5)
	st.SetCurrent(item)

	cfg := PlayerConfig{SilencePath: silencePath}
	log := discardLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan string, 1)
	go func() { done <- st.playLocalItem(ctx, log, item, cfg, bytesPerSecond) }()

	time.Sleep(100 * time.Millisecond)
	if !st.Pause() {
		t.Fatal("Pause() failed")
	}
	time.Sleep(100 * time.Millisecond)

	st.Interrupt()

	select {
	case reason := <-done:
		if reason != "interrupted" {
			t.Fatalf("expected \"interrupted\", got %q", reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("playLocalItem did not return after Interrupt while paused -- stuck in the pause hold loop")
	}
	if st.IsPaused() {
		t.Fatal("expected IsPaused() false after a genuine Interrupt")
	}
}

func TestPauseResumeNoOpsWhenNotApplicable(t *testing.T) {
	st := NewStation("test", "Test", "")

	if st.Pause() {
		t.Error("Pause() should be false with nothing playing")
	}
	if st.Resume() {
		t.Error("Resume() should be false when not paused")
	}
	if applied, _ := st.SeekPosition(5); applied {
		t.Error("SeekPosition() should be false with nothing playing")
	}
	if applied, _ := st.SeekBy(5); applied {
		t.Error("SeekBy() should be false with nothing playing")
	}

	liveItem := NewQueuedItem("live1", &audioserverv1.TrackSource{}, audioserverv1.QueueMode_QUEUE_MODE_APPEND, audioserverv1.Transition_TRANSITION_HARD_CUT)
	liveItem.MarkLive("http://example.invalid/live")
	st.SetCurrent(liveItem)

	if st.Pause() {
		t.Error("Pause() should be false for a live relay")
	}
	if applied, _ := st.SeekPosition(5); applied {
		t.Error("SeekPosition() should be false for a live relay")
	}
}
