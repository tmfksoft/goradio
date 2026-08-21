---@meta
--- Type stub for `require("sql")`, for LuaLS intellisense. Not loaded at
--- runtime -- see ../.luarc.json. Keep in sync with
--- internal/luastation/sqlmodule.go.

---@class SqlDB
local SqlDB = {}

--- Parameterized query (`?` placeholders). Returns an array of rows, each
--- a table keyed by column name; SQL NULL becomes Lua nil.
---@param query string
---@param ... any
---@return table[]? rows
---@return string? err
function SqlDB:query(query, ...) end

--- Parameterized statement for INSERT/UPDATE/DELETE.
---@param query string
---@param ... any
---@return boolean ok
---@return integer|string rows_affected_or_err # rows_affected if ok, error string if not
---@return integer? last_insert_id
function SqlDB:exec(query, ...) end

--- Closes the connection pool.
function SqlDB:close() end

---@class sqllib
local sql = {}

--- Connects and pings immediately, so a bad DSN or unreachable server
--- fails here rather than on first query.
---@param dsn string # Go MySQL DSN, e.g. "user:pass@tcp(host:3306)/dbname"
---@return SqlDB? db
---@return string? err
function sql.open(dsn) end

return sql
