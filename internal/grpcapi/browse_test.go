package grpcapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// newAudioFixture lays out a small nested audio_root under t.TempDir():
//
//	GTASA/KROSE/song.ogg
//	GTASA/RadioX/song.ogg
//	GTAVC/Emotion/song.ogg
//
// matching goradio-gta's real GAME/STATION layout closely enough to
// exercise recursive containment and self-filtering meaningfully.
func newAudioFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{
		"GTASA/KROSE/song.ogg",
		"GTASA/RadioX/song.ogg",
		"GTAVC/Emotion/song.ogg",
	} {
		full := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("fake audio"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	return root
}

func TestListDirectoryUnrestrictedToken(t *testing.T) {
	root := newAudioFixture(t)
	srv, _ := newTestServerWithAudioRoot(t, root)

	code, body := post(t, srv, "ListDirectory", token(t, false, "*"), `{}`)
	if code != http.StatusOK {
		t.Fatalf("ListDirectory: status = %d, body = %s", code, body)
	}
	names := entryNames(t, body)
	assertSameSet(t, names, []string{"GTASA", "GTAVC"})
}

func TestListDirectorySelfFiltersRoot(t *testing.T) {
	root := newAudioFixture(t)
	srv, _ := newTestServerWithAudioRoot(t, root)
	bearer := tokenWithDirs(t, false, []string{"GTASA/KROSE"}, "*")

	code, body := post(t, srv, "ListDirectory", bearer, `{}`)
	if code != http.StatusOK {
		t.Fatalf("ListDirectory: status = %d, body = %s", code, body)
	}
	names := entryNames(t, body)
	assertSameSet(t, names, []string{"GTASA"}) // GTAVC hidden entirely
}

func TestListDirectorySelfFiltersSubdirectory(t *testing.T) {
	root := newAudioFixture(t)
	srv, _ := newTestServerWithAudioRoot(t, root)
	bearer := tokenWithDirs(t, false, []string{"GTASA/KROSE"}, "*")

	code, body := post(t, srv, "ListDirectory", bearer, `{"path":"GTASA"}`)
	if code != http.StatusOK {
		t.Fatalf("ListDirectory: status = %d, body = %s", code, body)
	}
	names := entryNames(t, body)
	assertSameSet(t, names, []string{"KROSE"}) // RadioX hidden
}

func TestListDirectoryShowsFullyAuthorizedContents(t *testing.T) {
	root := newAudioFixture(t)
	srv, _ := newTestServerWithAudioRoot(t, root)
	bearer := tokenWithDirs(t, false, []string{"GTASA/KROSE"}, "*")

	code, body := post(t, srv, "ListDirectory", bearer, `{"path":"GTASA/KROSE"}`)
	if code != http.StatusOK {
		t.Fatalf("ListDirectory: status = %d, body = %s", code, body)
	}
	names := entryNames(t, body)
	assertSameSet(t, names, []string{"song.ogg"})
}

func TestListDirectoryRejectsUnauthorizedPath(t *testing.T) {
	root := newAudioFixture(t)
	srv, _ := newTestServerWithAudioRoot(t, root)
	bearer := tokenWithDirs(t, false, []string{"GTASA/KROSE"}, "*")

	code, body := post(t, srv, "ListDirectory", bearer, `{"path":"GTAVC"}`)
	if code != http.StatusForbidden {
		t.Fatalf("ListDirectory(GTAVC): status = %d, want 403, body = %s", code, body)
	}
}

func TestListDirectoryRejectsTraversal(t *testing.T) {
	root := newAudioFixture(t)
	srv, _ := newTestServerWithAudioRoot(t, root)

	code, body := post(t, srv, "ListDirectory", token(t, false, "*"), `{"path":"../../etc"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("ListDirectory(../../etc): status = %d, want 400, body = %s", code, body)
	}
}

func TestQueueTrackRejectsFileOutsideDirs(t *testing.T) {
	root := newAudioFixture(t)
	srv, reg := newTestServerWithAudioRoot(t, root)
	reg.Register("myfm", "My FM", "", "", nil, 0, nil)
	bearer := tokenWithDirs(t, false, []string{"GTASA/KROSE"}, "*")

	code, body := post(t, srv, "QueueTrack", bearer,
		`{"slug":"myfm","source":{"type":"TRACK_SOURCE_TYPE_LOCAL_FILE","location":"GTASA/RadioX/song.ogg"}}`)
	if code != http.StatusForbidden {
		t.Fatalf("QueueTrack(GTASA/RadioX/song.ogg): status = %d, want 403, body = %s", code, body)
	}
}

func TestQueueTrackAllowsFileInsideDirs(t *testing.T) {
	root := newAudioFixture(t)
	srv, reg := newTestServerWithAudioRoot(t, root)
	reg.Register("myfm", "My FM", "", "", nil, 0, nil)
	bearer := tokenWithDirs(t, false, []string{"GTASA/KROSE"}, "*")

	code, body := post(t, srv, "QueueTrack", bearer,
		`{"slug":"myfm","source":{"type":"TRACK_SOURCE_TYPE_LOCAL_FILE","location":"GTASA/KROSE/song.ogg"}}`)
	if code != http.StatusOK {
		t.Fatalf("QueueTrack(GTASA/KROSE/song.ogg): status = %d, want 200, body = %s", code, body)
	}
}

func TestQueueTrackUnrestrictedTokenAllowsAnyFile(t *testing.T) {
	root := newAudioFixture(t)
	srv, reg := newTestServerWithAudioRoot(t, root)
	reg.Register("myfm", "My FM", "", "", nil, 0, nil)

	// No -dirs at all -- must behave exactly as it did before this
	// feature existed, for every token minted before the server upgraded.
	code, body := post(t, srv, "QueueTrack", token(t, false, "*"),
		`{"slug":"myfm","source":{"type":"TRACK_SOURCE_TYPE_LOCAL_FILE","location":"GTAVC/Emotion/song.ogg"}}`)
	if code != http.StatusOK {
		t.Fatalf("QueueTrack with an unrestricted token: status = %d, want 200, body = %s", code, body)
	}
}

func TestQueueTrackHTTPURLBypassesDirScope(t *testing.T) {
	root := newAudioFixture(t)
	srv, reg := newTestServerWithAudioRoot(t, root)
	reg.Register("myfm", "My FM", "", "", nil, 0, nil)
	bearer := tokenWithDirs(t, false, []string{"GTASA/KROSE"}, "*")

	// An HTTP(S) source never touches audio_root, so Dirs shouldn't apply
	// to it at all -- this only checks the request is accepted (200), not
	// that the URL is ever actually fetched (no network access in this
	// test; the async prefetch will fail harmlessly with no prefetcher
	// configured, same as every other QueueTrack test here).
	code, body := post(t, srv, "QueueTrack", bearer,
		`{"slug":"myfm","source":{"type":"TRACK_SOURCE_TYPE_HTTP_URL","location":"https://example.com/track.mp3"}}`)
	if code != http.StatusOK {
		t.Fatalf("QueueTrack with an HTTP_URL source: status = %d, want 200, body = %s", code, body)
	}
}

// entryNames extracts DirectoryEntry.name from a ListDirectory JSON
// response body.
func entryNames(t *testing.T, body string) []string {
	t.Helper()
	var resp struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("response is not JSON (%v): %s", err, body)
	}
	names := make([]string, len(resp.Entries))
	for i, e := range resp.Entries {
		names[i] = e.Name
	}
	return names
}

func assertSameSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	for _, g := range got {
		if !wantSet[g] {
			t.Fatalf("got %v, want %v (unexpected entry %q)", got, want, g)
		}
	}
}
