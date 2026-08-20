# Registering & Queueing

## `radio.register(slug, name, description)`

Registers (or re-registers) a station on the audio server. Call this once,
typically at the top of your script.

```lua
local info = radio.register("myfm", "My FM", "A station about nothing in particular")
print(info.slug)           --> "myfm"
print(info.stream_url)     --> "http://localhost:8080/stream/myfm"
print(info.re_registered)  --> false (true if this slug was already registered)
```

`name` and `description` are optional — omitted, `name` defaults to the
slug and `description` to an empty string.

Registration is **idempotent by slug**: calling it again for a slug that's
already registered just updates its display metadata in place, without
disrupting whatever is currently queued or playing. This is what makes
reconnects safe — and you don't need to think about reconnects at all,
because the engine calls this for you automatically (with exponential
backoff) if the connection to the audio server drops or the server itself
restarts.

If the very first `radio.register` call fails (e.g. the audio server isn't
up yet), it blocks and retries internally rather than raising a Lua error —
your script's top-level code will simply wait until it succeeds.

## `radio.queue(source, mode)`

Queues something to play.

```lua
radio.queue("jingle.mp3", "APPEND")

radio.queue({
  type = "url",
  location = "https://example.com/track.mp3",
  title = "Track Title",
  artist = "Some Artist",
}, "PLAY_NEXT")
```

**`source`** is either:

- a plain string — a path relative to the audio server's `audio.audio_root`,
  or an `http://`/`https://` URL (detected automatically by its prefix), or
- a table: `{type = "local"|"url", location = ..., title = ..., artist = ...}`.
  `title`/`artist` are optional metadata carried through to
  `radio.on_track_started` and future ICY metadata support; `type` is
  optional too and inferred from `location`'s prefix if omitted.

**`mode`** (optional, default `"APPEND"`) is one of:

| Mode | Behavior |
|---|---|
| `"APPEND"` | Add to the end of the queue |
| `"PLAY_NEXT"` | Add to the front of the queue — plays right after whatever's currently playing finishes |
| `"PLAY_NOW_INTERRUPT"` | Add to the front of the queue **and** cut the currently playing clip immediately |

Returns a table:

```lua
local result = radio.queue("jingle.mp3", "APPEND")
print(result.queue_id)        -- server-assigned id, useful for correlating with events
print(result.queue_position)  -- 0 = currently playing/about to play immediately
print(result.status)          -- informational only ("queued") — see note below
```

!!! note "This call returns before playback is confirmed"
    `radio.queue` returns as soon as the audio server has accepted the item
    into its queue — the source hasn't necessarily been downloaded or
    transcoded yet (that happens in the background, starting immediately).
    If resolving or transcoding the source fails, you won't see it as an
    error from this call; you'll see it later as an `ERROR` event via
    [`radio.on_error`](events-and-scheduling.md#radioon_errorfn), and the
    item is skipped. Queue early — the audio server prefetches as soon as
    you call `radio.queue`, not when the item is about to play, precisely
    so it has time to be ready.

## `radio.status()`

An on-demand snapshot of the registered station's current state — a
synchronous `GetStatus` call.

```lua
local status = radio.status()
print(status.is_silence)      -- true if playing the fallback silence loop
print(status.listener_count)
print(status.queue_length)
print(status.uptime_seconds)
```

Use this for polling; use [`radio.on_track_started`/`radio.on_track_ended`](events-and-scheduling.md)
for reacting to changes in real time without polling.
