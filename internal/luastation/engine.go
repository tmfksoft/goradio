// Package luastation is the embedded Lua station controller runtime: it
// runs one Lua script per process, giving it a small `radio` API plus full
// HTTP/SQL modules, and speaks the AudioServerService gRPC protocol on the
// script's behalf — the same protocol any other language could use.
//
// gopher-lua's *lua.LState is not goroutine-safe, so all script execution
// and event-callback dispatch happens on one dedicated goroutine (Run's
// caller). A second goroutine only receives SubscribeEvents messages and
// forwards them down a channel for the main goroutine to dispatch.
package luastation

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
	"github.com/tmfksoft/goradio/internal/config"
)

// Engine runs one Lua station script against one audio server connection.
type Engine struct {
	log        *slog.Logger
	cfg        *config.StationConfig
	scriptPath string
	scriptArgs []string

	// ctx is Run's context, cancelled on SIGINT/SIGTERM. Every RPC the Lua
	// API issues (register/queue/status) derives from this rather than
	// context.Background(), so a script blocked retrying a call — e.g.
	// radio.register() while the audio server is unreachable — still
	// responds to the process being asked to shut down.
	ctx context.Context

	L      *lua.LState
	conn   *grpc.ClientConn
	client audioserverv1.AudioServerServiceClient

	registerMu     sync.RWMutex
	lastRegister   registerInfo
	registeredSlug string

	onTrackStarted *lua.LFunction
	onTrackEnded   *lua.LFunction
	onError        *lua.LFunction

	timers []*timerEntry
}

type registerInfo struct {
	slug, name, description string
}

type timerEntry struct {
	interval time.Duration // zero means one-shot (radio.after)
	next     time.Time
	fn       *lua.LFunction
}

// NewEngine constructs an Engine for the given script and its passthrough
// CLI args (exposed to Lua as radio.args).
func NewEngine(log *slog.Logger, cfg *config.StationConfig, scriptPath string, scriptArgs []string) *Engine {
	return &Engine{
		log:        log,
		cfg:        cfg,
		scriptPath: scriptPath,
		scriptArgs: scriptArgs,
		L:          lua.NewState(),
	}
}

// Run dials the audio server, loads and executes the script (which
// typically calls radio.register/radio.every/radio.on_* at top level),
// then services timers and pushed events until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	e.ctx = ctx
	defer e.L.Close()

	if err := e.connect(); err != nil {
		return err
	}
	defer e.conn.Close()

	e.setupLuaEnvironment()
	e.StartControlAPI()

	if err := e.L.DoFile(e.scriptPath); err != nil {
		return fmt.Errorf("run script %q: %w", e.scriptPath, err)
	}

	if e.getRegisteredSlug() == "" {
		return fmt.Errorf("script %q never called radio.register(...)", e.scriptPath)
	}

	eventsCh := make(chan *audioserverv1.StationEvent, 32)
	go e.subscribeEventsLoop(ctx, eventsCh)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-eventsCh:
			e.dispatchEvent(ev)
		case <-ticker.C:
			e.runDueTimers()
		}
	}
}

func (e *Engine) connect() error {
	conn, err := grpc.NewClient(
		e.cfg.Server.GRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(jwtCreds{token: e.cfg.Auth.JWT}),
	)
	if err != nil {
		return fmt.Errorf("dial audio server %q: %w", e.cfg.Server.GRPCAddr, err)
	}
	e.conn = conn
	e.client = audioserverv1.NewAudioServerServiceClient(conn)
	return nil
}

func (e *Engine) setRegisterInfo(slug, name, description string) {
	e.registerMu.Lock()
	e.lastRegister = registerInfo{slug, name, description}
	e.registeredSlug = slug
	e.registerMu.Unlock()
}

func (e *Engine) getRegisterInfo() registerInfo {
	e.registerMu.RLock()
	defer e.registerMu.RUnlock()
	return e.lastRegister
}

func (e *Engine) getRegisteredSlug() string {
	e.registerMu.RLock()
	defer e.registerMu.RUnlock()
	return e.registeredSlug
}

// registerWithRetry calls RegisterStation, retrying with exponential
// backoff (capped) until it succeeds or ctx is cancelled. Used both by the
// Lua-facing radio.register() and by the event-stream reconnect loop.
//
// Errors are only retried when they're plausibly transient (e.g. the audio
// server isn't reachable/up yet — codes.Unavailable). Errors that retrying
// can never fix on its own — a malformed/expired/wrong-audience JWT
// (Unauthenticated), a token not authorized for this slug
// (PermissionDenied), or a bad request (InvalidArgument) — return
// immediately instead of retrying forever, since nothing changes those
// outcomes short of the operator editing config and restarting.
func (e *Engine) registerWithRetry(ctx context.Context, slug, name, description string) (*audioserverv1.RegisterStationResponse, error) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err := e.client.RegisterStation(rctx, &audioserverv1.RegisterStationRequest{
			Slug:        slug,
			Name:        name,
			Description: description,
		})
		cancel()
		if err == nil {
			return resp, nil
		}
		if !isRetryable(err) {
			return nil, fmt.Errorf("register station %q: %w", slug, err)
		}

		e.log.Warn("register failed, retrying", "slug", slug, "error", err, "backoff", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// isRetryable reports whether err is a transient failure worth retrying
// (e.g. the audio server not being reachable yet) rather than a permanent
// one (bad token, bad request) that will keep failing identically forever.
func isRetryable(err error) bool {
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument:
		return false
	default:
		return true
	}
}

// subscribeEventsLoop runs on its own goroutine, forwarding StationEvents
// to out for the main goroutine to dispatch into Lua. On any stream error
// it re-registers (ephemeral audio-server state may have been lost if it
// restarted) and reconnects the stream, with exponential backoff.
func (e *Engine) subscribeEventsLoop(ctx context.Context, out chan<- *audioserverv1.StationEvent) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for ctx.Err() == nil {
		slug := e.getRegisteredSlug()
		stream, err := e.client.SubscribeEvents(ctx, &audioserverv1.SubscribeEventsRequest{Slug: slug})
		if err != nil {
			e.log.Warn("subscribe events failed, retrying", "error", err, "backoff", backoff)
			if !e.sleepBackoff(ctx, &backoff, maxBackoff) {
				return
			}
			continue
		}

		e.log.Info("subscribed to station events", "slug", slug)
		backoff = time.Second

		for {
			ev, err := stream.Recv()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				e.log.Warn("event stream ended, reconnecting", "error", err)
				break
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}

		info := e.getRegisterInfo()
		if _, err := e.registerWithRetry(ctx, info.slug, info.name, info.description); err != nil {
			return
		}
		if !e.sleepBackoff(ctx, &backoff, maxBackoff) {
			return
		}
	}
}

// sleepBackoff waits for backoff (advancing it, capped at max) or returns
// false immediately if ctx is cancelled.
func (e *Engine) sleepBackoff(ctx context.Context, backoff *time.Duration, max time.Duration) bool {
	select {
	case <-time.After(*backoff):
	case <-ctx.Done():
		return false
	}
	*backoff *= 2
	if *backoff > max {
		*backoff = max
	}
	return true
}

func (e *Engine) callLua(fn *lua.LFunction, args ...lua.LValue) {
	if fn == nil {
		return
	}
	if err := e.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, args...); err != nil {
		e.log.Warn("lua callback error", "error", err)
	}
}

func (e *Engine) runDueTimers() {
	now := time.Now()
	remaining := e.timers[:0]
	for _, t := range e.timers {
		if now.Before(t.next) {
			remaining = append(remaining, t)
			continue
		}
		e.callLua(t.fn)
		if t.interval > 0 {
			t.next = now.Add(t.interval)
			remaining = append(remaining, t)
		}
		// interval == 0 (radio.after): one-shot, drop it.
	}
	e.timers = remaining
}

func (e *Engine) dispatchEvent(ev *audioserverv1.StationEvent) {
	switch ev.GetType() {
	case audioserverv1.EventType_EVENT_TYPE_TRACK_STARTED:
		if e.onTrackStarted != nil {
			p := ev.GetTrackStarted()
			tbl := e.L.NewTable()
			tbl.RawSetString("queue_id", lua.LString(p.GetQueueId()))
			tbl.RawSetString("location", lua.LString(p.GetSource().GetLocation()))
			tbl.RawSetString("title", lua.LString(p.GetSource().GetDisplayTitle()))
			tbl.RawSetString("artist", lua.LString(p.GetSource().GetDisplayArtist()))
			e.callLua(e.onTrackStarted, tbl)
		}
	case audioserverv1.EventType_EVENT_TYPE_TRACK_ENDED:
		if e.onTrackEnded != nil {
			p := ev.GetTrackEnded()
			tbl := e.L.NewTable()
			tbl.RawSetString("queue_id", lua.LString(p.GetQueueId()))
			tbl.RawSetString("reason", lua.LString(p.GetReason()))
			e.callLua(e.onTrackEnded, tbl)
		}
	case audioserverv1.EventType_EVENT_TYPE_ERROR:
		if e.onError != nil {
			p := ev.GetError()
			tbl := e.L.NewTable()
			tbl.RawSetString("message", lua.LString(p.GetMessage()))
			tbl.RawSetString("code", lua.LString(p.GetCode()))
			e.callLua(e.onError, tbl)
		}
	default:
		// QUEUE_UPDATED, LISTENER_COUNT_CHANGED, SILENCE_STARTED/ENDED have
		// no dedicated Lua callback this phase; scripts can poll radio.status().
		e.log.Debug("event with no Lua callback registered", "type", ev.GetType())
	}
}

type jwtCreds struct {
	token string
}

func (c jwtCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + c.token}, nil
}

func (c jwtCreds) RequireTransportSecurity() bool { return false }
