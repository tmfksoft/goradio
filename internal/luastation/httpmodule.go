package luastation

import (
	"io"
	"net/http"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// maxHTTPResponseBytes caps how much of a response body a Lua script's
// http.* call will read into memory, to keep a runaway response from
// exhausting the process — not a security boundary (per the "full trusted
// access" decision, scripts may call http.* against any URL), just a
// sanity cap.
const maxHTTPResponseBytes = 20 * 1024 * 1024

// RegisterHTTPModule installs `require("http")`, giving Lua scripts real
// HTTP client access with no domain restrictions, per the "full trusted
// access" design decision: station authors are trusted operators writing
// their own decision logic, not sandboxed third parties.
//
// It's an Engine method so requests derive from Engine.ctx rather than
// context.Background() — a slow/hanging request should still be cut short
// by the process being asked to shut down.
func (e *Engine) RegisterHTTPModule(L *lua.LState) {
	L.PreloadModule("http", func(L *lua.LState) int {
		mod := L.NewTable()
		L.SetFuncs(mod, map[string]lua.LGFunction{
			"get":    e.httpDo(http.MethodGet),
			"post":   e.httpDo(http.MethodPost),
			"put":    e.httpDo(http.MethodPut),
			"delete": e.httpDo(http.MethodDelete),
		})
		L.Push(mod)
		return 1
	})
}

// httpDo implements http.<method>(url [, body [, headers]]) -> ok,
// status_or_error, body, headers. ok is false (with the error message as
// the second return) if the request itself failed (DNS, connection,
// timeout); a non-2xx HTTP response is still ok=true so scripts can branch
// on status themselves.
func (e *Engine) httpDo(method string) lua.LGFunction {
	return func(L *lua.LState) int {
		url := L.CheckString(1)

		var body io.Reader
		if L.GetTop() >= 2 {
			if s, ok := L.Get(2).(lua.LString); ok && s != "" {
				body = strings.NewReader(string(s))
			}
		}

		req, err := http.NewRequestWithContext(e.ctx, method, url, body)
		if err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		if L.GetTop() >= 3 {
			if headers, ok := L.Get(3).(*lua.LTable); ok {
				headers.ForEach(func(k, v lua.LValue) {
					req.Header.Set(lua.LVAsString(k), lua.LVAsString(v))
				})
			}
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBytes))
		if err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		respHeaders := L.NewTable()
		for k := range resp.Header {
			respHeaders.RawSetString(k, lua.LString(resp.Header.Get(k)))
		}

		L.Push(lua.LTrue)
		L.Push(lua.LNumber(resp.StatusCode))
		L.Push(lua.LString(string(data)))
		L.Push(respHeaders)
		return 4
	}
}
