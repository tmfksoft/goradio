# Installation

## Prerequisites

- **Go** — see `go.mod` for the minimum version.
- **ffmpeg** on `PATH` (or point `transcode.ffmpeg_path` at it) — GoRadio
  shells out to it to transcode every audio source into one fixed MP3
  format, which is what makes hard-cut playback safe.
- **buf** + `protoc-gen-go` + `protoc-gen-go-grpc` — only if you're changing
  the `.proto` files. Generated Go code is committed under `gen/go/`, so a
  normal build doesn't need any of this.

## Build

```sh
git clone https://github.com/goradioserver/goradio.git
cd goradio
make build
```

This produces `bin/radio`. Run `./bin/radio` with no arguments to see the
three subcommands (`serve`, `station`, `tokengen`).

## Or: pre-built binaries

If you don't want to build from source, tagged releases publish
`linux/amd64` and `windows/amd64` archives (binary + example configs + a
starter `station.lua`) to
[GitHub Releases](https://github.com/goradioserver/goradio/releases) — no Go
toolchain needed. Skip ahead to [Quickstart](quickstart.md) once you've
extracted one; it uses the same commands either way.

## Other Makefile targets

| Target | What it does |
|---|---|
| `make build` | Builds `bin/radio` |
| `make release [VERSION=v0.1.0]` | Cross-builds `linux/amd64` + `windows/amd64` and packages each with the example configs + starter Lua script into `dist/release/` — the same thing the release workflow publishes |
| `make test` | Runs `go test ./...` |
| `make vet` | Runs `go vet ./...` |
| `make fmt` | Lists any files `gofmt` would reformat |
| `make proto` | Lints and regenerates `gen/go/` from `proto/` |
| `make proto-push` | Publishes the `proto/` module to the Buf Schema Registry (requires `buf registry login`) |

Next: [Quickstart](quickstart.md).
