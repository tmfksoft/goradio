-- example.lua: a minimal reference station script, just enough to prove
-- the audio server <-> Lua engine protocol end-to-end. It is NOT a full
-- station implementation (no songs/idents/callers/adverts) -- that's
-- deliberately left for you to build in Lua on top of these primitives.
--
-- radio.args carries any CLI args after --config/--script, e.g. invoking
-- `radio station --script example.lua myfm "My FM"` makes
-- radio.args = {"myfm", "My FM"}, letting one shared script serve many
-- stations depending on how it's invoked.

local slug = radio.args[1] or "test-station"
local name = radio.args[2] or "Test Station"

local info = radio.register(slug, name, "Reference controller for protocol testing")
print(string.format("registered slug=%s stream_url=%s re_registered=%s",
  info.slug, info.stream_url, tostring(info.re_registered)))

-- Local test clips are resolved relative to the audio server's audio_root.
local test_clips = { "tone_a.mp3", "tone_b.mp3", "tone_long.mp3" }

radio.on_track_started(function(track)
  print(string.format("[event] track started: %s (%s)", track.location, track.queue_id))
end)

radio.on_track_ended(function(track)
  print(string.format("[event] track ended: %s (%s)", track.queue_id, track.reason))
end)

radio.on_error(function(err)
  print(string.format("[event] error: %s (%s)", err.message, err.code))
end)

math.randomseed(os.time())

radio.every(30, function()
  local clip = test_clips[math.random(#test_clips)]
  local result = radio.queue(clip, "APPEND")
  print(string.format("queued %s -> queue_id=%s position=%s", clip, result.queue_id, result.queue_position))
end)

radio.every(60, function()
  local status = radio.status()
  print(string.format("status: silence=%s listeners=%d queue_length=%d uptime=%ds",
    tostring(status.is_silence), status.listener_count, status.queue_length, status.uptime_seconds))
end)
