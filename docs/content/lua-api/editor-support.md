# Editor Support (VS Code)

Station scripts get real autocomplete, hover docs, and type-checking for
the `radio` global and the `http`/`sql`/`redis`/`json`/`yaml` modules — via
[LuaLS](https://github.com/LuaLS/lua-language-server) (the **Lua**
extension, published as `sumneko.lua`) and a set of type-stub files
shipped in this repo (and in every [release archive](../getting-started/installation.md#or-pre-built-binaries)).

## Setup

1. Install the **Lua** extension (`sumneko.lua`) in VS Code.
2. Open the folder that contains `.luarc.json` and `lua-types/` as your
   VS Code workspace — not just a single `.lua` file. (If you're working
   from a release archive, that's the extracted archive folder; if you're
   working from a checkout of this repo, that's the repo root.)

That's it — `.luarc.json` points LuaLS at `lua-types/` and declares
`radio` as a known global; opening any `.lua` file in that workspace
picks it up automatically.

## What you get

```lua
radio.reg   -- autocomplete suggests `register`
radio.register()  -- red squiggle: "This function requires 1 argument(s) but instead it is receiving 0."

local status = radio.status()
status.   -- autocomplete lists slug, name, is_silence, queue, current_track, ...
status.mispelled_field  -- red squiggle: "Undefined field `mispelled_field`."

radio.every("five", function() end)  -- red squiggle: string passed where a number is expected

local redis = require("redis")
local client = redis.open("localhost:6379")
client:  -- autocomplete lists get, set, del, lpush, subscribe, close, ...
```

## How it works

`lua-types/*.lua` are [LuaCATS/EmmyLua](https://luals.github.io/wiki/annotations/)
annotation files — `---@meta` marks them as type-only (LuaLS won't treat
them as real runtime code, and your actual station script never loads
them). Each one mirrors the real Go implementation:

| Stub | Mirrors |
|---|---|
| `lua-types/radio.lua` | `internal/luastation/api.go`, `engine.go` |
| `lua-types/http.lua` | `internal/luastation/httpmodule.go` |
| `lua-types/sql.lua` | `internal/luastation/sqlmodule.go` |
| `lua-types/redis.lua` | `internal/luastation/redismodule.go` |
| `lua-types/json.lua` | `internal/luastation/jsonmodule.go` |
| `lua-types/yaml.lua` | `internal/luastation/yamlmodule.go` |

If you're contributing a change to the Lua API surface (a new `radio.*`
function, a new module method), update the matching stub in the same
change — nothing enforces they stay in sync automatically, they're just
documentation-as-code for the editor.

These stubs were verified against the real LuaLS engine (`lua-language-server --check`),
including negative tests confirming it actually flags mistakes (missing
arguments, unknown fields, wrong argument types) rather than silently
accepting anything.
