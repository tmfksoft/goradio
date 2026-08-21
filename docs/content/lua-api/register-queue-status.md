# Registering & Queueing

## `radio.register(slug, name, description [, options])`

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

`options` is an optional table; the only field it currently recognizes is
`low_queue_threshold`, which enables the [`radio.on_queue_low`](events-and-scheduling.md#radioon_queue_lowfn)
event:

```lua
radio.register("myfm", "My FM", "", {low_queue_threshold = 3})
```

Registration is **idempotent by slug**: calling it again for a slug that's
already registered just updates its display metadata in place, without
disrupting whatever is currently queued or playing. This is what makes
reconnects safe — and you don't need to think about reconnects at all,
because the engine calls this for you automatically (with exponential
backoff) if the connection to the audio server drops or the server itself
restarts.

If the very first `radio.register` call fails for a **transient** reason
(e.g. the audio server isn't up yet), it blocks and retries internally with
backoff rather than raising a Lua error — your script's top-level code
simply waits until it succeeds, and `Ctrl+C`/`SIGTERM` still stops the
process while it's waiting. If it fails for a **permanent** reason instead
— a malformed/expired/wrong-audience JWT, or a token not authorized for
this slug — it raises a Lua error immediately rather than retrying forever,
since no amount of retrying fixes a bad token.

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

### Live streams

An `http(s)://` source doesn't have to be a downloadable file — point it
at a live stream (an Icecast/Shoutcast mountpoint, or anything else that
never sends `Content-Length` and keeps the connection open) and the audio
server auto-detects it from the response headers, no extra flag needed:

```lua
radio.queue({
  type = "url",
  location = "https://example.com/live-station",
  title = "Live relay",
}, "APPEND")
```

The server re-encodes it continuously (matching the station's fixed
target format, so it splices safely with everything else) and relays it
in real time. As far as your script is concerned it's just another
track — `radio.on_track_started`/`radio.on_track_ended` fire the same way
— except it has **no natural end**: it keeps playing until you call
[`radio.skip()`](#radioskip) or queue something else with
`"PLAY_NOW_INTERRUPT"`, or the upstream connection itself drops (which
ends it with `reason = "completed"`, same as any track finishing).

## `radio.dequeue(queue_id)`

Removes one still-pending (not yet playing) item from the queue.

```lua
local result = radio.queue("jingle.mp3", "APPEND")
local removed = radio.dequeue(result.queue_id)  -- true
```

Returns `false` — not an error — if `queue_id` wasn't found: it may already
have started playing, already been removed, or never existed. This cannot
remove whatever is currently playing (it's already left the queue by the
time it's playing) — use [`radio.skip()`](#radioskip) to just cut it, or
`radio.queue(source, "PLAY_NOW_INTERRUPT")` to cut it and replace it with
something specific.

## `radio.clear_queue([stop_current])`

Removes every pending item.

```lua
local removed_count = radio.clear_queue()

local removed_count, stopped_current = radio.clear_queue(true)
```

By default (`stop_current` omitted or `false`), this does not touch
whatever is currently playing — it keeps playing to completion, then falls
back to silence once it ends (since the queue is now empty). Pass
`stop_current = true` to also interrupt the current track immediately;
`stopped_current` in the return reports whether there actually was
something playing to interrupt. Playback falls back to silence rather than
jumping to another track, since the queue was just cleared — there's
nothing left to replace it with.

This is the usual pattern for a clean restart: call
`radio.clear_queue(true)` near the top of your script (after
`radio.register`) so a stale queue or a track left over from before a
restart doesn't keep playing underneath your script's fresh state.

```lua
radio.register("myfm", "My FM")
radio.clear_queue(true)  -- clean slate: drop pending items, stop whatever was playing
```

## `radio.skip()`

Interrupts whatever is currently playing, **leaving the rest of the queue
untouched** — playback immediately moves on to the next pending item (or
falls back to silence if the queue is empty).

```lua
local skipped = radio.skip()
```

Returns `false` if there was nothing playing to skip. This is the only
way to end a "track" that has no natural end — most notably a queued
[live stream](#live-streams), which otherwise plays indefinitely.
`radio.clear_queue(true)` also stops the current track, but drops
everything pending too; `radio.skip()` is the one to reach for when you
just want to move on to whatever's next.

## `radio.status()`

An on-demand snapshot of the registered station's current state — a
synchronous `GetStatus` call.

```lua
local status = radio.status()
print(status.is_silence)      -- true if playing the fallback silence loop
print(status.listener_count)
print(status.queue_length)
print(status.uptime_seconds)

if status.current_track then
  print(status.current_track.location)
end

for i, item in ipairs(status.queue) do
  print(i, item.queue_id, item.location, item.title, item.artist, item.mode)
end
```

`queue` is the full list of pending items in play order (each shaped like
the tables passed to [`radio.on_track_started`](events-and-scheduling.md#radioon_track_startedfn)),
not just the count — use it to see what's actually queued (e.g. to avoid
queueing the same track twice in a row) rather than just how much is
queued. `current_track` is `nil` while the station is playing silence.

Use this for polling; use [`radio.on_track_started`/`radio.on_track_ended`](events-and-scheduling.md)
for reacting to changes in real time without polling, or
[`radio.on_queue_low`](events-and-scheduling.md#radioon_queue_lowfn) if all
you need is "tell me when to queue more."
