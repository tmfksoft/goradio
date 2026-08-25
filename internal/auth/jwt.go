// Package auth handles JWT signing/verification and slug-scoped
// authorization for the audio server's gRPC control plane.
package auth

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload used across GoRadio: the bearer may act on any
// station slug listed in Slugs. Entries may be glob patterns (as understood
// by path/filepath's Match, e.g. "*" for every station or "test-*" for a
// prefix); see HasSlug. Dirs is a second, independent scope restricting
// which directories under audio_root a QueueTrack local-file location (or
// a ListDirectory browse) may reference -- unlike Slugs, an entry grants
// recursive containment (an entry of "GTASA/KROSE" also covers
// "GTASA/KROSE/song.ogg" and any deeper path), matching how a directory
// grant naturally reads. An empty/absent Dirs means unrestricted -- the
// only backward-compatible default, since every token minted before this
// field existed has no dirs claim at all and must keep working exactly as
// before. See HasDir/CanBrowse. If ReadOnly is true, that's restricted to
// read-only calls (GetStatus, ListStations, SubscribeEvents,
// GetServerInfo, ListDirectory) -- every write RPC
// (RegisterStation, UnregisterStation, QueueTrack, RemoveFromQueue,
// ClearQueue, Skip, SkipTo, Pause, Resume, Seek, SeekBy) requires
// ReadOnly to be false; see RequireWrite.
type Claims struct {
	jwt.RegisteredClaims
	Slugs    []string `json:"slugs"`
	Dirs     []string `json:"dirs,omitempty"`
	ReadOnly bool     `json:"read_only,omitempty"`
}

// Sign mints an HS256 JWT authorizing the given slugs and directories,
// optionally restricted to read-only calls.
func Sign(secret []byte, slugs []string, dirs []string, subject string, ttl time.Duration, readOnly bool) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Slugs:    slugs,
		Dirs:     dirs,
		ReadOnly: readOnly,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// Verify checks the token's signature and expiry and returns its claims.
func Verify(secret []byte, tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// HasSlug reports whether the claims authorize the given station slug.
// Entries in Slugs may be exact slugs or glob patterns (e.g. "*" for every
// station, "test-*" for a prefix); see path/filepath's Match for syntax.
func (c *Claims) HasSlug(slug string) bool {
	for _, s := range c.Slugs {
		if s == slug {
			return true
		}
		if matched, err := filepath.Match(s, slug); err == nil && matched {
			return true
		}
	}
	return false
}

// HasDir reports whether dir -- a clean, "/"-separated path relative to
// audio_root, as produced by audiosource.SafeRelPath -- is fully
// authorized: an exact match against an entry in Dirs, recursively
// contained within one (an entry of "GTASA/KROSE" also authorizes
// "GTASA/KROSE/song.ogg"), or a filepath.Match glob against one. An empty
// Dirs means unrestricted, so every token minted before this field
// existed keeps working unchanged. Use this for anything that must be
// genuinely authorized -- QueueTrack's location, and a file entry in a
// ListDirectory response -- not just reachable while browsing; see
// CanBrowse for that weaker check.
func (c *Claims) HasDir(dir string) bool {
	if len(c.Dirs) == 0 {
		return true
	}
	for _, d := range c.Dirs {
		if d == "*" || d == dir || strings.HasPrefix(dir, d+"/") {
			return true
		}
		if matched, err := filepath.Match(d, dir); err == nil && matched {
			return true
		}
	}
	return false
}

// isAncestorOfAllowedDir reports whether dir is a path segment leading
// toward at least one allowed directory -- e.g. with Dirs =
// ["GTASA/KROSE"], both "" (the root) and "GTASA" are ancestors, even
// though neither is itself authorized by HasDir. This exists only so a
// browsing client can navigate down into an allowed subdirectory without
// every parent folder along the way looking unauthorized; it must never
// be used to authorize a file or a QueueTrack location.
//
// The root case needs handling on its own: strings.HasPrefix(d, "/") is
// false for every non-empty d, since a Dirs entry is never written with a
// leading slash, so the general rule alone would make the root
// unreachable even when something is allowed under it.
func (c *Claims) isAncestorOfAllowedDir(dir string) bool {
	if dir == "" {
		return len(c.Dirs) > 0
	}
	for _, d := range c.Dirs {
		if strings.HasPrefix(d, dir+"/") {
			return true
		}
	}
	return false
}

// CanBrowse reports whether dir may be listed: either HasDir already
// authorizes it outright, or it's merely an ancestor a client needs to
// pass through to reach something that is. Use this to decide whether a
// ListDirectory request is allowed at all, and for directory entries
// within a listing; a file entry must use plain HasDir instead, since a
// file can never be "on the way to" anything.
func (c *Claims) CanBrowse(dir string) bool {
	return c.HasDir(dir) || c.isAncestorOfAllowedDir(dir)
}
