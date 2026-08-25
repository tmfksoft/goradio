package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goradioserver/goradio/internal/registry"
)

const testSecret = "test-secret"

func newTestServer(t *testing.T) (*httptest.Server, *registry.Registry) {
	t.Helper()

	reg := registry.New()
	srv := httptest.NewServer(NewMux(slog.New(slog.DiscardHandler), reg, []byte(testSecret)))
	t.Cleanup(srv.Close)

	return srv, reg
}

func TestCORS_AllowOriginOnGet(t *testing.T) {
	srv, _ := newTestServer(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/stations", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://example.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
}

// The preflight has to succeed even for a slug nothing has registered --
// the browser sends OPTIONS before it knows whether the eventual GET will
// 404, so the middleware must short-circuit ahead of mux dispatch.
func TestCORS_PreflightNowPlaying(t *testing.T) {
	srv, _ := newTestServer(t)

	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/stations/nonexistent/now-playing", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "Authorization")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("Access-Control-Allow-Methods is empty")
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); got != "Authorization" {
		t.Fatalf("Access-Control-Allow-Headers = %q, want %q", got, "Authorization")
	}
}

func TestCORS_HeaderOnRealNowPlaying(t *testing.T) {
	srv, reg := newTestServer(t)
	reg.Register("myfm", "My FM", "", "", nil, 0, nil)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/stations/myfm/now-playing", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://example.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}
