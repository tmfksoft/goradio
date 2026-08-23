# Lua Scripting API — Overview

Every `radio station` process runs one Lua script (via
[gopher-lua](https://github.com/yuin/gopher-lua), a pure-Go Lua 5.1 VM) on
a single dedicated goroutine, with a global `radio` table plus the full Lua
standard library and a few extra modules pre-registered:

```lua
radio.args               -- CLI passthrough args, see below
radio.register(slug, name, description [, options])
radio.unregister()
radio.queue(source, mode)
radio.dequeue(queue_id)
radio.clear_queue()
radio.skip()
radio.skip_to(queue_id)
radio.pause()
radio.resume()
radio.seek(position_seconds)
radio.seek_by(delta_seconds)
radio.status()
radio.list_stations()
radio.server_info()
radio.every(seconds, fn)
radio.after(seconds, fn)
radio.on_track_started(fn)
radio.on_track_ended(fn)
radio.on_error(fn)
radio.on_queue_low(fn)

local http  = require("http")   -- full, unrestricted HTTP client
local sql   = require("sql")    -- full MySQL access via database/sql
local redis = require("redis")  -- full Redis access: KV, lists, pub/sub
local json  = require("json")   -- JSON decode/encode
local yaml  = require("yaml")   -- YAML decode/encode

io.open(...)   -- stock Lua io/os/require, full stdlib -- see below
```

This is deliberately minimal — enough to register a station, queue tracks,
react to playback events, and pull data from anywhere over HTTP or MySQL to
decide what to queue next. It is **not** a genre-specific content engine
(no built-in concept of songs-with-intros, jingles, ad rotation, and so
on) — those are things you build in Lua on top of these primitives, using
`radio.every`/`radio.on_track_ended` to drive your own playout logic and
`http`/`sql` to pull in whatever data informs it.

## `radio.args`

An array table of every CLI argument after `--config`/`--script` on the
`radio station` command line, unparsed. This is how one shared script can
serve multiple different stations — see [`radio station`](../cli/station.md)
for the full explanation, and [Getting Started → Quickstart](../getting-started/quickstart.md)
for a worked example.

```lua
-- invoked as: radio station --script stations.lua myfm "My FM"
print(radio.args[1])  --> "myfm"
print(radio.args[2])  --> "My FM"
```

## Full reference

- [Registering & Queueing](register-queue-status.md) — `radio.register`, `radio.queue`, `radio.status`
- [Events & Scheduling](events-and-scheduling.md) — `radio.every`, `radio.after`, `radio.on_*`
- [HTTP & SQL Modules](http-and-sql-modules.md) — `require("http")`, `require("sql")`
- [Redis Module](redis-module.md) — `require("redis")`, including a listener request-system pattern
- [io / os / require, JSON & YAML](io-os-require-json-yaml.md) — the full Lua stdlib, splitting a script across files, and `require("json")`/`require("yaml")`
- [Editor Support (VS Code)](editor-support.md) — autocomplete/type-checking for all of the above via LuaLS

Or read [`testdata/station-scripts/example.lua`](https://github.com/tmfksoft/goradio/blob/main/testdata/station-scripts/example.lua)
for a complete, working reference script that exercises all of the above.

## A note on execution model

`gopher-lua`'s VM is not goroutine-safe, so **all** of your script's code —
top-level statements, `radio.every`/`radio.after` callbacks, and
`radio.on_*` event callbacks — run one at a time, on one goroutine. You
don't need locks or coroutine-safety in your script; you do need to know
that a slow callback (e.g. a slow `http.get` or `sql` query) will delay
everything else the engine needs to do — event dispatch, other timers —
until it returns. Keep callbacks reasonably quick, or offload slow work by
design (e.g. cache results from a `radio.every` poll rather than querying
inline in `on_track_ended`).
