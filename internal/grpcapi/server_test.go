package grpcapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/goradioserver/goradio/gen/go/audioserver/v1/audioserverv1connect"
	"github.com/goradioserver/goradio/internal/auth"
	"github.com/goradioserver/goradio/internal/registry"
)

const testSecret = "test-secret"

// newTestServer stands the Connect handler up behind httptest, which
// serves HTTP/1.1 only. That's deliberate: it's what proves a caller
// needs nothing but an HTTP/1.1 client and a JSON parser to drive the
// API -- the reason the Connect protocol is exposed at all (see
// docs/content/developer-api/http-json-api.md). A regression that made
// the API HTTP/2-only would fail here rather than in the field.
func newTestServer(t *testing.T) (*httptest.Server, *registry.Registry) {
	t.Helper()
	return newTestServerWithAudioRoot(t, "")
}

// newTestServerWithAudioRoot is newTestServer with an explicit audio_root,
// for tests exercising ListDirectory or QueueTrack's directory-scope
// check against real files -- t.TempDir() is the normal caller.
func newTestServerWithAudioRoot(t *testing.T, audioRoot string) (*httptest.Server, *registry.Registry) {
	t.Helper()

	reg := registry.New()
	api := NewServer(slog.New(slog.DiscardHandler), reg, nil, nil, "http://localhost:8080", audioRoot)
	path, handler := audioserverv1connect.NewAudioServerServiceHandler(
		api,
		connect.WithInterceptors(auth.NewInterceptor([]byte(testSecret))),
	)

	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, reg
}

func token(t *testing.T, readOnly bool, slugs ...string) string {
	t.Helper()
	return tokenWithDirs(t, readOnly, nil, slugs...)
}

func tokenWithDirs(t *testing.T, readOnly bool, dirs []string, slugs ...string) string {
	t.Helper()
	tok, err := auth.Sign([]byte(testSecret), slugs, dirs, "test", time.Hour, readOnly)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

// post makes the call the way a client with no gRPC library would: a
// plain JSON POST to /<service>/<Rpc>.
func post(t *testing.T, srv *httptest.Server, rpc, bearer, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/audioserver.v1.AudioServerService/"+rpc,
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.ProtoMajor != 1 {
		t.Fatalf("expected the call to succeed over HTTP/1.x, got HTTP/%d", resp.ProtoMajor)
	}

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(out)
}

func TestJSONOverHTTP1(t *testing.T) {
	srv, _ := newTestServer(t)

	code, body := post(t, srv, "GetServerInfo", token(t, false, "*"), `{}`)
	if code != http.StatusOK {
		t.Fatalf("GetServerInfo: status = %d, body = %s", code, body)
	}

	var got struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("response is not JSON (%v): %s", err, body)
	}
	if got.Version == "" {
		t.Errorf("expected a version in the response, got: %s", body)
	}
}

// TestJSONErrorsAreJSON pins the error contract a hand-written client
// branches on: an error code in a JSON body, plus an HTTP status that
// mirrors it.
func TestJSONErrorsAreJSON(t *testing.T) {
	srv, _ := newTestServer(t)

	tests := []struct {
		name     string
		rpc      string
		bearer   string
		body     string
		wantCode int
		wantErr  string
	}{
		{
			name:     "no token",
			rpc:      "GetServerInfo",
			bearer:   "",
			body:     `{}`,
			wantCode: http.StatusUnauthorized,
			wantErr:  "unauthenticated",
		},
		{
			name:     "malformed token",
			rpc:      "GetServerInfo",
			bearer:   "not-a-jwt",
			body:     `{}`,
			wantCode: http.StatusUnauthorized,
			wantErr:  "unauthenticated",
		},
		{
			name:     "token does not cover slug",
			rpc:      "GetStatus",
			bearer:   token(t, false, "other-*"),
			body:     `{"slug":"myfm"}`,
			wantCode: http.StatusForbidden,
			wantErr:  "permission_denied",
		},
		{
			name:     "read-only token on a write rpc",
			rpc:      "Skip",
			bearer:   token(t, true, "*"),
			body:     `{"slug":"myfm"}`,
			wantCode: http.StatusForbidden,
			wantErr:  "permission_denied",
		},
		{
			name:     "missing required field",
			rpc:      "RegisterStation",
			bearer:   token(t, false, "*"),
			body:     `{"slug":""}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_argument",
		},
		{
			name:     "unknown station",
			rpc:      "Skip",
			bearer:   token(t, false, "*"),
			body:     `{"slug":"nope"}`,
			wantCode: http.StatusNotFound,
			wantErr:  "not_found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, body := post(t, srv, tc.rpc, tc.bearer, tc.body)
			if code != tc.wantCode {
				t.Errorf("status = %d, want %d (body: %s)", code, tc.wantCode, body)
			}

			var got struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("error body is not JSON (%v): %s", err, body)
			}
			if got.Code != tc.wantErr {
				t.Errorf("code = %q, want %q (body: %s)", got.Code, tc.wantErr, body)
			}
		})
	}
}

// TestJSONWireConventions pins the three protobuf-JSON behaviours most
// likely to break a hand-written client, and the ones
// docs/content/developer-api/http-json-api.md warns about: camelCase
// field names, zero-valued fields omitted entirely, and 64-bit integers
// quoted as strings.
func TestJSONWireConventions(t *testing.T) {
	srv, reg := newTestServer(t)
	reg.Register("myfm", "My FM", "", "", nil, 0, nil)

	// Uptime is the only int64 on this response that's reachable without
	// driving actual playback, and it's truncated to whole seconds -- so
	// wait out one full second to get a non-zero value to assert on.
	// Everything else here is checked against the immediate response.
	time.Sleep(1100 * time.Millisecond)

	code, body := post(t, srv, "GetStatus", token(t, false, "*"), `{"slug":"myfm"}`)
	if code != http.StatusOK {
		t.Fatalf("GetStatus: status = %d, body = %s", code, body)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("response is not JSON (%v): %s", err, body)
	}

	// Field names are lowerCamelCase on the wire, not the proto's
	// snake_case -- clients read isRegistered, never is_registered.
	if _, ok := raw["is_registered"]; ok {
		t.Errorf("expected camelCase field names, found snake_case: %s", body)
	}
	if _, ok := raw["isRegistered"]; !ok {
		t.Errorf("expected isRegistered in the response: %s", body)
	}

	// Zero-valued fields are omitted rather than sent explicitly, so a
	// client has to treat a missing key as the zero value. This station
	// has no listeners, so the field should be absent entirely.
	if v, ok := raw["listenerCount"]; ok {
		t.Errorf("expected zero-valued listenerCount to be omitted, got %s", v)
	}

	// int64 fields cross the wire quoted. This is the one that catches
	// people: a client expecting a JSON number gets a string.
	uptime, ok := raw["uptimeSeconds"]
	if !ok {
		t.Fatalf("expected a non-zero uptimeSeconds after waiting: %s", body)
	}
	if !strings.HasPrefix(string(uptime), `"`) {
		t.Errorf("expected int64 uptimeSeconds to be a JSON string, got %s", uptime)
	}
}
