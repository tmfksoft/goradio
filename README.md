# GoRadio

A lightweight, general-purpose radio streaming server, split into two roles
sharing one `radio` binary:

- **`radio serve`** — the audio server. Hosts stations, transcodes and
  hard-cuts queued audio via ffmpeg, and streams MP3 over plain HTTP
  (ICY-style headers) to browsers, in-game clients, and radio apps. Plays a
  looping silence clip whenever a station's queue is empty.
- **`radio station`** — a station controller: runs one Lua script per
  process, which decides what to queue on that station via a small `radio`
  API, with full HTTP, MySQL, and Redis access available to the script
  (`require("http")`, `require("sql")`, `require("redis")`).

The two talk over a JWT-authenticated gRPC control plane
(`proto/audioserver/v1`), so a controller doesn't have to be Lua — anything
that can speak that protocol works. The schema is published to a Buf Schema
Registry at `proto.prod.wtf/tmfksoft/goradio` so a controller in any
language can pull it without vendoring `.proto` files — see
[Writing a controller in another language](#writing-a-controller-in-another-language).

## Documentation

Full docs (usage, Lua scripting API, developer/protocol API) live in
[`docs/`](docs/), built with MkDocs + Material and published to GitHub
Pages at <https://tmfksoft.github.io/goradio/>. To build/preview
locally:

```sh
pip install -r docs/requirements.txt
mkdocs serve --config-file docs/mkdocs.yml
```

The `docs` workflow (`.github/workflows/docs.yml`) auto-deploys on every
push to `main` that touches `docs/` — this requires the repo's
**Settings → Pages → Source** set to **GitHub Actions** once, which only
someone with repo admin access can do.

## Prerequisites

- Go (see `go.mod`)
- `ffmpeg` on PATH (or configured via `transcode.ffmpeg_path`)
- `buf` + `protoc-gen-go` + `protoc-gen-go-grpc` only if you're changing the
  `.proto` files — generated code is committed under `gen/go/`, so a normal
  build doesn't need them.

## Pre-built binaries

Tagged releases (`vX.Y.Z`) publish `linux/amd64` and `windows/amd64`
archives to [GitHub Releases](https://github.com/tmfksoft/goradio/releases)
via `.github/workflows/release.yml` — each contains the `radio` binary,
`server.example.yaml`, `station.example.yaml`, a starter `station.lua`, and
a `GETTING_STARTED.txt`. To build the same archives locally (any commit,
not just tags):

```sh
make release              # -> dist/release/goradio-dev-{linux,windows}-amd64.{tar.gz,zip}
make release VERSION=v0.1.0
```

## Quickstart

```sh
make build   # -> bin/radio

# 1. Start the audio server
cp configs/server.example.yaml server.yaml   # edit auth.jwt_secret, audio.audio_root, etc.
./bin/radio serve --config server.yaml

# 2. Mint a token authorizing a station slug
./bin/radio tokengen -secret <jwt_secret> myfm
# -> paste the printed token into station.yaml's auth.jwt

# 3. Run a station controller
cp configs/station.example.yaml station.yaml
./bin/radio station --config station.yaml --script testdata/station-scripts/example.lua myfm "My FM"
```

Listen at `http://<http.listen_addr>/stream/<slug>`.

## The `radio` Lua API

Deliberately minimal for now — enough to prove the protocol, not a
genre-specific content engine. It's the primitive layer you'd build a
richer station roster (songs, idents, DJ chatter, callers, ad rotation) on
top of, in Lua, without needing to touch Go:

```lua
local info = radio.register(slug, name, description [, {low_queue_threshold = 3}])
radio.queue(source, mode)      -- source: path/URL string or {type=,location=,title=,artist=}
                                -- mode: "APPEND" (default) | "PLAY_NEXT" | "PLAY_NOW_INTERRUPT"
                                -- an http(s):// source is auto-detected as a file or a live
                                -- stream (e.g. Icecast) and relayed continuously if live
radio.dequeue(queue_id)        -- remove one still-pending item
radio.clear_queue([stop_current]) -- remove all pending items, optionally interrupt what's playing too
radio.skip()                   -- interrupt current playback only, leaving the queue intact
local status = radio.status()  -- includes status.queue, status.current_track, .duration_seconds/.elapsed_seconds
radio.every(seconds, fn)
radio.after(seconds, fn)
radio.on_track_started(fn)
radio.on_track_ended(fn)
radio.on_error(fn)
radio.on_queue_low(fn)         -- fires (edge-triggered) at/below low_queue_threshold
radio.args                     -- array of CLI args after --config/--script

local http  = require("http")   -- full, unrestricted HTTP client
local sql   = require("sql")    -- full MySQL access via database/sql
local redis = require("redis")  -- full Redis access: KV, lists, pub/sub
```

See `testdata/station-scripts/example.lua` for a working reference script,
and the [docs](https://tmfksoft.github.io/goradio/lua-api/) for the full
API reference.

**VS Code autocomplete/type-checking:** open this repo (or a release
archive) as your workspace and install the **Lua** extension
(`sumneko.lua`) — `.luarc.json` and `lua-types/` are already set up to
give you real intellisense for `radio`/`http`/`sql`/`redis`. See
[Editor Support](https://tmfksoft.github.io/goradio/lua-api/editor-support/).

## Writing a controller in another language

The protocol (`AudioServerService`, `RegisterStation`/`QueueTrack`/`GetStatus`/`SubscribeEvents`)
is published to a self-hosted Buf Schema Registry, so you don't need this
repo's `.proto` files at all — just `buf` and your language's protoc
plugins.

1. Write a `buf.gen.yaml` with the plugins for your language, e.g. for Python:
   ```yaml
   version: v2
   plugins:
     - remote: buf.build/protocolbuffers/python
       out: gen/python
     - remote: buf.build/grpc/python
       out: gen/python
   ```
   (swap in whatever local or remote plugins your language/toolchain uses).
2. Generate straight from the registry — no local checkout of this repo needed:
   ```sh
   buf generate proto.prod.wtf/tmfksoft/goradio
   ```
3. Implement a client against the generated `AudioServerService` stub:
   - Dial the audio server's gRPC address (plaintext this phase — no TLS, see Known gaps below).
   - Attach `authorization: Bearer <jwt>` as gRPC metadata on every call (mint a token with `radio tokengen`).
   - Call `RegisterStation` once, then `QueueTrack` to queue playback, `GetStatus` for an on-demand snapshot, and open the `SubscribeEvents` server-streaming call to receive `TRACK_STARTED`/`TRACK_ENDED`/`QUEUE_UPDATED`/`ERROR`/etc. events in real time.

`internal/luastation` in this repo is just a first-party implementation of
that same client contract in Go+Lua — nothing about the protocol is
Lua-specific.

To publish a new schema version after changing the `.proto` files:
```sh
make proto        # regenerate gen/go/ for this repo's own build
make proto-push    # requires: buf registry login proto.prod.wtf
```

## Notes

- `radio station --config station.yaml --script station.lua [args...]` — any
  args after the two flags are passed straight through to the script
  (`radio.args`), so one script can drive multiple stations by how it's
  invoked.
- A station's own optional local control API (`api.enabled` in
  `station.yaml`) currently exposes only a placeholder, API-key-gated
  `GET /status` — richer control endpoints are future work.
- `GET /stations/{slug}/now-playing` is a public JSON mirror of `GetStatus`
  (title/artist/duration/elapsed/listeners) for pairing a plain HTML/JS
  player with a progress bar or a Discord bot — no gRPC client needed. A
  bearer token additionally unlocks raw file paths/URLs, which aren't
  public by default. See the [docs](https://tmfksoft.github.io/goradio/developer-api/now-playing-http-api/).
- `radio tokengen -readonly` mints a token that can `GetStatus`/
  `SubscribeEvents` but gets `PermissionDenied` on every write RPC — for
  observers (dashboards, bots) you don't want able to touch playback. Not
  usable for `radio station` itself, since it always registers on startup.
- Known gaps: no crossfade (hard cut only), no ICY mid-stream metadata, no
  TLS on the gRPC transport, no bundled genre-specific content logic
  (songs/idents/DJ chatter/callers/adverts) yet — build that in Lua on top
  of the primitives above.
