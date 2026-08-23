// Package transcode shells out to ffmpeg to normalize every audio source
// (local files and downloaded URLs) into one fixed MP3 format, caching
// results on disk so repeated plays don't re-encode. This uniform format is
// what makes hard-cut playback (byte concatenation, no gap/crossfade) safe.
package transcode

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// EncodeParams is the single fixed target format every source is
// transcoded to.
type EncodeParams struct {
	BitrateKbps int
	SampleRate  int
	Channels    int
}

// ErrTranscodeTimeout wraps RunFFmpeg's error when ffmpeg didn't finish
// within timeout, rather than failing on its own -- callers (job.go) use
// errors.Is against this to report a distinct "TRANSCODE_TIMEOUT" error
// code instead of the generic "TRANSCODE_FAILED", since a timeout usually
// means the input is longer than timeout accounts for (or the server is
// under heavy load) rather than anything wrong with the file itself.
var ErrTranscodeTimeout = errors.New("ffmpeg transcode timed out")

// RunFFmpeg transcodes inputPath into outputPath as CBR MP3 per params,
// stripping any video/album-art stream. It is cancelled after timeout.
func RunFFmpeg(ctx context.Context, ffmpegPath, inputPath, outputPath string, p EncodeParams, timeout time.Duration) error {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"-y",
		"-i", inputPath,
		"-vn",
		"-ar", fmt.Sprintf("%d", p.SampleRate),
		"-ac", fmt.Sprintf("%d", p.Channels),
		"-b:a", fmt.Sprintf("%dk", p.BitrateKbps),
		"-f", "mp3",
		outputPath,
	}

	cmd := exec.CommandContext(runCtx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// exec.CommandContext kills the process when runCtx is done, which
		// surfaces here as a generic "signal: killed" -- indistinguishable
		// from any other ffmpeg failure unless we check *why* runCtx ended.
		// DeadlineExceeded means our own timeout elapsed specifically (as
		// opposed to the caller's ctx being cancelled for some other
		// reason), which is worth reporting distinctly: it usually means
		// the input is longer than `timeout` accounts for, not that
		// anything is actually wrong with it.
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%w after %s (input may be longer than the configured transcode timeout, or the server is under heavy load): %s",
				ErrTranscodeTimeout, timeout, tail(output, 2000))
		}
		return fmt.Errorf("ffmpeg transcode failed: %w: %s", err, tail(output, 2000))
	}
	return nil
}

// tail returns output's last n characters (ffmpeg's actual error, if any,
// is at the end -- right before it exits -- not the start, which is
// always the same version/build-config banner regardless of what went
// wrong).
func tail(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return "...(truncated)" + s[len(s)-n:]
	}
	return s
}
