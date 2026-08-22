# Now Playing HTTP API

`GET /stations/{slug}/now-playing` is a plain JSON snapshot of what
[`GetStatus`](protocol-reference.md#getstatus) already returns over
gRPC — for consumers where standing up a gRPC(-web) client isn't worth
it: pairing a plain HTML/JS radio player with a progress bar, a Discord
bot, a dashboard. It's not a separate data source — it reads the exact
same live station state gRPC does.

## Public by default

Like `/stream/{slug}` and `/stations`, this route needs **no
authentication** to call. Title, artist, cover art, duration, elapsed
time, pause state, logo, metadata, and listener count carry no more
information than you could already get by listening to the (already
public, unauthenticated) audio stream itself, or are just descriptive
station-level info the operator set to be shown — so there's nothing
gained by gating any of that behind a token.

```sh
curl https://your-server/stations/myfm/now-playing
```

```json
{
  "slug": "myfm",
  "name": "My FM",
  "is_silence": false,
  "is_paused": false,
  "logo_url": "https://example.com/myfm.png",
  "metadata": {"group": "top-40"},
  "current_track": {
    "title": "Test Song",
    "artist": "Test Artist",
    "cover_art_url": "https://example.com/test-song.jpg",
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
`0`, rather than dividing by zero. `is_paused` and
`current_track_elapsed_seconds` both already account for
[`Pause`](protocol-reference.md#pause-resume)/[`Seek`](protocol-reference.md#seek-seekby)
correctly — elapsed freezes while paused rather than just tracking
wall-clock time. `logo_url` and `metadata` are omitted entirely (not just
empty) when the station never set them.

## Authenticated: raw locations, and history

Presenting a valid bearer token (any token authorized for the slug,
**including a read-only one** — this endpoint never mutates anything)
unlocks two things:

- `queue_id`, the raw `location`, and `mode` on every `current_track`/`queue` item.
- `history` at all — omitted entirely from the public response, not just
  stripped down, since a full recently-played log reveals more about a
  station's playout pattern over time than a single current/pending
  snapshot does.

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
    "cover_art_url": "https://example.com/test-song.jpg",
    "mode": "QUEUE_MODE_APPEND",
    "duration_seconds": 210
  },
  "history": [
    {
      "queue_id": "a1b2c3d4-...",
      "location": "jingle.mp3",
      "title": "Station ID",
      "reason": "completed",
      "ended_at_unix_ms": 1700000000000,
      "duration_seconds": 8
    }
  ],
  ...
}
```

These are held back from the public response deliberately: `location` can
be a raw filesystem path under `audio.audio_root`, or an upstream URL for
a live relay — which might itself embed something like a query-string
auth token. None of that belongs in a response anyone who knows a
station's slug can read, and neither does a play-history log.

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
