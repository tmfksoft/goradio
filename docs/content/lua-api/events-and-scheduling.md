# Events & Scheduling

## Scheduling

### `radio.every(seconds, fn)`

Calls `fn` repeatedly, once every `seconds`. This is the usual way to drive
periodic decisions — e.g. "queue something new every few minutes":

```lua
radio.every(180, function()
  local pick = pick_next_track()  -- your own logic
  radio.queue(pick, "APPEND")
end)
```

### `radio.after(seconds, fn)`

Calls `fn` once, after `seconds`, then never again.

```lua
radio.after(10, function()
  print("ten seconds in")
end)
```

Both are checked roughly every 200ms and run inline with the rest of the
script's event loop — see [the execution model note](index.md#a-note-on-execution-model)
for why a slow callback body delays everything else.

## Events

Register a callback once; it's called every time a matching event arrives
from the audio server's `SubscribeEvents` stream, for as long as the
process runs. The engine manages the subscription (including reconnects)
for you.

### `radio.on_track_started(fn)`

```lua
radio.on_track_started(function(track)
  print(track.queue_id)
  print(track.location)          -- the TrackSource location that started playing
  print(track.title)             -- display_title, if the source set one
  print(track.artist)            -- display_artist, if the source set one
  print(track.cover_art)         -- cover_art_url, if the source set one
  print(track.duration_seconds)  -- 0 = unknown/indefinite (a live relay)
end)
```

### `radio.on_track_ended(fn)`

```lua
radio.on_track_ended(function(track)
  print(track.queue_id)
  print(track.reason)  -- "completed" or "interrupted"
end)
```

`reason` is `"interrupted"` when a `PLAY_NOW_INTERRUPT` cut this clip short
(or the process is shutting down mid-clip), and `"completed"` otherwise.

### `radio.on_error(fn)`

```lua
radio.on_error(function(err)
  print(err.message)
  print(err.code)  -- freeform, e.g. "TRANSCODE_FAILED"
end)
```

Fired when something goes wrong that doesn't have a better place to
surface — most commonly, a queued item's source failed to resolve or
transcode (see the note in [`radio.queue`](register-queue-status.md#radioqueuesource-mode)).

### `radio.on_queue_low(fn)`

```lua
radio.register("myfm", "My FM", "", {low_queue_threshold = 3})

radio.on_queue_low(function(ev)
  print(ev.queue_length)  -- current pending queue length
  print(ev.threshold)     -- the low_queue_threshold you registered with
  radio.queue(pick_next_track(), "APPEND")
end)
```

This is the recommended way to keep a station's queue topped up — instead
of polling `radio.status().queue_length` on a timer, set
`low_queue_threshold` when you [register](register-queue-status.md#radioregisterslug-name-description-options),
and react exactly when the server tells you it's time to queue more. It's
**edge-triggered**: it fires once when the queue length drops to or below
the threshold, then won't fire again until the length rises back above the
threshold and dips again — so a callback that queues exactly one track at a
time won't be called repeatedly while the queue happens to sit at or below
threshold. No-op (never fires) unless `low_queue_threshold` was set > 0.

!!! warning "This alone can't recover a queue after the audio server restarts"
    `on_queue_low`'s edge trigger only fires on a transition *into* "low"
    from "not low." If the audio server restarts while you're
    disconnected, its registry — and this station's entire queue — comes
    back empty when the engine automatically re-registers you (see
    `on_register` below), and an empty queue that's been empty from the
    moment it was created never has a "not low → low" transition to
    trigger on. Prime the queue in `on_register`, not just once at the top
    of your script, if you want playback to actually resume after that.

### `radio.on_register(fn)`

```lua
radio.on_register(function()
  queue_something()  -- whatever you'd otherwise only do once at startup
end)
```

Fired every time the engine *automatically* (re-)registers this station
after the connection to the audio server dropped and came back — never
for your own script's `radio.register()` call. This is the only reliable
place to re-prime a queue that a server restart may have wiped (see the
warning above) — a script that only queues its first track once, at the
top of the file, goes silent after such a restart otherwise, since nothing
else will ever queue another track for it. Cheap to make idempotent: check
`radio.status().queue_length` first if you don't want to add a redundant
track on a reconnect where the queue was actually still healthy (e.g. a
brief network blip that didn't restart the server itself).

### Events without a Lua callback (yet)

The protocol also defines `QUEUE_UPDATED`, `LISTENER_COUNT_CHANGED`,
`SILENCE_STARTED`, and `SILENCE_ENDED` (see
[Protocol Reference](../developer-api/protocol-reference.md#subscribeevents)),
but there's no dedicated `radio.on_*` hook for them this phase — poll
[`radio.status()`](register-queue-status.md#radiostatus) if you need that
information. If you're writing a controller in another language instead of
Lua, all eight event types are available to you directly over
`SubscribeEvents`.
