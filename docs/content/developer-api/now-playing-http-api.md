# Now Playing HTTP API

`GET /stations/{slug}/now-playing` is a plain JSON snapshot of what
[`GetStatus`](protocol-reference.md#getstatus) already returns over
gRPC — for consumers where standing up a gRPC(-web) client isn't worth
it: pairing a plain HTML/JS radio player with a progress bar, a Discord
bot, a dashboard. It's not a separate data source — it reads the exact
same live station state gRPC does.

## Public by default

Like `/stream/{slug}` and `/stations`, this route needs **no
authentication** to call. Title, artist, duration, elapsed time, and
listener count carry no more information than you could already get by
listening to the (already public, unauthenticated) audio stream itself —
so there's nothing gained by gating that behind a token.

```sh
curl https://your-server/stations/myfm/now-playing
```

```json
{
  "slug": "myfm",
  "name": "My FM",
  "is_silence": false,
  "current_track": {
    "title": "Test Song",
    "artist": "Test Artist",
    "duration_seconds": 210
  },
  "current_track_elapsed_seconds": 42,
  "listener_count": 12,
  "queue": [
    {"title": "Next Up", "artist": "...", "duration_seconds": 180}
  ]
}
```

`duration_seconds` is `0` for a live relay (no fixed length) or a
just-queued item whose prefetch hasn't finished yet — render an
indefinite/pulsing progress bar instead of a fixed-length one when it's
`0`, rather than dividing by zero.

## Authenticated: raw locations too

Presenting a valid bearer token (any token authorized for the slug,
**including a read-only one** — this endpoint never mutates anything)
unlocks `queue_id`, the raw `location`, and `mode` on every track:

```sh
curl -H "Authorization: Bearer $TOKEN" https://your-server/stations/myfm/now-playing
```

```json
{
  "current_track": {
    "queue_id": "b4d5a738-...",
    "location": "song.mp3",
    "title": "Test Song",
    "artist": "Test Artist",
    "mode": "QUEUE_MODE_APPEND",
    "duration_seconds": 210
  },
  ...
}
```

These fields are held back from the public response deliberately:
`location` can be a raw filesystem path under `audio.audio_root`, or an
upstream URL for a live relay — which might itself embed something like a
query-string auth token. None of that belongs in a response anyone who
knows a station's slug can read.

An `Authorization` header that's present but invalid (bad token, wrong
signature, expired) gets a hard `401` — it's never silently downgraded to
the public view, so a typo'd token fails loudly rather than quietly
serving less data than expected. A valid token for the *wrong* slug gets
`403`.

## Pairing with an HTML player

A minimal "now playing" widget next to an `<audio>` tag streaming
`/stream/{slug}` needs nothing but a `fetch()` loop — no auth setup, no
proxy:

```html
<audio src="https://your-server/stream/myfm" controls></audio>
<div id="now-playing"></div>
<script>
async function refresh() {
  const r = await fetch("https://your-server/stations/myfm/now-playing");
  const data = await r.json();
  const el = document.getElementById("now-playing");
  if (data.is_silence) {
    el.textContent = "Off air";
  } else {
    el.textContent = `${data.current_track.artist} — ${data.current_track.title}`;
  }
}
refresh();
setInterval(refresh, 5000);
</script>
```

There's no push/SSE variant yet — this is poll-only. If you need
real-time updates without polling, [`SubscribeEvents`](protocol-reference.md#subscribeevents)
over gRPC already gives you that; a small backend bridging it to a
WebSocket for browsers is the natural next step if that becomes worth
building.
