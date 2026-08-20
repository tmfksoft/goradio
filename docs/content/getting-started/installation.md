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
git clone https://github.com/tmfksoft/goradio.git
cd goradio
make build
```

This produces `bin/radio`. Run `./bin/radio` with no arguments to see the
three subcommands (`serve`, `station`, `tokengen`).

## Other Makefile targets

| Target | What it does |
|---|---|
| `make build` | Builds `bin/radio` |
| `make test` | Runs `go test ./...` |
| `make vet` | Runs `go vet ./...` |
| `make fmt` | Lists any files `gofmt` would reformat |
| `make proto` | Lints and regenerates `gen/go/` from `proto/` |
| `make proto-push` | Publishes the `proto/` module to the Buf Schema Registry (requires `buf registry login`) |

Next: [Quickstart](quickstart.md).
