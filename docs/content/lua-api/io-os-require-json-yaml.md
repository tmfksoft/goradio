# io / os / require, and JSON/YAML

Station scripts run on [gopher-lua](https://github.com/yuin/gopher-lua),
which opens the **full Lua 5.1 standard library** by default — nothing in
GoRadio restricts it. This follows the same "full trusted access" decision
already made for [`http`/`sql`](http-and-sql-modules.md) and
[`redis`](redis-module.md): a station script is code you (or someone you
trust) wrote, not a sandboxed third-party plugin.

That means the standard `io`, `os`, `require`/`package`, and `coroutine`
libraries all work exactly as in stock Lua:

```lua
local f = io.open("station-data.json", "r")
local contents = f:read("*a")
f:close()

os.getenv("HOME")
os.date("%Y-%m-%d")
```

!!! warning "`os.execute` and `io.popen` run arbitrary shell commands"
    Because the full stdlib is open, a station script can run **any**
    shell command as the `radio station` process's user via
    `os.execute("...")` or `io.popen("...")`. There's no allowlist or
    sandbox — treat a station script with the same trust you'd give a
    shell script, because it effectively is one. Don't run scripts you
    don't trust.

## `require`

Split a script across multiple `.lua` files with the standard `require`:

```lua
-- station.lua
local playlist = require("playlist")
playlist.queue_next()
```

```lua
-- playlist.lua, next to station.lua
local M = {}
function M.queue_next()
  radio.queue("/audio/track1.mp3")
end
return M
```

`package.path` is set up so `require` resolves **relative to the running
script's own directory**, regardless of the working directory `radio
station` was launched from — `require("playlist")` looks for
`playlist.lua` next to `station.lua`, and `require("lib.util")` looks for
`lib/util.lua` the same way, following Lua's usual dot-to-slash module
naming. This is a deliberate fix over gopher-lua's stock default (which
resolves against the process's current working directory and would break
depending on how/where you launch `radio station`).

## JSON and YAML

`io` gives you file reading; `json`/`yaml` let you actually parse what you
read — e.g. a station's own config-style data file, kept separate from
`station.yaml` (which is GoRadio's own config, not yours to repurpose).

```lua
local json = require("json")
local yaml = require("yaml")

local f = io.open("playlist.json", "r")
local decoded, err = json.decode(f:read("*a"))
f:close()

if err then
  print("bad playlist.json: " .. err)
else
  for _, track in ipairs(decoded.tracks) do
    radio.queue(track.path)
  end
end

local encoded, eerr = json.encode({ name = "myfm", tags = {"rock", "pop"} })
```

Both modules expose the same two functions:

| Function | Signature |
|---|---|
| `decode(str)` | `-> value, err` — `err` is `nil` on success |
| `encode(value)` | `-> str, err` — `err` is `nil` on success |

Tables round-trip as JSON/YAML objects or arrays based on their keys: a
table keyed `1..N` with no gaps encodes as an array, anything else encodes
as an object/map. `nil`/missing fields decode to Lua `nil` as usual — a
JSON/YAML `null` also decodes to Lua `nil`, so absent-vs-null isn't
distinguishable, same as most Lua JSON libraries.

`yaml.decode`/`yaml.encode` use [`gopkg.in/yaml.v3`](https://pkg.go.dev/gopkg.in/yaml.v3);
`json.decode`/`json.encode` use Go's `encoding/json`.
