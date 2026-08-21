# CLAUDE.md

Guidance for Claude Code (or any future contributor) working in this repo.

## Project shape

GoRadio is an audio server (`radio serve`) plus a Lua station controller
(`radio station`), talking to each other over a shared gRPC protocol
(`audioserver.v1.AudioServerService`), defined in `proto/audioserver/v1/*.proto`
and published to a self-hosted Buf Schema Registry at
`proto.prod.wtf/tmfksoft/goradio`.

## After adding or changing a gRPC RPC or message field

A proto change is never self-contained here — the same capability is meant
to be reachable from Go, from Lua station scripts, and from the docs at
once. Skipping a step below leaves a real gap: a doc claiming a capability
doesn't exist when it does, a Lua script unable to reach something the Go
API already supports, or a BSR consumer building against a stale schema.
Work through all of these every time:

1. **Regenerate stubs**: `make proto` (needs `buf`, `protoc-gen-go`,
   `protoc-gen-go-grpc` on `PATH` — check `$(go env GOPATH)/bin` if they
   were installed via `go install`).
2. **Wire the Go server** (`internal/grpcapi/server.go`): new handlers go
   through `auth.RequireSlug`/`auth.RequireWrite` like every existing one.
   A **write** RPC needs both; read-only RPCs (`GetStatus`,
   `ListStations`, `SubscribeEvents`) only need `RequireSlug` (or nothing,
   for `ListStations`, which filters unauthorized stations out rather
   than rejecting the call).
3. **Update the write-RPC lists in doc comments**: `internal/auth/jwt.go`
   and `internal/auth/interceptor.go` both hardcode the list of write
   RPCs in a comment. Keep them in sync or the comments describe stale
   gating behavior.
4. **Lua API** (`internal/luastation/`): give station scripts a wrapper
   for the new capability unless there's a specific reason not to.
   - `api.go`: add the `radio.xxx` function, register it in
     `setupLuaEnvironment`'s `L.SetFuncs` map.
   - If it affects registration state (like a new `RegisterStationRequest`
     field), thread it through `engine.go`'s `registerInfo` struct,
     `setRegisterInfo`, and `registerWithRetry` too — the event-stream
     reconnect loop calls these on every dropped connection, so a field
     missed there silently reverts to its zero value on reconnect.
   - `lua-types/radio.lua`: update the `---@class`/`---@field` type
     stubs for editor autocomplete. The file's own header says "keep in
     sync with internal/luastation/api.go and engine.go" — take that
     literally.
5. **Documentation**:
   - `docs/content/developer-api/protocol-reference.md` — the primary
     spec. Update the `service AudioServerService { ... }` block, the RPC
     count sentence right after it, the write-RPC list in the
     Authentication section, and the RPC's own `##` section (add one if
     new).
   - `docs/content/lua-api/index.md` — the flat `radio.*` function list.
   - `docs/content/lua-api/register-queue-status.md` (or whichever page
     fits the function) — the detailed per-function docs with examples.
   - `docs/content/cli/tokengen.md` — its write-RPC list, if the new RPC
     is a write RPC.
   - `README.md` — the Lua API cheat sheet block. It has drifted behind
     reality before; check it every time rather than assuming it's current.
6. **Publish the schema**: `make proto-push` (`buf push proto`) — requires
   `buf registry login proto.prod.wtf` once per machine (the credential
   lives in `~/.netrc`; `buf registry whoami proto.prod.wtf` confirms
   you're logged in). Do this **after** the Go/Lua/docs changes are
   committed, not before, so the pushed schema version corresponds to a
   complete, working implementation rather than a schema nothing
   implements yet.
7. **Test**: `go build ./... && go vet ./... && go test ./... -race`,
   and `gofmt -l .` should report nothing outside `gen/`. A change to the
   playback state machine (pause/seek/skip-style interactions) is worth a
   real test in `internal/playback` — those interaction bugs are easy to
   introduce and easy to miss by inspection alone; see
   `internal/playback/player_test.go` for the pattern (drive
   `playLocalItem` directly against small on-disk test clips, assert on
   `Station`'s exported state).

## Versioning and release

Tag releases as `vX.Y.Z` (annotated git tag, pushed to `origin`) — pushing
a `v*` tag triggers `.github/workflows/release.yml`, which cross-builds
and publishes GitHub release binaries automatically. Bump minor for
additive/backward-compatible proto changes (new RPC, new optional field),
patch for fixes, major for anything that breaks an existing client.
