// Package silence generates and caches a looping silent MP3 clip, played
// by a station's player loop whenever its queue is empty.
package silence

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tmfksoft/goradio/internal/transcode"
)

// EnsureClip generates (if not already cached) a silent MP3 matching
// params, `duration` long, meant to be looped by the player. Silence is not
// per-station-distinct, so one clip is cached and reused across every
// station and across process restarts (keyed by encode params, so a config
// change regenerates it).
func EnsureClip(ctx context.Context, ffmpegPath, cacheDir string, params transcode.EncodeParams, duration time.Duration, timeout time.Duration) (string, error) {
	path := filepath.Join(cacheDir, fmt.Sprintf(
		"silence-%dkbps-%dhz-%dch-%ds.mp3",
		params.BitrateKbps, params.SampleRate, params.Channels, int(duration.Seconds()),
	))

	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tmpPath := path + ".tmp"
	args := []string{
		"-y",
		"-f", "lavfi",
		"-i", fmt.Sprintf("anullsrc=r=%d:cl=%s", params.SampleRate, channelLayout(params.Channels)),
		"-t", fmt.Sprintf("%d", int(duration.Seconds())),
		"-b:a", fmt.Sprintf("%dk", params.BitrateKbps),
		"-f", "mp3",
		tmpPath,
	}

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("generate silence clip: %w: %s", err, string(output))
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("finalize silence clip: %w", err)
	}

	return path, nil
}

func channelLayout(channels int) string {
	if channels == 1 {
		return "mono"
	}
	return "stereo"
}
