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
		"dequeue":          e.luaDequeue,
		"clear_queue":      e.luaClearQueue,
		"skip":             e.luaSkip,
		"status":           e.luaStatus,
		"every":            e.luaEvery,
		"after":            e.luaAfter,
		"on_track_started": e.luaOnTrackStarted,
		"on_track_ended":   e.luaOnTrackEnded,
		"on_error":         e.luaOnError,
		"on_queue_low":     e.luaOnQueueLow,
	})

	L.SetGlobal("radio", radioTable)

	e.RegisterHTTPModule(L)
	e.RegisterSQLModule(L)
	e.RegisterRedisModule(L)
}

// radio.register(slug, name, description [, options]) -> {slug, stream_url, re_registered}
//
// options is an optional table; currently the only recognized field is
// low_queue_threshold (number): if set > 0, the audio server fires
// EVENT_TYPE_QUEUE_LOW (see radio.on_queue_low) once, edge-triggered,
// whenever the pending queue length drops to or below it.
func (e *Engine) luaRegister(L *lua.LState) int {
	slug := L.CheckString(1)
	name := L.OptString(2, slug)
	description := L.OptString(3, "")

	var lowQueueThreshold int32
	if L.GetTop() >= 4 {
		if opts, ok := L.Get(4).(*lua.LTable); ok {
			if n, ok := opts.RawGetString("low_queue_threshold").(lua.LNumber); ok {
				lowQueueThreshold = int32(n)
			}
		}
	}

	resp, err := e.registerWithRetry(e.ctx, slug, name, description, lowQueueThreshold)
	if err != nil {
		L.RaiseError("radio.register failed: %v", err)
		return 0
	}
	e.setRegisterInfo(slug, name, description, lowQueueThreshold)

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

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
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

// radio.dequeue(queue_id) -> removed (bool)
//
// Removes one still-pending item. Returns false (not an error) if
// queue_id wasn't found -- it may already have started playing, already
// been removed, or never existed. Cannot remove whatever is currently
// playing; use radio.queue(source, "PLAY_NOW_INTERRUPT") for that.
func (e *Engine) luaDequeue(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.dequeue called before radio.register")
		return 0
	}
	queueID := L.CheckString(1)

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.RemoveFromQueue(ctx, &audioserverv1.RemoveFromQueueRequest{Slug: slug, QueueId: queueID})
	if err != nil {
		L.RaiseError("radio.dequeue failed: %v", err)
		return 0
	}

	L.Push(lua.LBool(resp.GetRemoved()))
	return 1
}

// radio.clear_queue([stop_current]) -> removed_count, stopped_current
//
// Removes every pending item. By default (stop_current omitted or false)
// this does not touch whatever is currently playing. Pass stop_current =
// true to also interrupt it -- since the queue is now empty, playback
// falls back to silence rather than jumping to some other track. This is
// the usual "clean restart" pattern: call radio.clear_queue(true) when
// your script starts, so a stale queue/track from before a restart
// doesn't keep playing underneath your fresh state.
func (e *Engine) luaClearQueue(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.clear_queue called before radio.register")
		return 0
	}
	stopCurrent := L.OptBool(1, false)

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.ClearQueue(ctx, &audioserverv1.ClearQueueRequest{Slug: slug, StopCurrent: stopCurrent})
	if err != nil {
		L.RaiseError("radio.clear_queue failed: %v", err)
		return 0
	}

	L.Push(lua.LNumber(resp.GetRemovedCount()))
	L.Push(lua.LBool(resp.GetStoppedCurrent()))
	return 2
}

// radio.skip() -> skipped (bool)
//
// Interrupts whatever is currently playing, leaving the rest of the queue
// untouched -- playback immediately moves on to the next pending item (or
// silence if the queue is empty). Returns false if nothing was playing to
// skip. This is the only way to end a "track" with no natural end, such
// as a queued live stream (see radio.queue) -- it plays until skipped or
// interrupted.
func (e *Engine) luaSkip(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.skip called before radio.register")
		return 0
	}

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.Skip(ctx, &audioserverv1.SkipRequest{Slug: slug})
	if err != nil {
		L.RaiseError("radio.skip failed: %v", err)
		return 0
	}

	L.Push(lua.LBool(resp.GetSkipped()))
	return 1
}

// radio.status() -> table snapshot of GetStatus
func (e *Engine) luaStatus(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.status called before radio.register")
		return 0
	}

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
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

	if cur := resp.GetCurrentTrack(); cur != nil {
		tbl.RawSetString("current_track", queuedItemToLua(L, cur))
	}

	queueTbl := L.NewTable()
	for _, item := range resp.GetQueue() {
		queueTbl.Append(queuedItemToLua(L, item))
	}
	tbl.RawSetString("queue", queueTbl)

	L.Push(tbl)
	return 1
}

func queuedItemToLua(L *lua.LState, item *audioserverv1.QueuedItemStatus) *lua.LTable {
	tbl := L.NewTable()
	tbl.RawSetString("queue_id", lua.LString(item.GetQueueId()))
	tbl.RawSetString("location", lua.LString(item.GetSource().GetLocation()))
	tbl.RawSetString("title", lua.LString(item.GetSource().GetDisplayTitle()))
	tbl.RawSetString("artist", lua.LString(item.GetSource().GetDisplayArtist()))
	tbl.RawSetString("mode", lua.LString(item.GetMode().String()))
	return tbl
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

// radio.on_queue_low(fn): fn is called (once, edge-triggered) whenever the
// pending queue length drops to or below the low_queue_threshold given to
// radio.register. No-op unless that threshold was set > 0.
func (e *Engine) luaOnQueueLow(L *lua.LState) int {
	e.onQueueLow = L.CheckFunction(1)
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
