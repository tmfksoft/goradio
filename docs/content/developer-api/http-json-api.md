# HTTP + JSON API (no gRPC library)

Every RPC in the [Protocol Reference](protocol-reference.md) can be called
over plain HTTP with a JSON body — no gRPC library, no protobuf library,
no code generation, and **no HTTP/2**. If your platform can make an
HTTP/1.1 request and parse JSON, it can drive the audio server.

This exists because gRPC is a hard dependency to satisfy on some
platforms: old runtimes, embedded scripting engines, game-server plugin
APIs, and 32-bit processes that can't load the available gRPC binaries.
None of that matters here — this is just POST and JSON.

It's the same server, the same port, and the same auth as gRPC. The audio
server speaks three protocols on `grpc.listen_addr` simultaneously
(gRPC, gRPC-Web, and the Connect protocol documented on this page), and
picks per request based on the headers. Nothing needs enabling.

## Calling a unary RPC

```
POST /audioserver.v1.AudioServerService/<RpcName> HTTP/1.1
Content-Type: application/json
Authorization: Bearer <jwt>

<request message as JSON>
```

The path is always `/audioserver.v1.AudioServerService/` plus the RPC name
exactly as it appears in the `.proto`. The method is always `POST`, even
for reads. A worked example:

```sh
curl -X POST http://localhost:9090/audioserver.v1.AudioServerService/GetStatus \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"slug":"myfm"}'
```

```json
{"slug":"myfm","name":"My FM","isRegistered":true,"isSilence":false,"uptimeSeconds":"412"}
```

Queueing a track is the same shape:

```sh
curl -X POST http://localhost:9090/audioserver.v1.AudioServerService/QueueTrack \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"slug":"myfm","source":{"type":"TRACK_SOURCE_TYPE_LOCAL_FILE","location":"jingle.mp3"},"mode":"QUEUE_MODE_APPEND"}'
```

```json
{"queueId":"7bd96ff0-da51-4f34-943f-e9046b94b9cb","status":"queued"}
```

## Four things that will trip up a hand-written client

These are all consequences of how protobuf maps to JSON. None of them are
optional or configurable, so build for them up front.

**1. 64-bit integers are JSON *strings*, not numbers.** This catches
everyone. `uptime_seconds`, `duration_seconds`, `listener_count`,
`timestamp_unix_ms` and every other `int64` field arrive quoted:

```json
{"uptimeSeconds": "412", "durationSeconds": "5"}
```

not `412`. Your parser will hand you a string — convert explicitly. The
server accepts both forms on input. `int32` fields (`queue_position`,
`low_queue_threshold`) are ordinary JSON numbers; only the 64-bit ones are
strings.

This is a good thing for a 32-bit client: the `*_unix_ms` timestamps
exceed 2³¹, so parsing them as a native 32-bit int would overflow. Parse
them as a double (exact well past millisecond timestamps) or a real 64-bit
type.

**2. Field names are `lowerCamelCase` on the wire.** The `.proto` says
`listener_count`, the JSON says `listenerCount`. The server accepts
*either* spelling in a request body, but it always *emits* camelCase, so
read from the camelCase key.

**3. Fields at their default value are omitted entirely.** A station with
no listeners has no `listenerCount` key at all, not `"listenerCount": "0"`.
Same for `false` booleans and empty strings/lists. Treat a missing key as
the zero value rather than as an error — don't require it to be present.

**4. Enums are strings, spelled out in full.** `"mode": "QUEUE_MODE_APPEND"`,
not `1` and not `"APPEND"`. The full set of values for each enum is in the
[Protocol Reference](protocol-reference.md).

## Errors

An error from an RPC is a non-200 response whose body is JSON, regardless
of what `Content-Type` you asked for:

```json
{"code": "permission_denied", "message": "token is read-only"}
```

The HTTP status is derived from `code`, so you can branch on either:

| `code` | HTTP | When |
|---|---|---|
| `invalid_argument` | 400 | Missing required field (e.g. an empty `slug`), or a malformed JSON body |
| `unauthenticated` | 401 | Missing/malformed/expired token |
| `permission_denied` | 403 | Token doesn't cover this slug, or is read-only and this is a write RPC |
| `not_found` | 404 | No such station slug, or no such queue item |

Two failures happen *before* the RPC is reached and so come back as plain
text, not as a JSON error object — worth recognising while getting a new
client working:

- **404 with a `404 page not found` body** — the RPC name in your path is
  wrong (it's case-sensitive, and must match the `.proto` exactly). A
  genuine `not_found` RPC error is also a 404, but has a JSON body; the
  body is how you tell them apart.
- **405 with an empty body** — you used a method other than `POST`.

The auth rules are identical to gRPC — same tokens, same slug scoping,
same read-only gating. See
[Authentication](protocol-reference.md#authentication).

## Streaming: SubscribeEvents

`SubscribeEvents` works over HTTP/1.1 too, as a chunked response — you do
**not** need HTTP/2 for it. It's more work than a unary call, because both
the request and each response message are wrapped in a 5-byte envelope:

```
[1 byte flags][4 bytes length, big-endian][payload]
```

Send the request with `Content-Type: application/connect+json` and a
`Connect-Protocol-Version: 1` header, with the request message enveloped
the same way (flags `0`). Then read envelopes off the response as they
arrive:

- **flags `0`** — a `StationEvent`; the payload is the event as JSON.
- **flags `2`** — end-of-stream. The payload is `{}` on a clean close, or
  `{"error":{"code":"...","message":"..."}}` if the stream is ending
  because of an error. No more messages follow.

A minimal reader, in Python for legibility — the logic ports to anything
with a socket and a byte-order swap:

```python
import struct, json, requests

body = json.dumps({"slug": "myfm"}).encode()
envelope = struct.pack(">BI", 0, len(body)) + body

resp = requests.post(
    "http://localhost:9090/audioserver.v1.AudioServerService/SubscribeEvents",
    data=envelope,
    headers={
        "Content-Type": "application/connect+json",
        "Connect-Protocol-Version": "1",
        "Authorization": f"Bearer {TOKEN}",
    },
    stream=True,
)

buf = b""
for chunk in resp.iter_content(4096):
    buf += chunk
    while len(buf) >= 5:
        flags, length = struct.unpack(">BI", buf[:5])
        if len(buf) < 5 + length:
            break
        payload, buf = buf[5:5 + length], buf[5 + length:]
        if flags & 2:
            print("stream ended:", payload.decode())
        else:
            print("event:", json.loads(payload))
```

Sample events, one per envelope:

```json
{"slug":"myfm","type":"EVENT_TYPE_TRACK_STARTED","timestampUnixMs":"1787586747047","trackStarted":{"queueId":"15c5...","source":{"type":"TRACK_SOURCE_TYPE_LOCAL_FILE","location":"jingle.mp3"}}}
{"slug":"myfm","type":"EVENT_TYPE_SILENCE_STARTED","timestampUnixMs":"1787586752117"}
```

If the envelope parsing is more than you want to implement, the
alternative is to poll `GetStatus` on a timer — less immediate, but it's
an ordinary unary call. The
[now-playing HTTP endpoint](now-playing-http-api.md) is another option for
read-only "what's playing" use cases; it's a plain unauthenticated GET
with no envelopes and no token.

## Reconnecting

The reconnect guidance in
[Writing a Controller](writing-a-controller.md#reconnecting) applies here
unchanged: re-send `RegisterStation` on every reconnect (it's idempotent
by slug), then re-subscribe. A dropped HTTP connection is exactly as
expected as a dropped gRPC stream — assume it will happen and loop with
backoff.
