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

---@class RadioRegisterOptions
---@field low_queue_threshold? integer # > 0 enables radio.on_queue_low; 0 (default) disables it

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
---@field mode string
---@field duration_seconds integer # 0 = unknown/indefinite (a live relay, or not-yet-ready)

---@class RadioStatus
---@field slug string
---@field name string
---@field is_registered boolean
---@field is_silence boolean
---@field listener_count integer
---@field uptime_seconds integer
---@field queue_length integer
---@field current_track? RadioQueueItem # nil while playing silence
---@field elapsed_seconds? integer # how long current_track has been playing; only set when current_track is
---@field queue RadioQueueItem[] # pending items, in play order

---@class RadioTrackStartedEvent
---@field queue_id string
---@field location string
---@field title string
---@field artist string
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

--- An on-demand snapshot of the registered station's current state.
---@return RadioStatus
function radio.status() end

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
