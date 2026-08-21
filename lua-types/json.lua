---@meta
--- Type stub for `require("json")`, for LuaLS intellisense. Not loaded at
--- runtime -- see ../.luarc.json. Keep in sync with
--- internal/luastation/jsonmodule.go.

---@class jsonlib
local json = {}

---@param str string
---@return any value
---@return string? err
function json.decode(str) end

---@param value any
---@return string? str
---@return string? err
function json.encode(value) end

return json
