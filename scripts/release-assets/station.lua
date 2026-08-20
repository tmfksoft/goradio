-- station.lua: a minimal example GoRadio station script.
--
-- Run it with:
--   radio station --config station.yaml --script station.lua
--
-- Add some audio and edit the `playlist` table below with paths relative
-- to the audio server's configured audio.audio_root (or http(s):// URLs),
-- then this will periodically queue a random pick. Until you do, the
-- station just plays silence -- that's expected, not an error.
--
-- Full Lua API docs: https://tmfksoft.github.io/goradio/lua-api/

local slug = radio.args[1] or "myfm"
local name = radio.args[2] or "My FM"

local info = radio.register(slug, name, "A GoRadio station")
print(string.format("registered '%s' -> %s", info.slug, info.stream_url))

local playlist = {
  -- "jingle.mp3",
  -- "songs/track1.mp3",
  -- "https://example.com/track2.mp3",
}

radio.on_track_started(function(track)
  print(string.format("now playing: %s", track.location))
end)

radio.on_error(function(err)
  print(string.format("error: %s (%s)", err.message, err.code))
end)

if #playlist == 0 then
  print("playlist is empty -- edit station.lua and add some tracks; playing silence until then")
else
  math.randomseed(os.time())
  radio.every(180, function()
    local pick = playlist[math.random(#playlist)]
    radio.queue(pick, "APPEND")
  end)
end
