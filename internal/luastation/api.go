package luastation

import (
	"context"
	"fmt"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
)

// setupLuaEnvironment installs the `radio` global table (deliberately
// minimal this phase: register/queue/status/scheduling/event callbacks —
// richer, genre-specific queue helpers are future work, built in Lua on
// top of these primitives) plus the http and sql modules.
func (e *Engine) setupLuaEnvironment() {
	L := e.L

	radioTable := L.NewTable()

	argsTable := L.NewTable()
	for _, a := range e.scriptArgs {
		argsTable.Append(lua.LString(a))
	}
	radioTable.RawSetString("args", argsTable)

	L.SetFuncs(radioTable, map[string]lua.LGFunction{
		"register":         e.luaRegister,
		"queue":            e.luaQueue,
		"status":           e.luaStatus,
		"every":            e.luaEvery,
		"after":            e.luaAfter,
		"on_track_started": e.luaOnTrackStarted,
		"on_track_ended":   e.luaOnTrackEnded,
		"on_error":         e.luaOnError,
	})

	L.SetGlobal("radio", radioTable)

	RegisterHTTPModule(L)
	RegisterSQLModule(L)
}

// radio.register(slug, name, description) -> {slug, stream_url, re_registered}
func (e *Engine) luaRegister(L *lua.LState) int {
	slug := L.CheckString(1)
	name := L.OptString(2, slug)
	description := L.OptString(3, "")

	resp, err := e.registerWithRetry(context.Background(), slug, name, description)
	if err != nil {
		L.RaiseError("radio.register failed: %v", err)
		return 0
	}
	e.setRegisterInfo(slug, name, description)

	tbl := L.NewTable()
	tbl.RawSetString("slug", lua.LString(resp.GetSlug()))
	tbl.RawSetString("stream_url", lua.LString(resp.GetStreamUrl()))
	tbl.RawSetString("re_registered", lua.LBool(resp.GetReRegistered()))
	L.Push(tbl)
	return 1
}

// radio.queue(source, mode) -> {queue_id, queue_position, status}
//
// source is either a plain string (local path, or an http(s):// URL) or a
// table {type="local"|"url", location=..., title=..., artist=...}. mode is
// one of "APPEND" (default), "PLAY_NEXT", "PLAY_NOW_INTERRUPT".
func (e *Engine) luaQueue(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.queue called before radio.register")
		return 0
	}

	source, err := parseTrackSource(L.CheckAny(1))
	if err != nil {
		L.RaiseError("radio.queue: %v", err)
		return 0
	}
	mode := parseQueueMode(L.OptString(2, "APPEND"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := e.client.QueueTrack(ctx, &audioserverv1.QueueTrackRequest{
		Slug:   slug,
		Source: source,
		Mode:   mode,
	})
	if err != nil {
		L.RaiseError("radio.queue failed: %v", err)
		return 0
	}

	tbl := L.NewTable()
	tbl.RawSetString("queue_id", lua.LString(resp.GetQueueId()))
	tbl.RawSetString("queue_position", lua.LNumber(resp.GetQueuePosition()))
	tbl.RawSetString("status", lua.LString(resp.GetStatus()))
	L.Push(tbl)
	return 1
}

// radio.status() -> table snapshot of GetStatus
func (e *Engine) luaStatus(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.status called before radio.register")
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := e.client.GetStatus(ctx, &audioserverv1.GetStatusRequest{Slug: slug})
	if err != nil {
		L.RaiseError("radio.status failed: %v", err)
		return 0
	}

	tbl := L.NewTable()
	tbl.RawSetString("slug", lua.LString(resp.GetSlug()))
	tbl.RawSetString("name", lua.LString(resp.GetName()))
	tbl.RawSetString("is_registered", lua.LBool(resp.GetIsRegistered()))
	tbl.RawSetString("is_silence", lua.LBool(resp.GetIsSilence()))
	tbl.RawSetString("listener_count", lua.LNumber(resp.GetListenerCount()))
	tbl.RawSetString("uptime_seconds", lua.LNumber(resp.GetUptimeSeconds()))
	tbl.RawSetString("queue_length", lua.LNumber(len(resp.GetQueue())))
	L.Push(tbl)
	return 1
}

// radio.every(seconds, fn): calls fn repeatedly, every `seconds`.
func (e *Engine) luaEvery(L *lua.LState) int {
	seconds := float64(L.CheckNumber(1))
	fn := L.CheckFunction(2)
	interval := time.Duration(seconds * float64(time.Second))
	e.timers = append(e.timers, &timerEntry{interval: interval, next: time.Now().Add(interval), fn: fn})
	return 0
}

// radio.after(seconds, fn): calls fn once, after `seconds`.
func (e *Engine) luaAfter(L *lua.LState) int {
	seconds := float64(L.CheckNumber(1))
	fn := L.CheckFunction(2)
	e.timers = append(e.timers, &timerEntry{next: time.Now().Add(time.Duration(seconds * float64(time.Second))), fn: fn})
	return 0
}

func (e *Engine) luaOnTrackStarted(L *lua.LState) int {
	e.onTrackStarted = L.CheckFunction(1)
	return 0
}

func (e *Engine) luaOnTrackEnded(L *lua.LState) int {
	e.onTrackEnded = L.CheckFunction(1)
	return 0
}

func (e *Engine) luaOnError(L *lua.LState) int {
	e.onError = L.CheckFunction(1)
	return 0
}

func parseTrackSource(v lua.LValue) (*audioserverv1.TrackSource, error) {
	switch val := v.(type) {
	case lua.LString:
		return &audioserverv1.TrackSource{Type: inferSourceType(string(val)), Location: string(val)}, nil

	case *lua.LTable:
		location := lua.LVAsString(val.RawGetString("location"))
		if location == "" {
			return nil, fmt.Errorf("source table missing 'location'")
		}

		typ := inferSourceType(location)
		switch strings.ToLower(lua.LVAsString(val.RawGetString("type"))) {
		case "url", "http", "http_url":
			typ = audioserverv1.TrackSourceType_TRACK_SOURCE_TYPE_HTTP_URL
		case "local", "local_file":
			typ = audioserverv1.TrackSourceType_TRACK_SOURCE_TYPE_LOCAL_FILE
		}

		return &audioserverv1.TrackSource{
			Type:          typ,
			Location:      location,
			DisplayTitle:  lua.LVAsString(val.RawGetString("title")),
			DisplayArtist: lua.LVAsString(val.RawGetString("artist")),
		}, nil

	default:
		return nil, fmt.Errorf("source must be a string or table, got %s", v.Type().String())
	}
}

func inferSourceType(location string) audioserverv1.TrackSourceType {
	if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
		return audioserverv1.TrackSourceType_TRACK_SOURCE_TYPE_HTTP_URL
	}
	return audioserverv1.TrackSourceType_TRACK_SOURCE_TYPE_LOCAL_FILE
}

func parseQueueMode(s string) audioserverv1.QueueMode {
	switch strings.ToUpper(s) {
	case "PLAY_NEXT":
		return audioserverv1.QueueMode_QUEUE_MODE_PLAY_NEXT
	case "PLAY_NOW_INTERRUPT", "INTERRUPT":
		return audioserverv1.QueueMode_QUEUE_MODE_PLAY_NOW_INTERRUPT
	default:
		return audioserverv1.QueueMode_QUEUE_MODE_APPEND
	}
}
