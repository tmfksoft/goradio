package luastation

import (
	"encoding/json"

	lua "github.com/yuin/gopher-lua"
)

// RegisterJSONModule installs `require("json")`, letting Lua scripts decode
// and encode JSON — e.g. for reading a config file fetched via `io` or a
// response body from `require("http")`.
func RegisterJSONModule(L *lua.LState) {
	L.PreloadModule("json", func(L *lua.LState) int {
		mod := L.NewTable()
		L.SetFuncs(mod, map[string]lua.LGFunction{
			"decode": jsonDecode,
			"encode": jsonEncode,
		})
		L.Push(mod)
		return 1
	})
}

// jsonDecode implements json.decode(str) -> value, err.
func jsonDecode(L *lua.LState) int {
	str := L.CheckString(1)

	var v interface{}
	if err := json.Unmarshal([]byte(str), &v); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	L.Push(goValueToLuaGeneric(L, v))
	L.Push(lua.LNil)
	return 2
}

// jsonEncode implements json.encode(value) -> str, err.
func jsonEncode(L *lua.LState) int {
	v := luaValueToGoGeneric(L.CheckAny(1))

	data, err := json.Marshal(v)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	L.Push(lua.LString(string(data)))
	L.Push(lua.LNil)
	return 2
}
