# Protocol Reference

Service: `audioserver.v1.AudioServerService`, in package `audioserver.v1`.
Full source: [`proto/audioserver/v1`](https://github.com/tmfksoft/goradio/tree/main/proto/audioserver/v1).

```proto
service AudioServerService {
  rpc RegisterStation(RegisterStationRequest) returns (RegisterStationResponse);
  rpc QueueTrack(QueueTrackRequest) returns (QueueTrackResponse);
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
  rpc SubscribeEvents(SubscribeEventsRequest) returns (stream StationEvent);
}
```

Four RPCs: three unary commands, one server-streaming feed of events. There
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
  "slugs": ["myfm", "otherfm"]
}
```

The server verifies the signature against its configured `auth.jwt_secret`,
then checks that **the slug the specific call targets** is present in
`slugs` — a token scoped to `myfm` gets `PermissionDenied` on any call
(`RegisterStation`, `QueueTrack`, `GetStatus`, `SubscribeEvents`) made
against `otherfm`, even if both stations exist on the same server. Mint
tokens with [`radio tokengen`](../cli/tokengen.md), or sign your own
HS256 JWT with this claim shape from any language.

There is no TLS on the gRPC transport this phase — see
[Known gaps](../index.md#known-gaps).

## RegisterStation

```proto
message RegisterStationRequest {
  string slug = 1;
  string name = 2;
  string description = 3;
}

message RegisterStationResponse {
  string slug = 1;
  string stream_url = 2;
  bool re_registered = 3;
}
```

Registers a station. **Idempotent by slug**: if `slug` is already
registered, this just updates `name`/`description` in place and returns
`re_registered = true` — it does **not** reset the queue or interrupt
playback. Call this on every (re)connect, not just once ever — see
[Writing a Controller](writing-a-controller.md#reconnecting).

`stream_url` is the fully-qualified listener URL (built from the server's
`http.public_base_url` config), suitable for handing straight to a player.

Station registration is **in-memory only** on the audio server — it does
not survive a `radio serve` restart. There's no `DeregisterStation` RPC.

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
- `TRACK_SOURCE_TYPE_HTTP_URL`'s `location` must be `http://` or `https://`;
  the download is capped at `fetch.max_download_bytes`.
- `TRANSITION_CROSSFADE` is accepted by the schema for forward
  compatibility but not implemented — the server logs a warning and treats
  it as `TRANSITION_HARD_CUT`. Every source is transcoded to one fixed MP3
  format specifically so hard-cut concatenation between clips has no gap or
  click.
- The response returns as soon as the item is accepted into the queue, not
  once it's confirmed playable — prefetch (download + transcode) starts
  immediately in the background, but failures surface later as an
  `EVENT_TYPE_ERROR` on `SubscribeEvents`, not as an error from this call.
  Queue well ahead of when you want something to play.

## GetStatus

```proto
message GetStatusRequest {
  string slug = 1;
}

message QueuedItemStatus {
  string queue_id = 1;
  TrackSource source = 2;
  QueueMode mode = 3;
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
}
```

An on-demand snapshot. If `slug` isn't registered, `is_registered` is
`false` and every other field is zero-valued (this is not an RPC error).

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
| `TRACK_STARTED` | `TrackStartedPayload{queue_id, source}` | A queued item starts playing |
| `TRACK_ENDED` | `TrackEndedPayload{queue_id, reason}` | A track finishes — `reason` is `"completed"` or `"interrupted"` |
| `QUEUE_UPDATED` | `QueueUpdatedPayload{queue_length}` | The queue length changes (an item was queued or consumed) |
| `LISTENER_COUNT_CHANGED` | `ListenerCountChangedPayload{listener_count}` | An HTTP listener connects, disconnects, or is evicted |
| `ERROR` | `ErrorPayload{message, code}` | A queued item failed to resolve/transcode and was skipped |
| `SILENCE_STARTED` | *(none)* | The queue drained and playback fell back to the silence loop |
| `SILENCE_ENDED` | *(none)* | Playback resumed with a real track after a silence period |

This is a server-streaming RPC: it stays open and pushes events as they
happen until you cancel the context or the server closes it (e.g. on
shutdown). If it closes unexpectedly, reconnect — see
[Writing a Controller](writing-a-controller.md#reconnecting).
