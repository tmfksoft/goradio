---@meta
--- Type stub for `require("http")`, for LuaLS intellisense. Not loaded at
--- runtime -- see ../.luarc.json. Keep in sync with
--- internal/luastation/httpmodule.go.

---@class httplib
local http = {}

--- ok is false (with the error message as the second return) only if the
--- request itself failed (DNS, connection, timeout); a non-2xx HTTP
--- response is still ok=true so you can branch on status yourself.
---@param url string
---@param body? string
---@param headers? table<string, string>
---@return boolean ok
---@return integer|string status_or_error
---@return string? body
---@return table<string, string>? headers
function http.get(url, body, headers) end

---@param url string
---@param body? string
---@param headers? table<string, string>
---@return boolean ok
---@return integer|string status_or_error
---@return string? body
---@return table<string, string>? headers
function http.post(url, body, headers) end

---@param url string
---@param body? string
---@param headers? table<string, string>
---@return boolean ok
---@return integer|string status_or_error
---@return string? body
---@return table<string, string>? headers
function http.put(url, body, headers) end

---@param url string
---@param body? string
---@param headers? table<string, string>
---@return boolean ok
---@return integer|string status_or_error
---@return string? body
---@return table<string, string>? headers
function http.delete(url, body, headers) end

return http
