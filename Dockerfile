# One image, two roles: `radio serve` (the audio server) or
# `radio station` (a Lua station controller) -- pick one via the
# container's command/args at run time. See docs/content/deployment/docker.md.

FROM golang:1.27-alpine AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/radio ./cmd/radio

FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates && \
    addgroup -g 1000 -S radio && adduser -u 1000 -S radio -G radio

COPY --from=build /out/radio /usr/local/bin/radio
COPY configs/*.example.yaml /app/configs/

# audio.audio_root and transcode.cache_dir should point under here in your
# server.yaml -- mount a volume here for persistent transcode caching
# across restarts (not required, just avoids re-encoding after a restart).
RUN mkdir -p /data && chown radio:radio /data
VOLUME ["/data"]

WORKDIR /app
USER radio

# No CMD: running the image with no arguments prints usage. Provide the
# role and its flags via the container's command, e.g.:
#   docker run ghcr.io/tmfksoft/goradio serve --config /config/server.yaml
#   docker run ghcr.io/tmfksoft/goradio station --config /config/station.yaml --script /config/station.lua
ENTRYPOINT ["radio"]
