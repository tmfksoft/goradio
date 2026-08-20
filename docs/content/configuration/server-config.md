# Audio server configuration (`server.yaml`)

Loaded by [`radio serve`](../cli/serve.md). Start from
[`configs/server.example.yaml`](https://github.com/tmfksoft/goradio/blob/main/configs/server.example.yaml).

```yaml
grpc:
  listen_addr: "0.0.0.0:9090"

http:
  listen_addr: "0.0.0.0:8080"
  public_base_url: "http://localhost:8080"

auth:
  jwt_secret: "CHANGE_ME"

audio:
  audio_root: "./data/audio"

transcode:
  ffmpeg_path: "ffmpeg"
  cache_dir: "./data/transcode-cache"
  bitrate_kbps: 128
  sample_rate: 44100
  channels: 2
  worker_count: 4
  timeout_seconds: 60

fetch:
  max_download_bytes: 52428800 # 50 MiB

silence:
  clip_duration_seconds: 15

logging:
  level: "info" # debug|info|warn|error
```

Every field shown above is optional — omitted fields fall back to these
same defaults.

## `grpc`

| Field | Default | Description |
|---|---|---|
| `listen_addr` | `0.0.0.0:9090` | Address the gRPC control plane listens on |

## `http`

| Field | Default | Description |
|---|---|---|
| `listen_addr` | `0.0.0.0:8080` | Address the public HTTP streaming server listens on |
| `public_base_url` | `http://localhost:8080` | Used to build the `stream_url` returned by `RegisterStation` — set this to whatever URL listeners will actually use to reach this server |

## `auth`

| Field | Default | Description |
|---|---|---|
| `jwt_secret` | `""` | HS256 shared secret used to verify every gRPC call's bearer token. Can also be supplied via the `GORADIO_JWT_SECRET` environment variable, which **takes precedence** over this field — prefer that for anything beyond local dev, so the secret doesn't sit in a config file |

The server logs a warning on boot if this is empty or left as `CHANGE_ME`.

## `audio`

| Field | Default | Description |
|---|---|---|
| `audio_root` | `./data/audio` | Root directory that `LOCAL_FILE` track sources are resolved relative to. Paths are checked to stay inside this directory — `../` traversal is rejected |

## `transcode`

| Field | Default | Description |
|---|---|---|
| `ffmpeg_path` | `ffmpeg` | Path to the ffmpeg binary |
| `cache_dir` | `./data/transcode-cache` | Where transcoded (and downloaded) files are cached on disk |
| `bitrate_kbps` | `128` | Target MP3 bitrate — every source is transcoded to this exact bitrate/sample rate/channel count so hard-cut concatenation is byte-clean |
| `sample_rate` | `44100` | Target sample rate (Hz) |
| `channels` | `2` | Target channel count |
| `worker_count` | `4` | Size of the background prefetch worker pool (bounds how many concurrent `ffmpeg` processes a burst of `QueueTrack` calls can spawn) |
| `timeout_seconds` | `60` | Per-transcode timeout |

Changing any of `bitrate_kbps`/`sample_rate`/`channels` changes the cache
key namespace implicitly (the cache directory isn't automatically purged),
and regenerates the silence clip on next boot since it's also keyed by
these params.

## `fetch`

| Field | Default | Description |
|---|---|---|
| `max_download_bytes` | `52428800` (50 MiB) | Hard cap on `HTTP_URL` track source downloads — exceeding it fails the fetch cleanly rather than truncating the file |

## `silence`

| Field | Default | Description |
|---|---|---|
| `clip_duration_seconds` | `15` | Length of the generated silence clip, looped whenever a station's queue is empty |

## `logging`

| Field | Default | Description |
|---|---|---|
| `level` | `info` | One of `debug`, `info`, `warn`, `error` |
