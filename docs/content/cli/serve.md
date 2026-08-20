# `radio serve`

Runs the audio server: the station registry, the per-station queue/player
engine, the transcode cache, and both the gRPC control plane and the public
HTTP streaming endpoints.

```sh
radio serve [--config server.yaml]
```

| Flag | Default | Description |
|---|---|---|
| `--config` | `server.yaml` | Path to the [audio server config file](../configuration/server-config.md) |

## What it does on boot

1. Loads and validates the config (warns if `auth.jwt_secret` is unset or
   left as the placeholder `CHANGE_ME`).
2. Generates (or reuses, if already cached) a looping silent MP3 clip
   matching the configured bitrate/sample rate/channels.
3. Starts a bounded ffmpeg worker pool for transcoding queued sources.
4. Starts the gRPC server (JWT auth interceptors attached) on
   `grpc.listen_addr`.
5. Starts the public HTTP server on `http.listen_addr`, serving
   `/stream/{slug}`, `/healthz`, and `/stations`.

Stations are **not** pre-configured here — they're registered dynamically
by station controllers at runtime (see `RegisterStation` in the
[Developer / Protocol API](../developer-api/protocol-reference.md)).
Registration is in-memory only: restarting `radio serve` clears every
registered station, and controllers are expected to reconnect and
re-register (both the bundled Lua engine and any protocol-compliant
controller should do this).

## Signals

`radio serve` shuts down gracefully on `SIGINT`/`SIGTERM`: the gRPC server
stops accepting new calls and the HTTP server is given 5 seconds to drain
in-flight requests.

## Listener-facing HTTP endpoints

| Route | Auth | Description |
|---|---|---|
| `GET /stream/{slug}` | none (public) | The live MP3 stream for a registered station |
| `GET /healthz` | none | Liveness probe, always `200 ok` |
| `GET /stations` | none | JSON list of registered stations: slug, name, listener count |

Only the gRPC control plane is JWT-protected — anyone can listen to a
public stream without a token.
