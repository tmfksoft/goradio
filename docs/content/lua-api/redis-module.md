# Redis Module

`require("redis")` gives station scripts real Redis access — key/value,
lists, and pub/sub — with no restrictions, per the same "full trusted
access" decision as the [HTTP and SQL modules](http-and-sql-modules.md):
station authors are trusted operators, not sandboxed third parties.

The main use case this enables: a **listener request system**. An external
app (a web page, a Discord bot, whatever) pushes song requests into Redis;
your station script pulls or reacts to them and calls `radio.queue`. Two
ways to wire that up — pick whichever fits:

- **A list as a FIFO queue** — the external app `RPUSH`es requests, your
  script polls with `LPOP` (e.g. from [`radio.every`](events-and-scheduling.md#radioeveryseconds-fn)).
- **Pub/sub** — the external app `PUBLISH`es requests, your script reacts
  immediately via `client:subscribe`, no polling needed.

## `redis.open(addr [, options])`

```lua
local redis = require("redis")

local client, err = redis.open("127.0.0.1:6379")
-- or with auth/db select:
local client, err = redis.open("127.0.0.1:6379", {password = "secret", db = 0})

if not client then
  print("connect failed: " .. err)
  return
end
```

Connects and pings immediately, so a bad address or unreachable server
fails right there rather than on first command.

## Key/value

```lua
local ok, err = client:set(key, value [, ttl_seconds])
local value = client:get(key)        -- nil if the key doesn't exist (not an error)
local removed_count, err = client:del(key1, key2, ...)
local found = client:exists(key)     -- bool
```

## Lists — a simple request queue

```lua
-- external app: RPUSH requests "some_track.mp3"

local new_length, err = client:rpush(key, value)
local new_length, err = client:lpush(key, value)
local value = client:lpop(key)       -- nil if the list is empty (not an error)
local value = client:rpop(key)
local length, err = client:llen(key)
```

```lua
radio.every(15, function()
  local requested = client:lpop("requests")
  if requested then
    radio.queue(requested, "PLAY_NEXT")
  end
end)
```

## Pub/sub

```lua
local ok, err = client:publish(channel, message)

client:subscribe(channel, function(payload, channel)
  radio.queue(payload, "PLAY_NEXT")
end)
```

`subscribe`'s callback runs on the engine's single Lua goroutine — same as
every other `radio.on_*` callback — so it's safe to call `radio.queue`,
touch other Lua state, etc. directly from it. The subscription runs for
the life of the script (or until `client:close()`); reconnects aren't
handled automatically the way the audio-server connection is, so for a
long-lived deployment prefer the list-polling pattern above if the Redis
server itself is expected to restart/move — pub/sub subscriptions don't
survive that.

## `client:close()`

Closes the connection. Not required for a script that runs for the
process's whole lifetime, but call it if you're opening short-lived
connections for some reason.
