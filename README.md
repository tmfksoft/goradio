# GoRadio

A lightweight, general-purpose radio streaming server, split into two roles
sharing one `radio` binary:

- **`radio serve`** — the audio server. Hosts stations, transcodes and
  hard-cuts queued audio via ffmpeg, and streams MP3 over plain HTTP
  (ICY-style headers) to browsers, in-game clients, and radio apps. Plays a
  looping silence clip whenever a station's queue is empty.
- **`radio station`** — a station controller: runs one Lua script per
  process, which decides what to queue on that station via a small `radio`
  API, with full HTTP and MySQL access available to the script
  (`require("http")`, `require("sql")`).

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
local info = radio.register(slug, name, description)
radio.queue(source, mode)      -- source: path/URL string or {type=,location=,title=,artist=}
                                -- mode: "APPEND" (default) | "PLAY_NEXT" | "PLAY_NOW_INTERRUPT"
local status = radio.status()
radio.every(seconds, fn)
radio.after(seconds, fn)
radio.on_track_started(fn)
radio.on_track_ended(fn)
radio.on_error(fn)
radio.args                     -- array of CLI args after --config/--script
```

See `testdata/station-scripts/example.lua` for a working reference script.

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
- Known gaps: no crossfade (hard cut only), no ICY mid-stream metadata, no
  TLS on the gRPC transport, no bundled genre-specific content logic
  (songs/idents/DJ chatter/callers/adverts) yet — build that in Lua on top
  of the primitives above.
