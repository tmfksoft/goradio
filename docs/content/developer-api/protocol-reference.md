# Protocol Reference

Service: `audioserver.v1.AudioServerService`, in package `audioserver.v1`.
Full source: [`proto/audioserver/v1`](https://github.com/goradioserver/goradio/tree/main/proto/audioserver/v1).

```proto
service AudioServerService {
  rpc RegisterStation(RegisterStationRequest) returns (RegisterStationResponse);
  rpc UnregisterStation(UnregisterStationRequest) returns (UnregisterStationResponse);
  rpc ListStations(ListStationsRequest) returns (ListStationsResponse);
  rpc QueueTrack(QueueTrackRequest) returns (QueueTrackResponse);
  rpc RemoveFromQueue(RemoveFromQueueRequest) returns (RemoveFromQueueResponse);
  rpc ClearQueue(ClearQueueRequest) returns (ClearQueueResponse);
  rpc Skip(SkipRequest) returns (SkipResponse);
  rpc SkipTo(SkipToRequest) returns (SkipToResponse);
  rpc Pause(PauseRequest) returns (PauseResponse);
  rpc Resume(ResumeRequest) returns (ResumeResponse);
  rpc Seek(SeekRequest) returns (SeekResponse);
  rpc SeekBy(SeekByRequest) returns (SeekByResponse);
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
  rpc SubscribeEvents(SubscribeEventsRequest) returns (stream StationEvent);
  rpc GetServerInfo(GetServerInfoRequest) returns (GetServerInfoResponse);
  rpc ListDirectory(ListDirectoryRequest) returns (ListDirectoryResponse);
}
```

Sixteen RPCs: fifteen unary commands, one server-streaming feed of
events. There is no bidirectional streaming — commands are always plain
request/response.

## Transports

The server speaks three protocols on the same port (`grpc.listen_addr`),
chosen per request from the headers — there's nothing to configure, and
all three reach the same handlers with the same auth:

- **gRPC** — what you get from a generated client in any language, and
  what the bundled Lua controller uses. Requires HTTP/2.
- **gRPC-Web** — for browser clients.
- **Connect** — plain HTTP `POST` with a JSON body, over HTTP/1.1 or
  HTTP/2. No gRPC library, no protobuf library, no codegen.

That last one is documented in full on
[HTTP + JSON API](http-json-api.md). Reach for it when gRPC is awkward or
impossible to depend on — legacy runtimes, embedded scripting engines,
32-bit processes — or when you just want to `curl` the server. The rest of
this page describes the RPCs themselves, which are identical whichever
transport you use.

## Authentication

Every RPC (including `SubscribeEvents`) requires an `Authorization: Bearer
<jwt>` header (gRPC request metadata is carried as HTTP headers, so this
is the same thing on every transport). The token is an HS256 JWT whose
claims include:

```json
{
  "sub": "whatever you passed as -subject",
  "iat": 1700000000,
  "exp": 1700086400,
  "slugs": ["myfm", "otherfm"],
  "dirs": ["GTASA/KROSE"],
  "read_only": false
}
```

The server verifies the signature against its configured `auth.jwt_secret`,
then checks that **the slug the specific call targets** is present in
`slugs` — a token scoped to `myfm` gets `PermissionDenied` on any call made
against `otherfm`, even if both stations exist on the same server.
`ListStations` is the one exception, since it doesn't target a single
slug: it never errors on scope, it just silently omits any registered
station not in `slugs` from the result.

Entries in `slugs` may be glob patterns instead of exact slugs (matched with
Go's `path/filepath.Match`): `"*"` authorizes every station on the server —
useful for a management dashboard that lists/controls all stations — and a
pattern like `"test-*"` authorizes any slug with that prefix. Note that `*`
does not cross a `/`, so a slug containing a slash needs its own explicit
pattern.

### Directory scope

`dirs` is a second, independent scope: which directories under `audio_root`
a `QueueTrack` local-file `location` (or a [`ListDirectory`](#listdirectory)
browse) may reference. **An entry grants recursive containment** — `"GTASA/KROSE"`
also authorizes `"GTASA/KROSE/song.ogg"` and anything deeper — unlike
`slugs`, which only ever matches a whole segment. Entries may also be
`path/filepath.Match` globs (e.g. `"GTASA/*"`).

**Omitting `dirs` entirely (or an empty array) means unrestricted** — the
only backward-compatible default, since it lets every token minted before
this field existed keep authorizing any local file, exactly as before.
Restricting `dirs` only matters once you deliberately start minting
narrower tokens — a natural fit for a shared `audio_root` where one
controller per directory (see
[goradio-gta](https://goradioserver.github.io/goradio-gta/), forty
controllers sharing one root) shouldn't be able to queue another
controller's files even though nothing in its own script ever tries to.

`QueueTrack` rejects an unauthorized local-file `location` with
`PermissionDenied`, checked synchronously before the item is even queued —
not left to fail silently during the async prefetch that resolves it. An
HTTP(S) source is never subject to `dirs` at all, since it doesn't touch
`audio_root`.

`read_only` (optional, default `false` — omit the field entirely for a
normal read-write token) additionally gates every **write** RPC —
`RegisterStation`, `UnregisterStation`, `QueueTrack`, `RemoveFromQueue`,
`ClearQueue`, `Skip`, `SkipTo`, `Pause`, `Resume`, `Seek`, `SeekBy` —
behind `read_only` being false; a read-only token gets `PermissionDenied`
on any of those, while `GetStatus`, `SubscribeEvents`, `ListDirectory`, and
the [now-playing HTTP endpoint](now-playing-http-api.md) remain available
regardless. Use this to hand out tokens for pure observers — a web embed,
a Discord bot, a dashboard — that should never be able to touch playback.

Mint tokens with [`radio tokengen`](../cli/tokengen.md) (`-readonly` for a
read-only one, `-dirs` for directory scope), or sign your own HS256 JWT
with this claim shape from any language.

The server itself listens in plaintext — put a TLS-terminating reverse
proxy in front if you need encryption in transit. See
[Known gaps](../index.md#known-gaps).

## RegisterStation

```proto
message RegisterStationRequest {
  string slug = 1;
  string name = 2;
  string description = 3;
  int32 low_queue_threshold = 4;
  string logo_url = 5;
  map<string, string> metadata = 6;
}

message RegisterStationResponse {
  string slug = 1;
  string stream_url = 2;
  bool re_registered = 3;
}
```

Registers a station. **Idempotent by slug**: if `slug` is already
registered, this just updates `name`/`description`/`logo_url`/
`metadata`/`low_queue_threshold` in place and returns
`re_registered = true` — it does **not** reset the queue or interrupt
playback. Call this on every (re)connect, not just once ever — see
[Writing a Controller](writing-a-controller.md#reconnecting).

`low_queue_threshold` (optional, default 0/disabled): if > 0, the server
fires `EVENT_TYPE_QUEUE_LOW` (edge-triggered, see
[SubscribeEvents](#subscribeevents)) whenever the pending queue length
drops to or below it, so you don't have to poll `GetStatus` to know when
to queue more.

`logo_url` (optional): a station logo/artwork URL, surfaced via
`GetStatus`/`ListStations`. Purely descriptive — never fetched or
validated by the audio server.

`metadata` (optional): freeform key/value data — a group name to cluster
stations in a dashboard, a genre tag, an operator-defined ID, whatever you
need. The audio server never interprets these keys itself; it just stores
and returns them (via `GetStatus`/`ListStations`) for the
controller/player/dashboard to use however it wants.

Every field on this request is fully replaced on re-registration, not
merged, so to update just the logo (or metadata) on the fly, re-send the
same `name`/`description` along with the new value — omitting a field on
a later call clears whatever was previously set for it, same as omitting
`name`/`description` would.

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

## ListStations

```proto
message ListStationsRequest {}

message StationSummary {
  string slug = 1;
  string name = 2;
  int64 listener_count = 3;
  string logo_url = 4;
  map<string, string> metadata = 5;
}

message ListStationsResponse {
  repeated StationSummary stations = 1;
}
```

Lists every currently registered station **the caller's token authorizes**
— not every station on the server — each with its live `listener_count`
(the same figure `GetStatus` reports). A token scoped to `"*"` sees
everything; one scoped to `myfm` sees only `myfm`, even if `otherfm` is
also registered. This is the call behind a management dashboard's station
list: one round trip instead of a `GetStatus` per known slug, and it works
even when the caller doesn't already know every slug in play.

Deliberately lighter than `GetStatus` — no queue, no history, no current
track — since it's meant to summarize many stations at once rather than
give a full picture of one. `metadata` (see `RegisterStation`) is included
though, since it's exactly the kind of thing a dashboard listing many
stations wants — e.g. a `group` key to cluster related stations together
in the list.

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
  string cover_art_url = 5;
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

- `display_title`, `display_artist`, and `cover_art_url` are all optional
  and purely descriptive — never fetched or validated by the audio server.
  They're carried through unchanged to `QueuedItemStatus`/
  `HistoryEntryStatus`/`TrackStartedPayload` wherever this `TrackSource`
  appears, for a controller/dashboard to render without needing its own
  metadata store.
- `TRACK_SOURCE_TYPE_LOCAL_FILE`'s `location` is resolved relative to the
  audio server's `audio.audio_root` config; it's rejected with
  `InvalidArgument` if it resolves outside that root (no `../` traversal),
  and with `PermissionDenied` if it falls outside the caller's token's
  [directory scope](#directory-scope) — both checked synchronously, before
  the item is queued, not left to fail silently during async prefetch.
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

## Pause / Resume

```proto
message PauseRequest {
  string slug = 1;
}

message PauseResponse {
  bool paused = 1;
}

message ResumeRequest {
  string slug = 1;
}

message ResumeResponse {
  bool resumed = 1;
}
```

`Pause` pauses the current track **in place**: the station falls back to
the silence loop, same as an empty queue, until `Resume`, which picks the
track back up from exactly where it was paused. This is a station-wide
pause, like everything else here — it affects the one shared broadcast
every listener is tuned into, not some per-listener state (there's no
per-listener playback in this protocol at all; see `SkipTo` for the same
point about positions).

Both return `false` rather than erroring when there's nothing sensible to
do: `paused` is `false` if nothing is playing, the current track is a live
relay (no fixed position to hold — see `QueueTrack`'s note on live
streams), or it was already paused; `resumed` is `false` if the station
wasn't paused. `Skip`/`SkipTo`/`ClearQueue` all still work as normal on a
paused station — they end the paused track (or replace it) rather than
being swallowed by the pause.

## Seek / SeekBy

```proto
message SeekRequest {
  string slug = 1;
  int64 position_seconds = 2;
}

message SeekResponse {
  bool seeked = 1;
  int64 position_seconds = 2;
}

message SeekByRequest {
  string slug = 1;
  int64 delta_seconds = 2;
}

message SeekByResponse {
  bool seeked = 1;
  int64 position_seconds = 2;
}
```

`Seek` jumps the current track to an absolute `position_seconds`; `SeekBy`
jumps by a signed `delta_seconds` from wherever it currently is (positive
= forward, negative = backward). Both clamp to `[0, duration_seconds]` and
return the resulting (clamped) position. This works because every cached
clip is transcoded to a fixed CBR format specifically so a time position
converts to an exact byte offset with no frame decoding needed — see
[Known gaps](../index.md#known-gaps) for what this doesn't cover (live
relays, below).

`seeked` is `false` (not an error) if nothing seekable is playing — no
current track, or it's a live relay (no fixed position to seek within, or
pause to hold — the only way to move past a live relay is `Skip`/`SkipTo`).
Works the same whether or not the station is currently paused: seeking
while paused just moves where `Resume` will pick up, without itself
resuming playback.

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
  bool is_paused = 11;   // true if current_track is paused (see Pause)
  string logo_url = 12;  // station logo/artwork URL, if set (see RegisterStation)
  map<string, string> metadata = 13;  // freeform key/value data, if set (see RegisterStation)
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
length when `duration_seconds` is `0`. `current_track_elapsed_seconds`
accounts for `Pause`/`Seek`/`SeekBy` correctly — it freezes while
`is_paused` and jumps on a seek, rather than just tracking wall-clock time
since the track started.

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

## GetServerInfo

```proto
message GetServerInfoRequest {}

message GetServerInfoResponse {
  string version = 1;
}
```

Reports the audio server's build version. Not scoped to any station —
like `ListStations`, it just needs a valid token, not one authorized for
a particular slug, since there's no per-station authorization decision to
make for a fact about the server itself.

`version` matches the git tag a release binary was built from (e.g.
`"v0.9.0"`), baked in at build time via `-ldflags`
(`scripts/package-release.sh`/`.github/workflows/release.yml`). A locally
built binary (`go build`/`make build` with no `-ldflags` override)
reports `"dev"`.

## ListDirectory

```proto
message DirectoryEntry {
  string name = 1;
  bool is_dir = 2;
  string path = 3;
  int64 size_bytes = 4;
}

message ListDirectoryRequest {
  string path = 1;
}

message ListDirectoryResponse {
  repeated DirectoryEntry entries = 1;
}
```

Lists one directory under `audio_root`. `path` is relative to `audio_root`
and defaults to the root when omitted; `entries[].path` is already in the
form a `QueueTrack` local-file `location` expects, so a file entry from a
listing can be queued as-is.

Not scoped to any station — like `ListStations`/`GetServerInfo`, it just
needs a valid token. What comes back is instead filtered by the token's
[directory scope](#directory-scope) (`dirs`, not `slugs`): an unrestricted
token sees the directory's full contents; a scoped one sees only entries
that are themselves authorized, or — for a subdirectory only — merely on
the way to one, so a client can still navigate down into an allowed
subdirectory without every parent folder along the way looking
unauthorized. Requesting a path that isn't reachable either way is
rejected with `PermissionDenied` rather than silently returning an empty
list, matching how a directly-named unauthorized slug is rejected
elsewhere rather than treated as empty.

A `path` that escapes `audio_root` (e.g. `"../"`) is rejected with
`InvalidArgument`, the same defense `QueueTrack`'s local-file resolution
already applies.
