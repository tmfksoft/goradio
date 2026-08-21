---@meta
--- Type stub for `require("redis")`, for LuaLS intellisense. Not loaded
--- at runtime -- see ../.luarc.json. Keep in sync with
--- internal/luastation/redismodule.go.

---@class RedisOpenOptions
---@field password? string
---@field db? integer

---@class RedisClient
local RedisClient = {}

---@param key string
---@return string? value # nil if the key doesn't exist (not an error)
---@return string? err
function RedisClient:get(key) end

---@param key string
---@param value string
---@param ttl_seconds? number
---@return boolean ok
---@return string? err
function RedisClient:set(key, value, ttl_seconds) end

---@param ... string
---@return integer? removed_count
---@return string? err
function RedisClient:del(...) end

---@param key string
---@return boolean found
---@return string? err
function RedisClient:exists(key) end

---@param key string
---@param value string
---@return integer? new_length
---@return string? err
function RedisClient:lpush(key, value) end

---@param key string
---@param value string
---@return integer? new_length
---@return string? err
function RedisClient:rpush(key, value) end

---@param key string
---@return string? value # nil if the list is empty (not an error)
---@return string? err
function RedisClient:lpop(key) end

---@param key string
---@return string? value # nil if the list is empty (not an error)
---@return string? err
function RedisClient:rpop(key) end

---@param key string
---@return integer? length
---@return string? err
function RedisClient:llen(key) end

---@param channel string
---@param message string
---@return boolean ok
---@return string? err
function RedisClient:publish(channel, message) end

--- fn runs on the engine's single Lua goroutine -- same as any radio.on_*
--- callback -- every time a message arrives on `channel`, for the life of
--- the script (or until :close()). Reconnects aren't handled
--- automatically the way the audio-server connection is.
---@param channel string
---@param fn fun(payload: string, channel: string)
function RedisClient:subscribe(channel, fn) end

function RedisClient:close() end

---@class redislib
local redis = {}

--- Connects and pings immediately, so a bad address or unreachable server
--- fails here rather than on first command.
---@param addr string # "host:port"
---@param options? RedisOpenOptions
---@return RedisClient? client
---@return string? err
function redis.open(addr, options) end

return redis
