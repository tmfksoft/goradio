package transcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/singleflight"
)

// Cache maps a source's stable cache key to its transcoded MP3 on disk,
// transcoding via ffmpeg on a cache miss. Concurrent requests for the same
// key are deduplicated so identical sources queued back-to-back (idents,
// adverts, reused songs) don't double-encode.
type Cache struct {
	dir        string
	ffmpegPath string
	params     EncodeParams
	timeout    time.Duration
	group      singleflight.Group
}

func NewCache(dir, ffmpegPath string, params EncodeParams, timeout time.Duration) *Cache {
	return &Cache{dir: dir, ffmpegPath: ffmpegPath, params: params, timeout: timeout}
}

// pathFor returns the sharded on-disk path for a cache key: <dir>/<first 2
// hex chars>/<full hex key>.mp3.
func (c *Cache) pathFor(cacheKey string) string {
	sum := sha256.Sum256([]byte(cacheKey))
	hexKey := hex.EncodeToString(sum[:])
	return filepath.Join(c.dir, hexKey[:2], hexKey+".mp3")
}

// GetOrTranscode returns the cached transcoded MP3 for sourcePath/cacheKey,
// transcoding it first if not already cached.
func (c *Cache) GetOrTranscode(ctx context.Context, sourcePath, cacheKey string) (string, error) {
	outPath := c.pathFor(cacheKey)

	if _, err := os.Stat(outPath); err == nil {
		return outPath, nil
	}

	v, err, _ := c.group.Do(outPath, func() (interface{}, error) {
		// Re-check now that we hold the singleflight slot, in case another
		// goroutine finished the same transcode while we were waiting.
		if _, err := os.Stat(outPath); err == nil {
			return outPath, nil
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return nil, fmt.Errorf("create cache dir: %w", err)
		}

		tmpPath := outPath + ".tmp"
		if err := RunFFmpeg(ctx, c.ffmpegPath, sourcePath, tmpPath, c.params, c.timeout); err != nil {
			os.Remove(tmpPath)
			return nil, err
		}

		// Atomic rename: readers never observe a partially-written cache file.
		if err := os.Rename(tmpPath, outPath); err != nil {
			os.Remove(tmpPath)
			return nil, fmt.Errorf("finalize cache entry: %w", err)
		}

		return outPath, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}
