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

`options` is an optional table recognizing two fields:

- `low_queue_threshold` — enables the [`radio.on_queue_low`](events-and-scheduling.md#radioon_queue_lowfn) event.
- `logo_url` — an optional station logo/artwork URL, surfaced via [`radio.status()`](#radiostatus)/[`radio.list_stations()`](#radiolist_stations). Purely descriptive — never fetched or validated by the audio server.

```lua
radio.register("myfm", "My FM", "", {low_queue_threshold = 3, logo_url = "https://example.com/myfm.png"})
```

Registration is **idempotent by slug**: calling it again for a slug that's
already registered just updates its display metadata in place, without
disrupting whatever is currently queued or playing. This is what makes
reconnects safe — and you don't need to think about reconnects at all,
because the engine calls this for you automatically (with exponential
backoff) if the connection to the audio server drops or the server itself
restarts. It's also how you change the logo (or name/description) **on
the fly**: just call `radio.register` again with the same slug and the
new value — nothing is merged, though, so pass every field you want kept
each time (a call that omits `logo_url` clears a previously set one, same
as it already does for `name`/`description`).

If the very first `radio.register` call fails for a **transient** reason
(e.g. the audio server isn't up yet), it blocks and retries internally with
backoff rather than raising a Lua error — your script's top-level code
simply waits until it succeeds, and `Ctrl+C`/`SIGTERM` still stops the
process while it's waiting. If it fails for a **permanent** reason instead
— a malformed/expired/wrong-audience JWT, or a token not authorized for
this slug — it raises a Lua error immediately rather than retrying forever,
since no amount of retrying fixes a bad token.

## `radio.unregister()`

Removes this station from the audio server: stops its player, and
disconnects any listeners on its stream and any open `radio.on_*` event
subscription.

```lua
radio.unregister()
```

This does not persist anywhere — a later `radio.register()` call starts a
fresh station with an empty queue, not a resumed one. After calling this,
every other `radio.*` function that requires registration (`radio.queue`,
`radio.status`, `radio.skip`, ...) raises a Lua error until
`radio.register()` is called again — and the engine's automatic reconnect
logic (see above) won't try to re-register on your behalf in the meantime,
since this was an intentional unregister, not a dropped connection.

## `radio.queue(source, mode)`

Queues something to play.

```lua
radio.queue("jingle.mp3", "APPEND")

radio.queue({
  type = "url",
  location = "https://example.com/track.mp3",
  title = "Track Title",
  artist = "Some Artist",
  cover_art = "https://example.com/track-cover.jpg",
}, "PLAY_NEXT")
```

**`source`** is either:

- a plain string — a path relative to the audio server's `audio.audio_root`,
  or an `http://`/`https://` URL (detected automatically by its prefix), or
- a table: `{type = "local"|"url", location = ..., title = ..., artist = ..., cover_art = ...}`.
  `title`/`artist`/`cover_art` are all optional metadata carried through
  unchanged to `radio.on_track_started`, `radio.status()`'s
  `current_track`/`queue`/`history`, and future ICY metadata support —
  purely descriptive, never fetched or validated by the audio server;
  `type` is optional too and inferred from `location`'s prefix if omitted.

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

## `radio.skip_to(queue_id)`

Jumps playback straight to a specific pending item, dropping everything
queued ahead of it and interrupting whatever's currently playing so the
target item starts immediately.

```lua
local a = radio.queue("a.mp3")
local b = radio.queue("b.mp3")
local c = radio.queue("c.mp3")

local removed_count, interrupted_current = radio.skip_to(c.queue_id)
-- removed_count == 2 (a and b were dropped)
-- interrupted_current == true if something was already playing
```

Addressed by `queue_id`, not position — your own view of positions can be
stale by the time the call lands (something could finish or get queued in
between), but a `queue_id` can't drift out from under you. Raises a Lua
error if `queue_id` isn't a pending item — in particular, you can't
`skip_to` whatever's already playing, since it's already left the queue by
then; use plain [`radio.skip()`](#radioskip) to interrupt the current
track without changing what plays after it.

## `radio.pause()` / `radio.resume()`

Pauses the current track **in place** — the station falls back to the
silence loop, same as an empty queue, until `radio.resume()` — then
resumes it from exactly where it left off.

```lua
local paused = radio.pause()    -- station now plays silence
-- ... later ...
local resumed = radio.resume()  -- picks the same track back up mid-way
```

Both return `false` (not an error) rather than doing nothing silently:
`radio.pause()` is `false` if nothing is playing, the current track is a
[live stream](#live-streams) (there's no fixed position to hold — a live
relay can only be skipped, not paused), or it's already paused;
`radio.resume()` is `false` if the station wasn't paused. This is a
station-wide pause — like everything else here, it affects the one shared
broadcast every listener is tuned into, not some per-listener state.

`radio.skip()`/`radio.skip_to()`/`radio.clear_queue()` all still work as
normal on a paused station — they end the paused track (or replace it)
rather than being swallowed by the pause.

## `radio.seek(position_seconds)` / `radio.seek_by(delta_seconds)`

Jumps the current track to a new position — `radio.seek` to an absolute
position, `radio.seek_by` by a signed delta from wherever it currently is
(positive = forward, negative = backward). Both clamp to
`[0, duration_seconds]` and return the resulting (clamped) position.

```lua
local seeked, position = radio.seek(30)      -- jump to 0:30
local seeked, position = radio.seek_by(10)   -- 10s forward from here
local seeked, position = radio.seek_by(-10)  -- 10s back
```

`seeked` is `false` (not an error) if nothing seekable is playing — no
current track, or it's a [live stream](#live-streams) (no fixed position
to seek within). Works the same whether or not the station is currently
paused: seeking while paused just moves where `radio.resume()` will pick
up, without itself resuming playback.

## `radio.status()`

An on-demand snapshot of the registered station's current state — a
synchronous `GetStatus` call.

```lua
local status = radio.status()
print(status.is_silence)      -- true if playing the fallback silence loop
print(status.is_paused)       -- true if current_track is paused (see radio.pause)
print(status.logo_url)        -- station logo/artwork URL, if set
print(status.listener_count)
print(status.queue_length)
print(status.uptime_seconds)

if status.current_track then
  print(status.current_track.location)
  print(status.current_track.duration_seconds)  -- 0 = unknown/indefinite (a live relay)
  print(status.elapsed_seconds)                  -- how long it's been playing
end

for i, item in ipairs(status.queue) do
  print(i, item.queue_id, item.location, item.title, item.artist, item.mode, item.duration_seconds)
end

for i, item in ipairs(status.history) do
  print(i, item.queue_id, item.location, item.reason, item.ended_at_unix_ms)
end
```

`queue` is the full list of pending items in play order (each shaped like
the tables passed to [`radio.on_track_started`](events-and-scheduling.md#radioon_track_startedfn)),
not just the count — use it to see what's actually queued (e.g. to avoid
queueing the same track twice in a row) rather than just how much is
queued. `current_track` is `nil` while the station is playing silence.
`duration_seconds` and `status.elapsed_seconds` are exactly what you'd
need to render a progress bar — treat `duration_seconds == 0` as
"indefinite" rather than dividing by zero, which is always the case for a
queued live stream.

`history` is the most recently finished items, oldest first, capped at a
small fixed count (currently 20) — each entry adds `reason`
(`"completed"`/`"interrupted"`) and `ended_at_unix_ms` on top of the same
shape as `queue`'s items. To requeue something from history, just call
`radio.queue` again with its `location`/`title`/`artist`/`cover_art` —
there's no separate "requeue" call.

`status()` and its `history` field are meant for a one-shot fetch (e.g.
right after `radio.register`, to see what was already playing/queued from
before a restart) — use [`radio.on_track_started`/`radio.on_track_ended`](events-and-scheduling.md)
to keep your own view of `queue`/`history` current after that without
polling, or [`radio.on_queue_low`](events-and-scheduling.md#radioon_queue_lowfn)
if all you need is "tell me when to queue more."

## `radio.list_stations()`

Lists every station **this token authorizes** — not every station on the
server, and not just this script's own. Doesn't require `radio.register()`
to have been called first, since it isn't scoped to "this" station at all.

```lua
for _, st in ipairs(radio.list_stations()) do
  print(st.slug, st.name, st.listener_count, st.logo_url)
end
```

Each entry is `{slug, name, listener_count, logo_url}` — deliberately
lighter than `radio.status()` (no queue, no history, no current track),
since this is meant to summarize many stations in one call rather than
give a full picture of one. Useful for a script that drives several stations from a
shared token and wants to see the whole picture — e.g. deciding what to
play next based on what's already playing elsewhere — without hardcoding
every slug or polling `radio.status()` once per station.

A token scoped to a single slug only ever sees that one slug here; this
never raises an error for a slug outside the token's scope, it's just
omitted from the result.
