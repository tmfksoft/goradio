# Developer / Protocol API — Overview

The audio server exposes one service, `AudioServerService`, defined in
`proto/audioserver/v1`. It's reachable over gRPC, gRPC-Web, or plain
HTTP+JSON — same port, same handlers, same auth — so a station controller
doesn't have to be the Lua engine bundled in this repo. Anything that can
make an HTTP request and hold a JWT can be a controller. `internal/luastation` (this repo's Lua engine) is
just a first-party implementation of that same contract; nothing about the
protocol is Lua-specific.

## Why you'd write your own controller

- You want to write playout logic in a language other than Lua.
- You want to embed control of a station inside an existing service (a bot,
  a web dashboard, a game server plugin) rather than running a separate
  `radio station` process.
- You're building tooling (an admin CLI, a monitoring dashboard) that just
  needs to observe or drive stations without owning their playout logic.

## The schema is on a registry, not just in this repo

The `.proto` files are published to a Buf Schema Registry at
`proto.prod.wtf/goradioserver/goradio`. You can generate a client for your
language directly from the registry without cloning this repository — see
[Writing a Controller](writing-a-controller.md) for the exact `buf`
commands.

## Read next

- **[Protocol Reference](protocol-reference.md)** — every RPC, every
  message field, the auth model.
- **[Writing a Controller](writing-a-controller.md)** — the lifecycle: what
  order to call things in, how to handle reconnects, worked example.
- **[HTTP + JSON API](http-json-api.md)** — every RPC over plain HTTP/1.1
  with a JSON body, for clients that can't or shouldn't depend on a gRPC
  library.
- **[Now Playing HTTP API](now-playing-http-api.md)** — a plain public JSON
  endpoint mirroring `GetStatus`, for a web embed or a bot that doesn't
  want to speak gRPC at all.
