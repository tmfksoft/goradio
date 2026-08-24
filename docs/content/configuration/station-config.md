# Station configuration (`station.yaml`)

Loaded by [`radio station`](../cli/station.md). Start from
[`configs/station.example.yaml`](https://github.com/goradioserver/goradio/blob/main/configs/station.example.yaml).

```yaml
server:
  grpc_addr: "localhost:9090"

auth:
  jwt: "eyJhbGciOi..."

station:
  slug: "myfm"

api:
  enabled: false
  bind_host: "127.0.0.1:8091"
  api_key: "CHANGE_ME"

logging:
  level: "info"
```

## `server`

| Field | Default | Description |
|---|---|---|
| `grpc_addr` | `localhost:9090` | The audio server's gRPC address |

`grpc_addr` accepts either a bare `host:port` (dialed in plaintext — the
default, for talking directly to `radio serve`'s gRPC port) or a URL with a
scheme, for talking to a TLS-terminating reverse proxy in front of it:
`https://`/`grpcs://` dial with TLS (defaulting to port 443 if none is
given), `http://`/`grpc://` dial in plaintext (defaulting to port 80). E.g.
`grpc_addr: "https://radio-rpc.example.com"`.

## `auth`

| Field | Default | Description |
|---|---|---|
| `jwt` | `""` | Bearer token, signed via [`radio tokengen`](../cli/tokengen.md), authorizing this controller for the slug(s) it will register/queue against. Can also be supplied via the `GORADIO_JWT` environment variable, which **takes precedence** over this field |

## `station`

| Field | Default | Description |
|---|---|---|
| `slug` | `""` | Not currently read directly by the engine — your Lua script decides what slug to register (typically from `radio.args`, see [`radio station`](../cli/station.md)). This field is here for your own bookkeeping/tooling around the config file |

## `api` — local control API

Each station controller can optionally run its own small local HTTP API,
independent of the audio server's HTTP/gRPC surface.

| Field | Default | Description |
|---|---|---|
| `enabled` | `false` | Whether to start the local control API at all |
| `bind_host` | `127.0.0.1:8091` | Address it listens on |
| `api_key` | `""` | Required on every request, via `X-Api-Key: <key>` or `Authorization: Bearer <key>` |

This phase ships exactly one route:

| Route | Description |
|---|---|
| `GET /status` | Live `GetStatus` snapshot for this controller's registered station, as JSON |

Richer control endpoints (e.g. external "queue this now" triggers) are
planned but not built yet — this exists today mainly to prove the
config/auth wiring and give you a live health check.

## `logging`

| Field | Default | Description |
|---|---|---|
| `level` | `info` | One of `debug`, `info`, `warn`, `error` |
