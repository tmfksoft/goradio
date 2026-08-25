package audiosource

import (
	"path/filepath"
	"testing"
)

func TestSafeRelPathRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	cases := []string{
		"../outside.mp3",
		"../../etc/passwd",
		"GTASA/../../outside.mp3",
		"a/b/../../../outside.mp3",
	}
	for _, location := range cases {
		if _, _, err := SafeRelPath(root, location); err == nil {
			t.Errorf("SafeRelPath(%q) = nil error, want an escape error", location)
		}
	}
}

func TestSafeRelPathRejectsAbsolutePathEscape(t *testing.T) {
	root := t.TempDir()

	// An absolute path outside root must not be treated as an override --
	// filepath.Join(root, "/etc/passwd") folds it into a plain segment
	// under root, but confirm that's actually where it resolves, not that
	// it silently escapes.
	relPath, absPath, err := SafeRelPath(root, "/etc/passwd")
	if err != nil {
		t.Fatalf("SafeRelPath: %v", err)
	}
	if relPath != "etc/passwd" {
		t.Errorf("relPath = %q, want %q (an absolute location should be folded under root, not used as-is)", relPath, "etc/passwd")
	}
	wantAbs := filepath.Join(root, "etc", "passwd")
	if absPath != wantAbs {
		t.Errorf("absPath = %q, want %q", absPath, wantAbs)
	}
}

func TestSafeRelPathAllowsNormalPaths(t *testing.T) {
	root := t.TempDir()

	cases := map[string]string{
		"GTASA/KROSE/song.ogg":   "GTASA/KROSE/song.ogg",
		"./GTASA/KROSE/song.ogg": "GTASA/KROSE/song.ogg",
		"":                       "",
		".":                      "",
	}
	for location, want := range cases {
		relPath, _, err := SafeRelPath(root, location)
		if err != nil {
			t.Errorf("SafeRelPath(%q): %v", location, err)
			continue
		}
		if relPath != want {
			t.Errorf("SafeRelPath(%q) relPath = %q, want %q", location, relPath, want)
		}
	}
}

func TestSafeRelPathUsesForwardSlashes(t *testing.T) {
	root := t.TempDir()
	relPath, _, err := SafeRelPath(root, "GTASA/KROSE/song.ogg")
	if err != nil {
		t.Fatalf("SafeRelPath: %v", err)
	}
	if want := "GTASA/KROSE/song.ogg"; relPath != want {
		t.Errorf("relPath = %q, want %q (must be \"/\"-separated regardless of host OS, since this is also the TrackSource.location/DirectoryEntry.path convention)", relPath, want)
	}
}
