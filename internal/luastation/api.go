package luastation

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	audioserverv1 "github.com/goradioserver/goradio/gen/go/audioserver/v1"
)

// setupLuaEnvironment installs the `radio` global table (deliberately
// minimal this phase: register/queue/status/scheduling/event callbacks —
// richer, genre-specific queue helpers are future work, built in Lua on
// top of these primitives) plus the http/sql/redis/json/yaml modules.
//
// gopher-lua opens the full Lua 5.1 stdlib by default (io, os, require,
// coroutines, ...) -- nothing here restricts that, matching the "full
// trusted access" decision already made for http/sql/redis: station
// authors are trusted operators, not sandboxed third parties. That
// includes os.execute/io.popen (arbitrary shell access) -- see the docs.
func (e *Engine) setupLuaEnvironment() {
	L := e.L

	e.setupModuleSearchPath()

	radioTable := L.NewTable()

	argsTable := L.NewTable()
	for _, a := range e.scriptArgs {
		argsTable.Append(lua.LString(a))
	}
	radioTable.RawSetString("args", argsTable)

	L.SetFuncs(radioTable, map[string]lua.LGFunction{
		"register":         e.luaRegister,
		"unregister":       e.luaUnregister,
		"queue":            e.luaQueue,
		"dequeue":          e.luaDequeue,
		"clear_queue":      e.luaClearQueue,
		"skip":             e.luaSkip,
		"skip_to":          e.luaSkipTo,
		"pause":            e.luaPause,
		"resume":           e.luaResume,
		"seek":             e.luaSeek,
		"seek_by":          e.luaSeekBy,
		"status":           e.luaStatus,
		"list_stations":    e.luaListStations,
		"list_directory":   e.luaListDirectory,
		"server_info":      e.luaServerInfo,
		"every":            e.luaEvery,
		"after":            e.luaAfter,
		"on_track_started": e.luaOnTrackStarted,
		"on_track_ended":   e.luaOnTrackEnded,
		"on_error":         e.luaOnError,
		"on_queue_low":     e.luaOnQueueLow,
		"on_register":      e.luaOnRegister,
	})

	L.SetGlobal("radio", radioTable)

	e.RegisterHTTPModule(L)
	e.RegisterSQLModule(L)
	e.RegisterRedisModule(L)
	RegisterJSONModule(L)
	RegisterYAMLModule(L)
}

// setupModuleSearchPath prepends the running script's own directory to
// package.path, so require("mymodule") finds mymodule.lua next to the
// script regardless of the process's working directory -- gopher-lua's
// default ("./?.lua;...") is relative to the cwd, which would silently
// break depending on where `radio station` happens to be launched from.
// The stdlib defaults stay in the path too, just after it.
func (e *Engine) setupModuleSearchPath() {
	pkg, ok := e.L.GetGlobal("package").(*lua.LTable)
	if !ok {
		return
	}
	scriptDir := filepath.Dir(e.scriptPath)
	existing := lua.LVAsString(pkg.RawGetString("path"))
	pkg.RawSetString("path", lua.LString(fmt.Sprintf("%s/?.lua;%s/?/init.lua;%s", scriptDir, scriptDir, existing)))
}

// radio.register(slug, name, description [, options]) -> {slug, stream_url, re_registered}
//
// options is an optional table recognizing three fields:
//   - low_queue_threshold (number) -- if set > 0, the audio server fires
//     EVENT_TYPE_QUEUE_LOW (see radio.on_queue_low) once, edge-triggered,
//     whenever the pending queue length drops to or below it.
//   - logo_url (string) -- an optional station logo/artwork URL surfaced
//     via radio.status()/radio.list_stations().
//   - metadata (table<string, string>) -- freeform key/value data (e.g. a
//     group name to cluster stations in a dashboard). The audio server
//     never interprets these keys itself; it just stores and returns
//     them via radio.status()/radio.list_stations() for the
//     controller/player to use however it wants. Non-string keys/values
//     are silently dropped.
//
// Every field (including options) is fully replaced on each call, not
// merged: to update just the logo (or metadata) on the fly, re-register
// with the same slug/name/description and the new options, same as you
// would to change the name or description -- omitting options.metadata
// on a later call clears any previously set metadata.
func (e *Engine) luaRegister(L *lua.LState) int {
	slug := L.CheckString(1)
	name := L.OptString(2, slug)
	description := L.OptString(3, "")

	var lowQueueThreshold int32
	var logoURL string
	var metadata map[string]string
	if L.GetTop() >= 4 {
		if opts, ok := L.Get(4).(*lua.LTable); ok {
			if n, ok := opts.RawGetString("low_queue_threshold").(lua.LNumber); ok {
				lowQueueThreshold = int32(n)
			}
			logoURL = lua.LVAsString(opts.RawGetString("logo_url"))
			if md, ok := opts.RawGetString("metadata").(*lua.LTable); ok {
				metadata = luaTableToStringMap(md)
			}
		}
	}

	resp, err := e.registerWithRetry(e.ctx, slug, name, description, logoURL, metadata, lowQueueThreshold)
	if err != nil {
		L.RaiseError("radio.register failed: %v", err)
		return 0
	}
	e.setRegisterInfo(slug, name, description, logoURL, metadata, lowQueueThreshold)

	tbl := L.NewTable()
	tbl.RawSetString("slug", lua.LString(resp.GetSlug()))
	tbl.RawSetString("stream_url", lua.LString(resp.GetStreamUrl()))
	tbl.RawSetString("re_registered", lua.LBool(resp.GetReRegistered()))
	L.Push(tbl)
	return 1
}

// radio.unregister() removes this station from the audio server: stops its
// player, and disconnects any listeners/SubscribeEvents streams on it. It
// does not persist anywhere -- a later radio.register() call starts a
// fresh station with an empty queue, not a resumed one. After this call,
// every other radio.* function that requires registration (radio.queue,
// radio.status, etc.) raises an error until radio.register() is called
// again; the engine's own event-stream reconnect loop also won't try to
// auto-re-register in the meantime.
func (e *Engine) luaUnregister(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.unregister called before radio.register")
		return 0
	}

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	if _, err := e.client.UnregisterStation(ctx, &audioserverv1.UnregisterStationRequest{Slug: slug}); err != nil {
		L.RaiseError("radio.unregister failed: %v", err)
		return 0
	}
	e.setUnregistered()

	return 0
}

// radio.queue(source, mode) -> {queue_id, queue_position, status}
//
// source is either a plain string (local path, or an http(s):// URL) or a
// table {type="local"|"url", location=..., title=..., artist=..., cover_art=...}
// -- title/artist/cover_art are all optional, purely descriptive metadata
// carried through unchanged to radio.status()/radio.on_track_started/
// radio.status().history, never fetched or validated by the audio server.
// mode is one of "APPEND" (default), "PLAY_NEXT", "PLAY_NOW_INTERRUPT".
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

// radio.skip_to(queue_id) -> removed_count, interrupted_current
//
// Jumps playback straight to a specific pending item, by queue_id (use
// queue_id, not a position -- your own view of positions can be stale by
// the time the call lands). Every item ahead of it in the queue is
// dropped (removed_count), and whatever's currently playing is
// interrupted (interrupted_current) so the target item starts
// immediately. Raises an error if queue_id isn't a pending item -- in
// particular you can't skip_to whatever's already playing, since it's
// left the queue by then; use radio.skip() to interrupt the current track
// without changing what plays after it.
func (e *Engine) luaSkipTo(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.skip_to called before radio.register")
		return 0
	}
	queueID := L.CheckString(1)

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.SkipTo(ctx, &audioserverv1.SkipToRequest{Slug: slug, QueueId: queueID})
	if err != nil {
		L.RaiseError("radio.skip_to failed: %v", err)
		return 0
	}

	L.Push(lua.LNumber(resp.GetRemovedCount()))
	L.Push(lua.LBool(resp.GetInterruptedCurrent()))
	return 2
}

// radio.pause() -> paused (bool)
//
// Pauses the current track in place -- the station falls back to the
// silence loop, same as an empty queue, until radio.resume(). Returns
// false (not an error) if nothing is playing, the current track is a
// live stream (no fixed position to hold -- see radio.queue's note on
// live streams), or it's already paused.
func (e *Engine) luaPause(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.pause called before radio.register")
		return 0
	}

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.Pause(ctx, &audioserverv1.PauseRequest{Slug: slug})
	if err != nil {
		L.RaiseError("radio.pause failed: %v", err)
		return 0
	}

	L.Push(lua.LBool(resp.GetPaused()))
	return 1
}

// radio.resume() -> resumed (bool)
//
// Resumes a paused track from exactly where it was paused. Returns false
// if the station wasn't paused.
func (e *Engine) luaResume(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.resume called before radio.register")
		return 0
	}

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.Resume(ctx, &audioserverv1.ResumeRequest{Slug: slug})
	if err != nil {
		L.RaiseError("radio.resume failed: %v", err)
		return 0
	}

	L.Push(lua.LBool(resp.GetResumed()))
	return 1
}

// radio.seek(position_seconds) -> seeked (bool), position_seconds (number)
//
// Jumps the current track to an absolute position, clamped to
// [0, duration]. Works whether or not the station is currently paused --
// seeking while paused just moves where radio.resume() will pick up.
// Returns seeked = false (not an error) if nothing seekable is playing --
// no current track, or it's a live stream (no fixed position to seek
// within).
func (e *Engine) luaSeek(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.seek called before radio.register")
		return 0
	}
	positionSeconds := int64(L.CheckNumber(1))

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.Seek(ctx, &audioserverv1.SeekRequest{Slug: slug, PositionSeconds: positionSeconds})
	if err != nil {
		L.RaiseError("radio.seek failed: %v", err)
		return 0
	}

	L.Push(lua.LBool(resp.GetSeeked()))
	L.Push(lua.LNumber(resp.GetPositionSeconds()))
	return 2
}

// radio.seek_by(delta_seconds) -> seeked (bool), position_seconds (number)
//
// Jumps the current track by a signed delta from its current position
// (positive = forward, negative = backward), clamped to [0, duration].
// See radio.seek.
func (e *Engine) luaSeekBy(L *lua.LState) int {
	slug := e.getRegisteredSlug()
	if slug == "" {
		L.RaiseError("radio.seek_by called before radio.register")
		return 0
	}
	deltaSeconds := int64(L.CheckNumber(1))

	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.SeekBy(ctx, &audioserverv1.SeekByRequest{Slug: slug, DeltaSeconds: deltaSeconds})
	if err != nil {
		L.RaiseError("radio.seek_by failed: %v", err)
		return 0
	}

	L.Push(lua.LBool(resp.GetSeeked()))
	L.Push(lua.LNumber(resp.GetPositionSeconds()))
	return 2
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
	tbl.RawSetString("is_paused", lua.LBool(resp.GetIsPaused()))
	tbl.RawSetString("listener_count", lua.LNumber(resp.GetListenerCount()))
	tbl.RawSetString("uptime_seconds", lua.LNumber(resp.GetUptimeSeconds()))
	tbl.RawSetString("queue_length", lua.LNumber(len(resp.GetQueue())))
	tbl.RawSetString("logo_url", lua.LString(resp.GetLogoUrl()))
	tbl.RawSetString("metadata", stringMapToLuaTable(L, resp.GetMetadata()))

	if cur := resp.GetCurrentTrack(); cur != nil {
		tbl.RawSetString("current_track", queuedItemToLua(L, cur))
		tbl.RawSetString("elapsed_seconds", lua.LNumber(resp.GetCurrentTrackElapsedSeconds()))
	}

	queueTbl := L.NewTable()
	for _, item := range resp.GetQueue() {
		queueTbl.Append(queuedItemToLua(L, item))
	}
	tbl.RawSetString("queue", queueTbl)

	historyTbl := L.NewTable()
	for _, h := range resp.GetHistory() {
		historyTbl.Append(historyEntryToLua(L, h))
	}
	tbl.RawSetString("history", historyTbl)

	L.Push(tbl)
	return 1
}

// radio.list_stations() -> array of {slug, name, listener_count, logo_url, metadata}
//
// Lists every station this token authorizes -- not every station on the
// server, and not just this script's own. Unlike radio.status(), doesn't
// require radio.register() to have been called first, since it isn't
// scoped to "this" station at all.
func (e *Engine) luaListStations(L *lua.LState) int {
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.ListStations(ctx, &audioserverv1.ListStationsRequest{})
	if err != nil {
		L.RaiseError("radio.list_stations failed: %v", err)
		return 0
	}

	tbl := L.NewTable()
	for _, st := range resp.GetStations() {
		row := L.NewTable()
		row.RawSetString("slug", lua.LString(st.GetSlug()))
		row.RawSetString("name", lua.LString(st.GetName()))
		row.RawSetString("listener_count", lua.LNumber(st.GetListenerCount()))
		row.RawSetString("logo_url", lua.LString(st.GetLogoUrl()))
		row.RawSetString("metadata", stringMapToLuaTable(L, st.GetMetadata()))
		tbl.Append(row)
	}
	L.Push(tbl)
	return 1
}

// radio.list_directory(path) -> {{name=, is_dir=, path=, size_bytes=}, ...}
//
// Lists one directory under audio_root -- path defaults to "" (the root)
// if omitted. Not scoped to any station, same as radio.list_stations();
// what comes back is instead filtered by this controller's own token, via
// its dirs claim rather than its station slugs (see the audio server's
// auth package) -- an unrestricted token sees everything, a scoped one
// only what its dirs authorize. A file entry's path is already in the
// form radio.queue() expects as a local-file location, so a listing
// result can be queued directly.
func (e *Engine) luaListDirectory(L *lua.LState) int {
	path := L.OptString(1, "")
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.ListDirectory(ctx, &audioserverv1.ListDirectoryRequest{Path: path})
	if err != nil {
		L.RaiseError("radio.list_directory failed: %v", err)
		return 0
	}

	tbl := L.NewTable()
	for _, entry := range resp.GetEntries() {
		row := L.NewTable()
		row.RawSetString("name", lua.LString(entry.GetName()))
		row.RawSetString("is_dir", lua.LBool(entry.GetIsDir()))
		row.RawSetString("path", lua.LString(entry.GetPath()))
		row.RawSetString("size_bytes", lua.LNumber(entry.GetSizeBytes()))
		tbl.Append(row)
	}
	L.Push(tbl)
	return 1
}

// radio.server_info() -> {version}
//
// Reports the audio server's build version -- "dev" for a locally built
// binary with no version baked in via -ldflags. Not scoped to any
// station, same as radio.list_stations().
func (e *Engine) luaServerInfo(L *lua.LState) int {
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	resp, err := e.client.GetServerInfo(ctx, &audioserverv1.GetServerInfoRequest{})
	if err != nil {
		L.RaiseError("radio.server_info failed: %v", err)
		return 0
	}

	tbl := L.NewTable()
	tbl.RawSetString("version", lua.LString(resp.GetVersion()))
	L.Push(tbl)
	return 1
}

func queuedItemToLua(L *lua.LState, item *audioserverv1.QueuedItemStatus) *lua.LTable {
	tbl := L.NewTable()
	tbl.RawSetString("queue_id", lua.LString(item.GetQueueId()))
	tbl.RawSetString("location", lua.LString(item.GetSource().GetLocation()))
	tbl.RawSetString("title", lua.LString(item.GetSource().GetDisplayTitle()))
	tbl.RawSetString("artist", lua.LString(item.GetSource().GetDisplayArtist()))
	tbl.RawSetString("cover_art", lua.LString(item.GetSource().GetCoverArtUrl()))
	tbl.RawSetString("mode", lua.LString(item.GetMode().String()))
	tbl.RawSetString("duration_seconds", lua.LNumber(item.GetDurationSeconds()))
	return tbl
}

// historyEntryToLua mirrors queuedItemToLua, plus the reason/ended_at
// fields unique to a finished (rather than pending) item.
func historyEntryToLua(L *lua.LState, item *audioserverv1.HistoryEntryStatus) *lua.LTable {
	tbl := L.NewTable()
	tbl.RawSetString("queue_id", lua.LString(item.GetQueueId()))
	tbl.RawSetString("location", lua.LString(item.GetSource().GetLocation()))
	tbl.RawSetString("title", lua.LString(item.GetSource().GetDisplayTitle()))
	tbl.RawSetString("artist", lua.LString(item.GetSource().GetDisplayArtist()))
	tbl.RawSetString("cover_art", lua.LString(item.GetSource().GetCoverArtUrl()))
	tbl.RawSetString("mode", lua.LString(item.GetMode().String()))
	tbl.RawSetString("duration_seconds", lua.LNumber(item.GetDurationSeconds()))
	tbl.RawSetString("reason", lua.LString(item.GetReason()))
	tbl.RawSetString("ended_at_unix_ms", lua.LNumber(item.GetEndedAtUnixMs()))
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

// radio.on_register(fn): fn is called every time the engine
// *automatically* (re-)registers this station after the connection to the
// audio server dropped and came back -- never for the radio.register()
// call your own script makes explicitly. Use it to re-prime anything that
// only ever happens once at script startup today (most commonly, queueing
// a first track): if the audio server itself restarted while disconnected,
// its registry -- and this station's entire queue -- comes back empty,
// and radio.on_queue_low alone won't rescue that, since its edge trigger
// only fires on a transition into "low" from "not low," which never
// happens on its own for a queue that's been empty from the moment it was
// recreated.
func (e *Engine) luaOnRegister(L *lua.LState) int {
	e.onRegistered = L.CheckFunction(1)
	return 0
}

// luaTableToStringMap converts a Lua table into a map[string]string,
// silently dropping any entry whose key isn't a string (values are
// coerced via lua.LVAsString, same as every other string field read from
// a Lua table elsewhere in this file). Returns nil for an empty/nil
// table, matching RegisterStationRequest.metadata's "unset" wire value.
func luaTableToStringMap(tbl *lua.LTable) map[string]string {
	if tbl == nil {
		return nil
	}
	out := make(map[string]string)
	tbl.ForEach(func(k, v lua.LValue) {
		key, ok := k.(lua.LString)
		if !ok {
			return
		}
		out[string(key)] = lua.LVAsString(v)
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// stringMapToLuaTable is luaTableToStringMap's inverse, for surfacing a
// station's metadata back to Lua via radio.status()/radio.list_stations().
func stringMapToLuaTable(L *lua.LState, m map[string]string) *lua.LTable {
	tbl := L.NewTable()
	for k, v := range m {
		tbl.RawSetString(k, lua.LString(v))
	}
	return tbl
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
			CoverArtUrl:   lua.LVAsString(val.RawGetString("cover_art")),
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
