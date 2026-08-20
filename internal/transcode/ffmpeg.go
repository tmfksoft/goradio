// Package transcode shells out to ffmpeg to normalize every audio source
// (local files and downloaded URLs) into one fixed MP3 format, caching
// results on disk so repeated plays don't re-encode. This uniform format is
// what makes hard-cut playback (byte concatenation, no gap/crossfade) safe.
package transcode

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// EncodeParams is the single fixed target format every source is
// transcoded to.
type EncodeParams struct {
	BitrateKbps int
	SampleRate  int
	Channels    int
}

// RunFFmpeg transcodes inputPath into outputPath as CBR MP3 per params,
// stripping any video/album-art stream. It is cancelled after timeout.
func RunFFmpeg(ctx context.Context, ffmpegPath, inputPath, outputPath string, p EncodeParams, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
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

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg transcode failed: %w: %s", err, truncate(output, 2000))
	}
	return nil
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "...(truncated)"
	}
	return s
}
