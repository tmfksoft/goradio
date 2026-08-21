# `radio tokengen`

Mints a JWT authorizing one or more station slugs, for use as a station
controller's `auth.jwt`.

```sh
radio tokengen [-secret SECRET] [-subject SUBJECT] [-ttl 24h] [-readonly] <slug...>
```

| Flag | Default | Description |
|---|---|---|
| `-secret` | *(required)* | HS256 signing secret — **must match** the audio server's `auth.jwt_secret` |
| `-subject` | `tokengen` | The JWT `sub` claim (freeform, useful for identifying which controller/deployment a token belongs to) |
| `-ttl` | `24h` | Token lifetime, as a Go duration string (`1h`, `720h`, ...) |
| `-readonly` | `false` | Mint a read-only token — see below |

The token is printed to stdout. Everything else is positional: one or more
station slugs the token should authorize.

```sh
radio tokengen -secret s3cret -subject myfm-prod -ttl 720h myfm
radio tokengen -secret s3cret myfm otherfm thirdfm   # one token, three stations
radio tokengen -secret s3cret -readonly myfm         # observer-only, can't queue/skip/etc.
radio tokengen -secret s3cret "*"                    # every station — e.g. for a management dashboard
```

A slug argument may be a glob pattern (`*`, `test-*`, ...) instead of an
exact slug — see [Protocol Reference — Auth](../developer-api/protocol-reference.md#authentication)
for matching details.

## What's in the token

A `slugs` claim listing every slug you passed, checked by the audio server
on every gRPC call against the slug the call actually targets — a token for
`myfm` can't be used to call `QueueTrack` on `otherfm`, even if both stations
exist on the same server. See [Protocol Reference — Auth](../developer-api/protocol-reference.md#authentication)
for the exact claim shape if you're minting tokens from another language
instead.

## `-readonly`

A read-only token can still call `GetStatus` and `SubscribeEvents` (and
the [now-playing HTTP endpoint](../developer-api/now-playing-http-api.md),
though that one doesn't need a token at all for its public fields) — every
write RPC (`RegisterStation`, `UnregisterStation`, `QueueTrack`,
`RemoveFromQueue`, `ClearQueue`, `Skip`, `SkipTo`) gets `PermissionDenied`. Use this for anything that
should only ever observe a station — a dashboard, a bot, a second
controller instance that watches but never queues — without trusting it
not to also call `QueueTrack` by mistake or by design.

!!! warning "Not for `radio station`"
    `radio station`'s Lua engine always calls `radio.register()` on
    startup — that's `RegisterStation`, a write RPC — so a read-only
    token makes it fail immediately (and it won't retry, since
    `PermissionDenied` is treated as a permanent failure; see
    [Writing a Controller](../developer-api/writing-a-controller.md#reconnecting)).
    Read-only tokens are for a custom observer calling `GetStatus`/
    `SubscribeEvents` directly, not for running a Lua station script.
