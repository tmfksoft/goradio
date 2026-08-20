# HTTP & SQL Modules

Station scripts are trusted code written by you (or someone you trust) —
not sandboxed third-party plugins — so both modules give **full,
unrestricted access**: any URL, any database, no allowlists. This is what
lets a script decide what to play based on an external API, a database of
your own station's content metadata, listener requests stored somewhere,
or anything else you can reach over HTTP or SQL.

## `require("http")`

```lua
local http = require("http")

local ok, status, body, headers = http.get("https://example.com/next-track")
local ok, status, body, headers = http.post(url, body_string, {["Content-Type"] = "application/json"})
local ok, status, body, headers = http.put(url, body_string)
local ok, status, body, headers = http.delete(url)
```

Each function returns four values:

| Return | Meaning |
|---|---|
| `ok` | `false` only if the request itself failed (DNS, connection, timeout) — a non-2xx HTTP response is still `ok = true` so you can branch on `status` yourself |
| `status` (if `ok`) / error message (if not `ok`) | HTTP status code, or the error string |
| `body` | Response body as a string |
| `headers` | Table of response headers, keyed by header name |

```lua
local ok, status, body, headers = http.get("https://example.com/api/next")
if not ok then
  print("request failed: " .. status)
elseif status ~= 200 then
  print("unexpected status: " .. status)
else
  -- do something with body
end
```

`body`/an optional third argument for `post`/`put` is a plain string —
encode JSON yourself (e.g. with a small hand-rolled encoder, or a Lua JSON
library you vendor alongside your script) if that's what the target API
expects. Response bodies are capped at 20MB to guard against a runaway
response; this is a sanity limit, not a security boundary.

## `require("sql")`

MySQL access via Go's `database/sql`.

```lua
local sql = require("sql")

local db, err = sql.open("user:password@tcp(127.0.0.1:3306)/mydb")
if not db then
  print("connect failed: " .. err)
  return
end

local rows, err = db:query("SELECT id, title FROM tracks WHERE played_at IS NULL LIMIT 10")
if rows then
  for _, row in ipairs(rows) do
    print(row.id, row.title)
  end
end

local ok, affected, last_insert_id = db:exec(
  "UPDATE tracks SET played_at = NOW() WHERE id = ?", row.id
)

db:close()
```

`sql.open(dsn)` — `dsn` is a [Go MySQL driver DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name)
(`user:pass@tcp(host:port)/dbname`). It connects and pings immediately, so a
bad DSN or unreachable server fails right there rather than on first query.

`db:query(sql, ...)` — parameterized query (`?` placeholders); returns an
array of tables, one per row, keyed by column name. Column values come back
as Lua strings, numbers, or booleans depending on their SQL type; `NULL`
comes back as Lua `nil`.

`db:exec(sql, ...)` — parameterized statement for `INSERT`/`UPDATE`/`DELETE`;
returns `ok, rows_affected, last_insert_id`, or `false, error_message` on
failure.

`db:close()` — closes the connection pool. There's no automatic connection
pooling/reuse guidance built in beyond what `database/sql` does internally
— for a long-running station script, open the connection once at startup
and reuse it, rather than opening/closing per query.
