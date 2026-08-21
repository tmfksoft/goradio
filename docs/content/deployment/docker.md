# Docker & Kubernetes

One image runs either role — `radio serve` (the audio server) or
`radio station` (a Lua controller) — chosen at container-run time via the
command/args. It's built from `Dockerfile` and published by
`.github/workflows/docker.yml` to:

```
ghcr.io/tmfksoft/goradio:vX.Y.Z
ghcr.io/tmfksoft/goradio:latest
```

Bundles `ffmpeg` (Alpine's build, confirmed built with `--enable-libmp3lame`)
so transcoding and live-relay re-encoding work out of the box. Runs as a
fixed non-root user, **uid 1000 / gid 1000** — see [Volume permissions](#volume-permissions)
below, this is the one thing that trips people up.

## Docker: the audio server

```sh
mkdir -p ./data ./audio
cp configs/server.example.yaml ./server.yaml   # edit auth.jwt_secret; point audio_root at /audio, cache_dir at /data/...

docker run -d --name goradio-serve \
  -p 9090:9090 -p 8080:8080 \
  -v "$PWD/server.yaml:/app/server.yaml:ro" \
  -v "$PWD/audio:/audio:ro" \
  -v "$PWD/data:/data" \
  ghcr.io/tmfksoft/goradio:latest \
  serve --config /app/server.yaml
```

`/data` is a declared `VOLUME` — mount it if you want the transcode cache
to survive a container restart (not required; it just avoids re-encoding
your library again on next boot).

## Docker: a station controller

```sh
cp configs/station.example.yaml ./station.yaml   # paste in a token from `radio tokengen`
cp testdata/station-scripts/example.lua ./station.lua

docker run -d --name goradio-station \
  -v "$PWD/station.yaml:/app/station.yaml:ro" \
  -v "$PWD/station.lua:/app/station.lua:ro" \
  ghcr.io/tmfksoft/goradio:latest \
  station --config /app/station.yaml --script /app/station.lua
```

Add trailing args after `--script` the same way you would outside Docker
to drive [one shared script as multiple stations](../cli/station.md).

## Volume permissions

The image's `radio` user is a fixed `uid 1000 / gid 1000` (not
auto-detected from the host) so it's predictable enough to document and
script against. A bind-mounted host directory keeps its host-side
ownership inside the container, so if your host user isn't uid 1000,
either:

```sh
sudo chown -R 1000:1000 ./data ./audio
```

or override the container's user to match yours instead:

```sh
docker run --user "$(id -u):$(id -g)" ...
```

(only needed for directories the process writes to — `/data` and
anywhere `audio_root` points; a read-only audio library mount works
regardless of ownership).

## Kubernetes

A minimal audio server `Deployment` + `Service`, with `server.yaml` from
a `ConfigMap`, the JWT secret from a `Secret`, and a `PersistentVolumeClaim`
for `/data`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: goradio-server-config
data:
  server.yaml: |
    grpc:
      listen_addr: "0.0.0.0:9090"
    http:
      listen_addr: "0.0.0.0:8080"
      public_base_url: "https://radio.example.com"
    audio:
      audio_root: "/audio"
    transcode:
      cache_dir: "/data/transcode-cache"
---
apiVersion: v1
kind: Secret
metadata:
  name: goradio-secrets
stringData:
  jwt-secret: "change-me"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: goradio-serve
spec:
  replicas: 1   # audio server state is in-memory/ephemeral -- see Known gaps; don't scale this beyond 1
  selector:
    matchLabels: {app: goradio-serve}
  template:
    metadata:
      labels: {app: goradio-serve}
    spec:
      securityContext:
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000   # so the mounted PVC is writable by uid 1000
      containers:
        - name: goradio
          image: ghcr.io/tmfksoft/goradio:latest
          args: ["serve", "--config", "/app/server.yaml"]
          env:
            - name: GORADIO_JWT_SECRET
              valueFrom: {secretKeyRef: {name: goradio-secrets, key: jwt-secret}}
          ports:
            - {containerPort: 9090, name: grpc}
            - {containerPort: 8080, name: http}
          readinessProbe:
            httpGet: {path: /healthz, port: 8080}
          livenessProbe:
            httpGet: {path: /healthz, port: 8080}
          volumeMounts:
            - {name: config, mountPath: /app/server.yaml, subPath: server.yaml, readOnly: true}
            - {name: data, mountPath: /data}
            # - {name: audio-library, mountPath: /audio, readOnly: true}
      volumes:
        - {name: config, configMap: {name: goradio-server-config}}
        - {name: data, persistentVolumeClaim: {claimName: goradio-data}}
---
apiVersion: v1
kind: Service
metadata:
  name: goradio-serve
spec:
  selector: {app: goradio-serve}
  ports:
    - {name: grpc, port: 9090, targetPort: grpc}
    - {name: http, port: 8080, targetPort: http}
```

A station controller is the same shape without the `Service`/ports (unless
its own `api.enabled` control API is on) or the `data` volume, pointing
`--config`/`--script` at a `ConfigMap`-mounted `station.yaml`/`station.lua`,
and its `grpc_addr` at the `goradio-serve` Service above
(`goradio-serve:9090` inside the cluster).

Don't run more than one replica of `radio serve` for the same
stations/data — registration and the transcode cache are per-process, in-
memory or local-disk state (see [Known gaps](../index.md#known-gaps)),
they're not shared or coordinated across replicas. Station controllers
(`radio station`), on the other hand, are fine to scale: nothing stops
multiple controller processes from registering and acting on the same
station slug (each with its own valid token), as long as they coordinate
with each other so they're not stepping on each other's decisions — the
audio server itself doesn't referee that.
