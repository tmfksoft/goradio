# Developer / Protocol API — Overview

The audio server exposes one gRPC service, `AudioServerService`, defined in
`proto/audioserver/v1`. A station controller doesn't have to be the Lua
engine bundled in this repo — anything that can make gRPC calls and hold a
JWT can be a controller. `internal/luastation` (this repo's Lua engine) is
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
`proto.prod.wtf/tmfksoft/goradio`. You can generate a client for your
language directly from the registry without cloning this repository — see
[Writing a Controller](writing-a-controller.md) for the exact `buf`
commands.

## Read next

- **[Protocol Reference](protocol-reference.md)** — every RPC, every
  message field, the auth model.
- **[Writing a Controller](writing-a-controller.md)** — the lifecycle: what
  order to call things in, how to handle reconnects, worked example.
