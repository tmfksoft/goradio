---@meta
--- Type stub for `require("yaml")`, for LuaLS intellisense. Not loaded at
--- runtime -- see ../.luarc.json. Keep in sync with
--- internal/luastation/yamlmodule.go.

---@class yamllib
local yaml = {}

---@param str string
---@return any value
---@return string? err
function yaml.decode(str) end

---@param value any
---@return string? str
---@return string? err
function yaml.encode(value) end

return yaml
