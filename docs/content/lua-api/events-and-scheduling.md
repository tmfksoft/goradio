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
  print(track.location)  -- the TrackSource location that started playing
  print(track.title)     -- display_title, if the source set one
  print(track.artist)    -- display_artist, if the source set one
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

### Events without a Lua callback (yet)

The protocol also defines `QUEUE_UPDATED`, `LISTENER_COUNT_CHANGED`,
`SILENCE_STARTED`, and `SILENCE_ENDED` (see
[Protocol Reference](../developer-api/protocol-reference.md#subscribeevents)),
but there's no dedicated `radio.on_*` hook for them this phase — poll
[`radio.status()`](register-queue-status.md#radiostatus) if you need that
information. If you're writing a controller in another language instead of
Lua, all seven event types are available to you directly over
`SubscribeEvents`.
