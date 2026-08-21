// Package fetch downloads HTTP(S) track sources with a hard size cap, and
// classifies whether a source is a finite file or a continuous live stream.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrTooLarge is returned (wrapped) when a download exceeds maxBytes.
var ErrTooLarge = errors.New("downloaded content exceeds max size")

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// Open issues the GET request and returns the still-open response once
// headers arrive (before any body is read), so the caller can classify
// the source (see IsLiveStream) before deciding whether to download it.
// The caller must close resp.Body.
func Open(ctx context.Context, rawURL string) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported url scheme %q (only http/https allowed)", u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", rawURL, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch %q: unexpected status %s", rawURL, resp.Status)
	}

	return resp, nil
}

// IsLiveStream reports whether a response looks like a continuous live
// stream (an Icecast/Shoutcast mountpoint or similar) rather than a
// downloadable file: no Content-Length (the response never completes on
// its own), or the presence of ICY/Icecast-style metadata headers
// (icy-*/ice-*), which finite audio files don't send.
func IsLiveStream(resp *http.Response) bool {
	if resp.ContentLength < 0 {
		return true
	}
	for key := range resp.Header {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "icy-") || strings.HasPrefix(lower, "ice-") {
			return true
		}
	}
	return false
}

// SaveToFile downloads resp's body into a new temp file under destDir,
// capped at maxBytes. On success the caller owns the returned file and
// must remove it once done with it. If the source exceeds maxBytes,
// SaveToFile returns ErrTooLarge rather than silently truncating the
// file. Does not close resp.Body; the caller (typically via Download)
// owns that.
func SaveToFile(resp *http.Response, maxBytes int64, destDir string) (string, error) {
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
		return "", fmt.Errorf("download %q: %w", resp.Request.URL, err)
	}
	if n > maxBytes {
		os.Remove(out.Name())
		return "", fmt.Errorf("%w: %q exceeded %d bytes", ErrTooLarge, resp.Request.URL, maxBytes)
	}

	return out.Name(), nil
}

// Download fetches rawURL into a new temp file under destDir, capped at
// maxBytes. Equivalent to Open followed by SaveToFile, for callers that
// don't need to inspect headers first.
func Download(ctx context.Context, rawURL string, maxBytes int64, destDir string) (string, error) {
	resp, err := Open(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	return SaveToFile(resp, maxBytes, destDir)
}
