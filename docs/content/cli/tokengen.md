# `radio tokengen`

Mints a JWT authorizing one or more station slugs, for use as a station
controller's `auth.jwt`.

```sh
radio tokengen [-secret SECRET] [-subject SUBJECT] [-ttl 24h] <slug...>
```

| Flag | Default | Description |
|---|---|---|
| `-secret` | *(required)* | HS256 signing secret — **must match** the audio server's `auth.jwt_secret` |
| `-subject` | `tokengen` | The JWT `sub` claim (freeform, useful for identifying which controller/deployment a token belongs to) |
| `-ttl` | `24h` | Token lifetime, as a Go duration string (`1h`, `720h`, ...) |

The token is printed to stdout. Everything else is positional: one or more
station slugs the token should authorize.

```sh
radio tokengen -secret s3cret -subject myfm-prod -ttl 720h myfm
radio tokengen -secret s3cret myfm otherfm thirdfm   # one token, three stations
```

## What's in the token

A `slugs` claim listing every slug you passed, checked by the audio server
on every gRPC call against the slug the call actually targets — a token for
`myfm` can't be used to call `QueueTrack` on `otherfm`, even if both stations
exist on the same server. See [Protocol Reference — Auth](../developer-api/protocol-reference.md#authentication)
for the exact claim shape if you're minting tokens from another language
instead.
