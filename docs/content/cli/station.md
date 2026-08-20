# `radio station`

Runs one Lua-scripted station controller process, connected to a
`radio serve` instance over gRPC.

```sh
radio station [--config station.yaml] [--script station.lua] [args...]
```

| Flag | Default | Description |
|---|---|---|
| `--config` | `station.yaml` | Path to the [station config file](../configuration/station-config.md) |
| `--script` | `station.lua` | Path to the Lua script to run |

**Everything after `--config`/`--script` is not parsed by the CLI at all.**
It's collected as-is and handed to the Lua script, exposed as the
`radio.args` array table. This is deliberate: it lets one shared script
drive many different stations purely by how it's invoked, e.g.:

```sh
radio station --config myfm.yaml    --script stations.lua myfm "My FM"
radio station --config otherfm.yaml --script stations.lua otherfm "Other FM"
```

Both invocations run the same `stations.lua`, but `radio.args` is
`{"myfm", "My FM"}` in the first process and `{"otherfm", "Other FM"}` in
the second — the script reads `radio.args` to decide which station it's
being run as. See [Lua Scripting API](../lua-api/index.md).

## One process, one station

Each `radio station` process runs exactly one Lua script and (normally)
registers exactly one station. Running several stations means running
several `radio station` processes — under systemd, pm2, supervisord, or
whatever process manager you prefer. This mirrors the legacy
one-process-per-station model and gives you crash isolation between
stations.

## What it does on boot

1. Loads the config; warns if `auth.jwt` is unset (the server will reject
   every call).
2. Dials the audio server's gRPC address (plaintext — no TLS this phase).
3. Sets up the Lua environment (the `radio` table, plus `require("http")`
   and `require("sql")`) and starts the optional [local control
   API](../configuration/station-config.md#api-local-control-api) if enabled.
4. Runs the script. Script top-level code typically calls `radio.register`
   once and registers `radio.on_*` callbacks / `radio.every`/`radio.after`
   timers.
5. Enters its event loop: dispatching due timers and incoming
   `SubscribeEvents` messages into the script's callbacks until the process
   receives `SIGINT`/`SIGTERM`.

If the connection to the audio server drops — or the audio server itself
restarts, wiping its in-memory station registry — the engine automatically
re-registers and reconnects the event stream, with exponential backoff.
Your script doesn't need to handle this itself.
