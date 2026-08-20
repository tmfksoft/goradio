# Writing a Controller

This walks through building a station controller from scratch, in any
language, against the gRPC protocol described in the
[Protocol Reference](protocol-reference.md).

## 1. Generate a client from the schema registry

You don't need this repository at all — the schema is published to a Buf
Schema Registry at `proto.prod.wtf/tmfksoft/goradio`.

1. Write a `buf.gen.yaml` naming the plugins for your language. For example, Python:
   ```yaml
   version: v2
   plugins:
     - remote: buf.build/protocolbuffers/python
       out: gen/python
     - remote: buf.build/grpc/python
       out: gen/python
   ```
   Swap in whatever local or remote plugins your language/toolchain uses —
   this works the same way for Node/TS, Java/Kotlin, C#, Rust, Swift, PHP,
   and anything else `buf` has plugins for.
2. Generate directly from the registry:
   ```sh
   buf generate proto.prod.wtf/tmfksoft/goradio
   ```

That's it — you now have a generated `AudioServerService` client stub for
your language, kept in sync by re-running that command whenever the schema
changes.

## 2. The call order

This is the part that matters most: the RPCs aren't independent — there's
an expected lifecycle.

```mermaid
sequenceDiagram
    participant C as Your Controller
    participant A as radio serve

    Note over C,A: 1. Connect
    C->>A: dial gRPC (plaintext)

    Note over C,A: 2. Register
    C->>A: RegisterStation(slug, name, description)
    A-->>C: stream_url, re_registered

    Note over C,A: 3. Subscribe (long-lived)
    C->>A: SubscribeEvents(slug)
    activate A
    Note right of A: stream stays open

    Note over C,A: 4. Queue whenever you have something to play
    C->>A: QueueTrack(source, mode)
    A-->>C: queue_id, queue_position
    A-->>C: event: QUEUE_UPDATED
    A-->>C: event: TRACK_STARTED
    A-->>C: event: TRACK_ENDED

    Note over C,A: 5. Poll on demand (optional)
    C->>A: GetStatus(slug)
    A-->>C: snapshot

    deactivate A
```

1. **Connect** — dial the audio server's gRPC address. Attach your JWT as
   `authorization: Bearer <token>` metadata on every call (most gRPC
   client libraries let you do this once via per-call/per-RPC credentials
   rather than manually on each call — see your language's gRPC docs for
   "call credentials" or "per-RPC credentials").
2. **Register** — call `RegisterStation` with your station's slug, name,
   and description. Do this before anything else; every other RPC targets
   a slug that must already be registered (`QueueTrack`/`SubscribeEvents`
   against an unregistered slug return `NotFound`).
3. **Subscribe** — open `SubscribeEvents` for your slug and keep it open
   for the life of your process. This is how you find out what's actually
   happening (track started/ended, errors, listener count) — `QueueTrack`'s
   response only confirms the item was *accepted*, not that it played.
4. **Queue** — call `QueueTrack` whenever your logic decides something
   should play. Queue ahead of when you need it to start (see
   [Protocol Reference — QueueTrack](protocol-reference.md#queuetrack)) —
   the server prefetches in the background starting the moment you call
   this, not when the item is about to play.
5. **Poll status when useful** — `GetStatus` for an on-demand snapshot
   (e.g. to show a dashboard, or to sanity-check state on startup before
   you've received any events yet). You don't need to poll it to drive
   playback — the event stream is the real-time source of truth.

## Reconnecting

Station registration is **in-memory only** on the audio server — it's
wiped by a `radio serve` restart. Your controller should be able to run
indefinitely across audio-server restarts and network blips without manual
intervention:

- If `RegisterStation` fails (audio server not reachable / not up yet),
  retry with backoff. This call is safe to retry — it's idempotent by slug.
- If the `SubscribeEvents` stream errors or closes unexpectedly, assume the
  audio server may have restarted (so your registration may be gone) and:
  1. Call `RegisterStation` again with the same slug/name/description.
  2. Re-open `SubscribeEvents`.
  3. Repeat with backoff if either fails.

This is exactly what `internal/luastation` (this repo's bundled Lua engine)
does — see [`internal/luastation/engine.go`](https://github.com/tmfksoft/goradio/blob/main/internal/luastation/engine.go)
if you want a concrete reference implementation of this reconnect loop to
port to your language.

## Things your controller does *not* need to worry about

- **Transcoding** — you hand the server a path or URL; it transcodes to
  the fixed target format and caches the result. You never touch encoded
  audio bytes.
- **Pacing/streaming to listeners** — entirely the audio server's job.
- **Silence fallback** — the server plays a looping silence clip whenever
  your queue is empty; you don't need to queue "nothing" explicitly.
- **Hard-cut correctness** — guaranteed by the server's uniform transcode
  format; you just pick `QUEUE_MODE_APPEND`/`PLAY_NEXT`/`PLAY_NOW_INTERRUPT`.

Everything your controller *is* responsible for is deciding **what** to
queue and **when** — that's the whole point of writing your own.
