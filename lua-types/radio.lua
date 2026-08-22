---@meta
--- Type stubs for the `radio` global, for LuaLS (the "Lua" VSCode
--- extension) intellisense. Not loaded at runtime -- see ../.luarc.json.
--- Keep in sync with internal/luastation/api.go and engine.go.

---@alias RadioQueueMode
---| "APPEND" # add to the end of the queue (default)
---| "PLAY_NEXT" # add to the front; plays after whatever's currently playing finishes
---| "PLAY_NOW_INTERRUPT" # add to the front and cut the currently playing clip immediately

---@class RadioTrackSource
---@field type? "local"|"url" # inferred from `location`'s prefix if omitted
---@field location string # path relative to the audio server's audio_root, or an http(s):// URL
---@field title? string
---@field artist? string
---@field cover_art? string # cover art URL, purely descriptive -- never fetched or validated by the audio server

---@class RadioRegisterOptions
---@field low_queue_threshold? integer # > 0 enables radio.on_queue_low; 0 (default) disables it
---@field logo_url? string # station logo/artwork URL, surfaced via radio.status()/radio.list_stations()
---@field metadata? table<string, string> # freeform key/value data (e.g. a group name) -- opaque to the audio server, surfaced via radio.status()/radio.list_stations()

---@class RadioRegisterInfo
---@field slug string
---@field stream_url string
---@field re_registered boolean

---@class RadioQueueResult
---@field queue_id string
---@field queue_position integer # 0 = currently playing/about to play immediately
---@field status string # informational only, e.g. "queued"

---@class RadioQueueItem
---@field queue_id string
---@field location string
---@field title string
---@field artist string
---@field cover_art string
---@field mode string
---@field duration_seconds integer # 0 = unknown/indefinite (a live relay, or not-yet-ready)

---@class RadioHistoryItem : RadioQueueItem
---@field reason "completed"|"interrupted"
---@field ended_at_unix_ms integer

---@class RadioStatus
---@field slug string
---@field name string
---@field is_registered boolean
---@field is_silence boolean
---@field is_paused boolean # true if current_track is paused (see radio.pause) -- silence is playing but current_track's position is held for radio.resume
---@field listener_count integer
---@field uptime_seconds integer
---@field queue_length integer
---@field logo_url string # station logo/artwork URL, if set (see radio.register's options.logo_url)
---@field metadata table<string, string> # freeform key/value data, if set (see radio.register's options.metadata)
---@field current_track? RadioQueueItem # nil while playing silence
---@field elapsed_seconds? integer # how long current_track has been playing; only set when current_track is
---@field queue RadioQueueItem[] # pending items, in play order
---@field history RadioHistoryItem[] # most recently finished items, oldest first, capped at a small fixed count -- seed your state from this once, then keep it current from radio.on_track_ended rather than re-polling radio.status()

---@class RadioTrackStartedEvent
---@field queue_id string
---@field location string
---@field title string
---@field artist string
---@field cover_art string
---@field duration_seconds integer # 0 = unknown/indefinite (a live relay)

---@class RadioTrackEndedEvent
---@field queue_id string
---@field reason "completed"|"interrupted"

---@class RadioErrorEvent
---@field message string
---@field code string # freeform, e.g. "TRANSCODE_FAILED"

---@class RadioQueueLowEvent
---@field queue_length integer
---@field threshold integer

---@class RadioStationSummary
---@field slug string
---@field name string
---@field listener_count integer
---@field logo_url string
---@field metadata table<string, string>

---@class RadioServerInfo
---@field version string # "dev" for a locally built binary with no version baked in via -ldflags

---@class radiolib
---@field args string[] # CLI args after --config/--script, e.g. {"myfm", "My FM"}
radio = {}

--- Registers (or re-registers) a station. Call once, typically at the top
--- of your script. Idempotent by slug -- re-registering an existing slug
--- updates its metadata in place without disrupting playback.
---@param slug string
---@param name? string # defaults to slug
---@param description? string # defaults to ""
---@param options? RadioRegisterOptions
---@return RadioRegisterInfo
function radio.register(slug, name, description, options) end

--- Removes this station from the audio server: stops its player, and
--- disconnects any listeners/SubscribeEvents streams on it. Does not
--- persist anywhere -- a later radio.register() call starts a fresh
--- station with an empty queue, not a resumed one. Every other radio.*
--- function that requires registration raises an error until
--- radio.register() is called again.
function radio.unregister() end

--- Queues something to play. Returns as soon as the item is accepted into
--- the queue -- not once it's confirmed playable; prefetch failures
--- surface later via radio.on_error.
---
--- An http(s):// source is auto-detected as either a finite file (fetched
--- and cached as usual) or a continuous live stream (e.g. an Icecast
--- mountpoint), re-encoded and relayed in real time. A live source has no
--- natural end -- it plays until radio.skip() or a new
--- "PLAY_NOW_INTERRUPT" item cuts it, same as any other track; you don't
--- need to know or declare which kind it is.
---@param source string|RadioTrackSource
---@param mode? RadioQueueMode # defaults to "APPEND"
---@return RadioQueueResult
function radio.queue(source, mode) end

--- Removes one still-pending (not yet playing) item. Returns false --
--- not an error -- if queue_id wasn't found.
---@param queue_id string
---@return boolean removed
function radio.dequeue(queue_id) end

--- Removes every pending item. Pass stop_current = true to also
--- interrupt whatever is currently playing (falls back to silence, since
--- the queue is now empty) -- the usual "clean restart" call.
---@param stop_current? boolean
---@return integer removed_count
---@return boolean stopped_current
function radio.clear_queue(stop_current) end

--- Interrupts whatever is currently playing, leaving the rest of the
--- queue untouched -- playback immediately moves on to the next pending
--- item (or silence). Returns false if nothing was playing to skip. This
--- is the only way to end a "track" with no natural end, such as a
--- queued live stream.
---@return boolean skipped
function radio.skip() end

--- Jumps playback straight to a specific pending item, by queue_id (not
--- position -- your own view of positions can be stale by the time the
--- call lands). Every item ahead of it in the queue is dropped
--- (removed_count), and whatever's currently playing is interrupted
--- (interrupted_current) so the target item starts immediately. Raises an
--- error if queue_id isn't a pending item -- you can't skip_to whatever's
--- already playing, since it's left the queue by then; use radio.skip()
--- for that instead.
---@param queue_id string
---@return integer removed_count
---@return boolean interrupted_current
function radio.skip_to(queue_id) end

--- Pauses the current track in place -- the station falls back to the
--- silence loop, same as an empty queue, until radio.resume(). Returns
--- false (not an error) if nothing is playing, the current track is a
--- live stream (no fixed position to hold), or it's already paused.
---@return boolean paused
function radio.pause() end

--- Resumes a paused track from exactly where it was paused. Returns
--- false if the station wasn't paused.
---@return boolean resumed
function radio.resume() end

--- Jumps the current track to an absolute position, clamped to
--- [0, duration]. Works whether or not the station is currently paused --
--- seeking while paused just moves where radio.resume() will pick up.
--- Returns seeked = false (not an error) if nothing seekable is playing --
--- no current track, or it's a live stream (no fixed position to seek
--- within).
---@param position_seconds integer
---@return boolean seeked
---@return integer position_seconds
function radio.seek(position_seconds) end

--- Jumps the current track by a signed delta from its current position
--- (positive = forward, negative = backward), clamped to [0, duration].
--- See radio.seek.
---@param delta_seconds integer
---@return boolean seeked
---@return integer position_seconds
function radio.seek_by(delta_seconds) end

--- An on-demand snapshot of the registered station's current state.
---@return RadioStatus
function radio.status() end

--- Lists every station this token authorizes -- not every station on the
--- server, and not just this script's own. Doesn't require radio.register()
--- to have been called first.
---@return RadioStationSummary[]
function radio.list_stations() end

--- Reports the audio server's build version. Not scoped to any station,
--- same as radio.list_stations().
---@return RadioServerInfo
function radio.server_info() end

--- Calls fn repeatedly, once every `seconds`.
---@param seconds number
---@param fn fun()
function radio.every(seconds, fn) end

--- Calls fn once, after `seconds`, then never again.
---@param seconds number
---@param fn fun()
function radio.after(seconds, fn) end

--- fn is called every time a queued item starts playing.
---@param fn fun(track: RadioTrackStartedEvent)
function radio.on_track_started(fn) end

--- fn is called every time a track finishes (reason: "completed" or
--- "interrupted").
---@param fn fun(track: RadioTrackEndedEvent)
function radio.on_track_ended(fn) end

--- fn is called when something goes wrong that doesn't have a better
--- place to surface -- most commonly a queued item that failed to
--- resolve or transcode.
---@param fn fun(err: RadioErrorEvent)
function radio.on_error(fn) end

--- fn is called (once, edge-triggered) whenever the pending queue length
--- drops to or below the low_queue_threshold given to radio.register.
--- No-op unless that threshold was set > 0.
---@param fn fun(ev: RadioQueueLowEvent)
function radio.on_queue_low(fn) end

--- fn is called every time the engine *automatically* (re-)registers this
--- station after the connection to the audio server dropped and came back
--- -- never for your own script's radio.register() call. Use it to
--- re-prime anything that only happens once at startup today (most
--- commonly, queueing a first track): if the audio server itself
--- restarted while disconnected, its registry -- and this station's
--- entire queue -- comes back empty, and radio.on_queue_low alone can't
--- rescue that on its own, since its edge trigger only fires transitioning
--- into "low" from "not low."
---@param fn fun()
function radio.on_register(fn) end
