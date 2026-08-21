# Protocol Reference

Service: `audioserver.v1.AudioServerService`, in package `audioserver.v1`.
Full source: [`proto/audioserver/v1`](https://github.com/tmfksoft/goradio/tree/main/proto/audioserver/v1).

```proto
service AudioServerService {
  rpc RegisterStation(RegisterStationRequest) returns (RegisterStationResponse);
  rpc UnregisterStation(UnregisterStationRequest) returns (UnregisterStationResponse);
  rpc QueueTrack(QueueTrackRequest) returns (QueueTrackResponse);
  rpc RemoveFromQueue(RemoveFromQueueRequest) returns (RemoveFromQueueResponse);
  rpc ClearQueue(ClearQueueRequest) returns (ClearQueueResponse);
  rpc Skip(SkipRequest) returns (SkipResponse);
  rpc SkipTo(SkipToRequest) returns (SkipToResponse);
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
  rpc SubscribeEvents(SubscribeEventsRequest) returns (stream StationEvent);
}
```

Nine RPCs: eight unary commands, one server-streaming feed of events. There
is no bidirectional streaming — commands are always plain request/response.

## Authentication

Every RPC (including `SubscribeEvents`) requires an `authorization: Bearer
<jwt>` entry in the gRPC request metadata. The token is an HS256 JWT whose
claims include:

```json
{
  "sub": "whatever you passed as -subject",
  "iat": 1700000000,
  "exp": 1700086400,
  "slugs": ["myfm", "otherfm"],
  "read_only": false
}
```

The server verifies the signature against its configured `auth.jwt_secret`,
then checks that **the slug the specific call targets** is present in
`slugs` — a token scoped to `myfm` gets `PermissionDenied` on any call made
against `otherfm`, even if both stations exist on the same server.

Entries in `slugs` may be glob patterns instead of exact slugs (matched with
Go's `path/filepath.Match`): `"*"` authorizes every station on the server —
useful for a management dashboard that lists/controls all stations — and a
pattern like `"test-*"` authorizes any slug with that prefix. Note that `*`
does not cross a `/`, so a slug containing a slash needs its own explicit
pattern.

`read_only` (optional, default `false` — omit the field entirely for a
normal read-write token) additionally gates every **write** RPC —
`RegisterStation`, `UnregisterStation`, `QueueTrack`, `RemoveFromQueue`,
`ClearQueue`, `Skip`, `SkipTo` —
behind `read_only` being false; a read-only token gets `PermissionDenied`
on any of those, while `GetStatus`, `SubscribeEvents`, and the
[now-playing HTTP endpoint](now-playing-http-api.md) remain available
regardless. Use this to hand out tokens for pure observers — a web embed,
a Discord bot, a dashboard — that should never be able to touch playback.

Mint tokens with [`radio tokengen`](../cli/tokengen.md) (`-readonly` for a
read-only one), or sign your own HS256 JWT with this claim shape from any
language.

There is no TLS on the gRPC transport this phase — see
[Known gaps](../index.md#known-gaps).

## RegisterStation

```proto
message RegisterStationRequest {
  string slug = 1;
  string name = 2;
  string description = 3;
  int32 low_queue_threshold = 4;
}

message RegisterStationResponse {
  string slug = 1;
  string stream_url = 2;
  bool re_registered = 3;
}
```

Registers a station. **Idempotent by slug**: if `slug` is already
registered, this just updates `name`/`description`/`low_queue_threshold`
in place and returns `re_registered = true` — it does **not** reset the
queue or interrupt playback. Call this on every (re)connect, not just once
ever — see [Writing a Controller](writing-a-controller.md#reconnecting).

`low_queue_threshold` (optional, default 0/disabled): if > 0, the server
fires `EVENT_TYPE_QUEUE_LOW` (edge-triggered, see
[SubscribeEvents](#subscribeevents)) whenever the pending queue length
drops to or below it, so you don't have to poll `GetStatus` to know when
to queue more.

`stream_url` is the fully-qualified listener URL (built from the server's
`http.public_base_url` config), suitable for handing straight to a player.

Station registration is **in-memory only** on the audio server — it does
not survive a `radio serve` restart. See `UnregisterStation` below to
remove one explicitly before that.

## UnregisterStation

```proto
message UnregisterStationRequest {
  string slug = 1;
}

message UnregisterStationResponse {}
```

Removes a station from the registry: stops its player goroutine, and
disconnects every listener currently on `/stream/{slug}` and every open
`SubscribeEvents` stream for it. `NotFound` if `slug` isn't registered.
This does not persist anywhere — re-`RegisterStation`ing the same slug
afterward starts a fresh station with an empty queue, not a resumed one.

## QueueTrack

```proto
enum QueueMode {
  QUEUE_MODE_UNSPECIFIED = 0;
  QUEUE_MODE_APPEND = 1;
  QUEUE_MODE_PLAY_NEXT = 2;
  QUEUE_MODE_PLAY_NOW_INTERRUPT = 3;
}

enum Transition {
  TRANSITION_HARD_CUT = 0;      // default
  TRANSITION_CROSSFADE = 1;     // reserved, not implemented — coerced to HARD_CUT
}

enum TrackSourceType {
  TRACK_SOURCE_TYPE_UNSPECIFIED = 0;
  TRACK_SOURCE_TYPE_LOCAL_FILE = 1;
  TRACK_SOURCE_TYPE_HTTP_URL = 2;
}

message TrackSource {
  TrackSourceType type = 1;
  string location = 2;          // relative path (LOCAL_FILE) or http(s) URL (HTTP_URL)
  string display_title = 3;
  string display_artist = 4;
}

message QueueTrackRequest {
  string slug = 1;
  TrackSource source = 2;
  QueueMode mode = 3;
  Transition transition = 4;
}

message QueueTrackResponse {
  string queue_id = 1;
  int32 queue_position = 2;
  string status = 3;
}
```

- `TRACK_SOURCE_TYPE_LOCAL_FILE`'s `location` is resolved relative to the
  audio server's `audio.audio_root` config; it's rejected if it resolves
  outside that root (no `../` traversal).
- `TRACK_SOURCE_TYPE_HTTP_URL`'s `location` must be `http://` or `https://`.
  It's auto-detected as either a finite file or a continuous **live
  stream** from the response headers (no `Content-Length`, and/or
  `icy-`/`ice-` prefixed headers — the same signals an Icecast/Shoutcast
  mountpoint sends) — no separate source type or flag needed. A finite
  file is downloaded (capped at `fetch.max_download_bytes`) and cached as
  usual; a live stream is instead re-encoded continuously (to the same
  fixed target format as everything else) and relayed in real time, for
  as long as it stays current — see the note on `Skip` below. The audio
  server remembers the classification per URL for the life of the
  process, so repeat `QueueTrack` calls for the same URL don't re-probe it.
- `TRANSITION_CROSSFADE` is accepted by the schema for forward
  compatibility but not implemented — the server logs a warning and treats
  it as `TRANSITION_HARD_CUT`. Every source (including a live relay's
  re-encoded output) is transcoded to one fixed MP3 format specifically so
  hard-cut concatenation between clips has no gap or click.
- The response returns as soon as the item is accepted into the queue, not
  once it's confirmed playable — prefetch (download + transcode, or
  live-stream classification) starts immediately in the background, but
  failures surface later as an `EVENT_TYPE_ERROR` on `SubscribeEvents`,
  not as an error from this call. Queue well ahead of when you want
  something to play.

## RemoveFromQueue

```proto
message RemoveFromQueueRequest {
  string slug = 1;
  string queue_id = 2;
}

message RemoveFromQueueResponse {
  bool removed = 1;
}
```

Removes one still-pending item. `removed` is `false` — not an error — if
`queue_id` wasn't found (already played, already removed, or never
existed). Cannot remove whatever is currently playing, since it's already
left the queue by the time it's playing; use `QueueTrack` with
`QUEUE_MODE_PLAY_NOW_INTERRUPT` to cut the current track short instead.

## ClearQueue

```proto
message ClearQueueRequest {
  string slug = 1;
  bool stop_current = 2;
}

message ClearQueueResponse {
  int32 removed_count = 1;
  bool stopped_current = 2;
}
```

Removes every pending item. By default (`stop_current` false), does not
touch whatever is currently playing — like `RemoveFromQueue`. Set
`stop_current` to also interrupt the current track; since the queue was
just cleared there's nothing to replace it with, so playback falls back to
silence rather than jumping to another track. `stopped_current` in the
response reports whether there was actually something playing to
interrupt. This is the typical "clean restart" call: a controller that
just (re)started often wants to guarantee nothing stale from before is
still playing or queued.

## Skip

```proto
message SkipRequest {
  string slug = 1;
}

message SkipResponse {
  bool skipped = 1;
}
```

Interrupts whatever is currently playing, leaving the rest of the queue
untouched — playback immediately moves on to the next pending item (or
falls back to silence). `skipped` is `false` if there was nothing playing.
This is the only way to end a "track" with no natural end — most notably
a queued live stream (see `QueueTrack` above), which otherwise plays
indefinitely: it's not a distinct concept at the protocol level, just a
`TrackSource` whose relay keeps running until `Skip`,
`QUEUE_MODE_PLAY_NOW_INTERRUPT`, or the upstream connection itself drops.

## SkipTo

```proto
message SkipToRequest {
  string slug = 1;
  string queue_id = 2;
}

message SkipToResponse {
  int32 removed_count = 1;
  bool interrupted_current = 2;
}
```

Jumps playback straight to a specific pending item, by `queue_id` (not
position — a dashboard's view of positions can be stale by the time the
call lands, since anything can finish or get queued in between; `queue_id`
doesn't have that race). Every item ahead of it in the queue is dropped
(`removed_count`), and whatever's currently playing is interrupted
(`interrupted_current`) so the target item starts immediately instead of
waiting for the current one to finish. `NotFound` if `queue_id` isn't a
pending item — in particular, you can't `SkipTo` whatever's already
playing, since it's left the queue by then; use plain `Skip` to interrupt
the current track without changing what plays after it.

## GetStatus

```proto
message GetStatusRequest {
  string slug = 1;
}

message QueuedItemStatus {
  string queue_id = 1;
  TrackSource source = 2;
  QueueMode mode = 3;
  int64 duration_seconds = 4;   // 0 = unknown/indefinite (a live relay, or not-yet-ready)
}

message HistoryEntryStatus {
  string queue_id = 1;
  TrackSource source = 2;
  QueueMode mode = 3;
  int64 duration_seconds = 4;
  string reason = 5;              // "completed" or "interrupted"
  int64 ended_at_unix_ms = 6;
}

message GetStatusResponse {
  string slug = 1;
  string name = 2;
  bool is_registered = 3;
  QueuedItemStatus current_track = 4;   // unset while playing silence
  bool is_silence = 5;
  repeated QueuedItemStatus queue = 6;
  int64 listener_count = 7;
  int64 uptime_seconds = 8;
  int64 current_track_elapsed_seconds = 9;   // only meaningful when current_track is set
  repeated HistoryEntryStatus history = 10;
}
```

An on-demand snapshot. If `slug` isn't registered, `is_registered` is
`false` and every other field is zero-valued (this is not an RPC error).
`queue` carries the actual pending items in play order (not just a count),
each with its `queue_id` — useful for building your own queue inspection
or de-duplication logic without tracking every `QueueTrack` response
yourself.

`history` carries the most recently finished items, oldest first, capped at
a small fixed count (currently 20) — enough to seed a "recently played"
view. This is meant for a **one-shot fetch on load, not polling**: build
your initial state from one `GetStatus` call, then keep both `queue` and
`history` current from there by watching `SubscribeEvents`
(`QUEUE_UPDATED` for the queue, `TRACK_STARTED`/`TRACK_ENDED` to move the
current track into your own history list) rather than re-calling
`GetStatus` on a timer.

`duration_seconds` is `0` for a live relay (no fixed length) and also
briefly `0` for a pending item whose prefetch hasn't finished yet — it's
computed from the cached, fixed-CBR file's size once transcoding
completes, not probed with a separate tool. `current_track_elapsed_seconds`
combined with `current_track.duration_seconds` is enough to render a
progress bar; render it as an indefinite/pulsing bar instead of a fixed
length when `duration_seconds` is `0`.

## SubscribeEvents

```proto
message SubscribeEventsRequest {
  string slug = 1;
}

enum EventType {
  EVENT_TYPE_UNSPECIFIED = 0;
  EVENT_TYPE_TRACK_STARTED = 1;
  EVENT_TYPE_TRACK_ENDED = 2;
  EVENT_TYPE_QUEUE_UPDATED = 3;
  EVENT_TYPE_LISTENER_COUNT_CHANGED = 4;
  EVENT_TYPE_ERROR = 5;
  EVENT_TYPE_SILENCE_STARTED = 6;
  EVENT_TYPE_SILENCE_ENDED = 7;
  EVENT_TYPE_QUEUE_LOW = 8;
}

message StationEvent {
  string slug = 1;
  EventType type = 2;
  int64 timestamp_unix_ms = 3;
  oneof payload {
    TrackStartedPayload track_started = 10;
    TrackEndedPayload track_ended = 11;
    QueueUpdatedPayload queue_updated = 12;
    ListenerCountChangedPayload listener_count_changed = 13;
    ErrorPayload error = 14;
  }
}
```

| Event | Payload | Fired when |
|---|---|---|
| `TRACK_STARTED` | `TrackStartedPayload{queue_id, source, duration_seconds}` | A queued item starts playing |
| `TRACK_ENDED` | `TrackEndedPayload{queue_id, reason}` | A track finishes — `reason` is `"completed"` or `"interrupted"` |
| `QUEUE_UPDATED` | `QueueUpdatedPayload{queue_length}` | The queue length changes (an item was queued or consumed) |
| `LISTENER_COUNT_CHANGED` | `ListenerCountChangedPayload{listener_count}` | An HTTP listener connects, disconnects, or is evicted |
| `ERROR` | `ErrorPayload{message, code}` | A queued item failed to resolve/transcode and was skipped |
| `SILENCE_STARTED` | *(none)* | The queue drained and playback fell back to the silence loop |
| `SILENCE_ENDED` | *(none)* | Playback resumed with a real track after a silence period |
| `QUEUE_LOW` | `QueueLowPayload{queue_length, threshold}` | The pending queue length dropped to or below `RegisterStationRequest.low_queue_threshold` (edge-triggered — fires once per dip, not repeatedly while it stays low; only if that threshold was set > 0) |

This is a server-streaming RPC: it stays open and pushes events as they
happen until you cancel the context or the server closes it (e.g. on
shutdown). If it closes unexpectedly, reconnect — see
[Writing a Controller](writing-a-controller.md#reconnecting).
