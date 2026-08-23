package transcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeFFmpeg writes a shell script standing in for ffmpeg at the given
// path, sleeping for sleepFor before exiting 0 -- enough to deterministically
// trigger RunFFmpeg's timeout without needing a real audio file or real
// ffmpeg's own timing. Uses `exec sleep` rather than plain `sleep` so
// `sleep` replaces the shell's own process image instead of running as a
// child of it -- otherwise SIGKILL (from the context timeout) only kills
// the shell wrapper, leaving `sleep` running as an orphan that keeps the
// inherited stdout pipe open, which makes cmd.Wait() (and so the test)
// block for the full sleepFor regardless of the timeout.
func fakeFFmpeg(t *testing.T, sleepFor time.Duration) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ffmpeg.sh")
	script := "#!/bin/sh\nexec sleep " + sleepFor.String() + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

func TestRunFFmpegTimeout(t *testing.T) {
	ffmpegPath := fakeFFmpeg(t, 2*time.Second)
	outPath := filepath.Join(t.TempDir(), "out.mp3")

	err := RunFFmpeg(context.Background(), ffmpegPath, "input.mp3", outPath, EncodeParams{BitrateKbps: 128, SampleRate: 44100, Channels: 2}, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected RunFFmpeg to time out, got nil error")
	}
	if !errors.Is(err, ErrTranscodeTimeout) {
		t.Fatalf("expected errors.Is(err, ErrTranscodeTimeout), got: %v", err)
	}
}

func TestRunFFmpegNonTimeoutFailureIsNotErrTranscodeTimeout(t *testing.T) {
	// A fake ffmpeg that exits immediately with a non-zero status -- a
	// real failure, not a timeout -- must not be misreported as one.
	path := filepath.Join(t.TempDir(), "fake-ffmpeg.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'boom' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	outPath := filepath.Join(t.TempDir(), "out.mp3")

	err := RunFFmpeg(context.Background(), path, "input.mp3", outPath, EncodeParams{BitrateKbps: 128, SampleRate: 44100, Channels: 2}, 5*time.Second)
	if err == nil {
		t.Fatal("expected RunFFmpeg to fail, got nil error")
	}
	if errors.Is(err, ErrTranscodeTimeout) {
		t.Fatalf("a non-timeout failure was misreported as ErrTranscodeTimeout: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the tail of ffmpeg's output in the error, got: %v", err)
	}
}

func TestTail(t *testing.T) {
	if got := tail([]byte("short"), 100); got != "short" {
		t.Errorf("tail() of short input = %q, want unchanged", got)
	}

	long := strings.Repeat("a", 50) + "IMPORTANT_END"
	got := tail([]byte(long), 20)
	if !strings.HasSuffix(got, "IMPORTANT_END") {
		t.Errorf("tail() dropped the end of the output: %q", got)
	}
	if strings.Contains(got, strings.Repeat("a", 50)) {
		t.Errorf("tail() kept the start instead of the end: %q", got)
	}
}
