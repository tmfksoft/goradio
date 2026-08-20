// Package fetch downloads HTTP(S) track sources with a hard size cap.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// ErrTooLarge is returned (wrapped) when a download exceeds maxBytes.
var ErrTooLarge = errors.New("downloaded content exceeds max size")

// Download fetches rawURL into a new temp file under destDir, capped at
// maxBytes. On success the caller owns the returned file and must remove it
// once done with it. If the source exceeds maxBytes, Download returns
// ErrTooLarge rather than silently truncating the file.
func Download(ctx context.Context, rawURL string, maxBytes int64, destDir string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported url scheme %q (only http/https allowed)", u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch %q: unexpected status %s", rawURL, resp.Status)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create download dir: %w", err)
	}

	out, err := os.CreateTemp(destDir, "download-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer out.Close()

	limited := io.LimitReader(resp.Body, maxBytes+1)
	n, err := io.Copy(out, limited)
	if err != nil {
		os.Remove(out.Name())
		return "", fmt.Errorf("download %q: %w", rawURL, err)
	}
	if n > maxBytes {
		os.Remove(out.Name())
		return "", fmt.Errorf("%w: %q exceeded %d bytes", ErrTooLarge, rawURL, maxBytes)
	}

	return out.Name(), nil
}
