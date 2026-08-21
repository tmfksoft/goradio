package luastation

import (
	lua "github.com/yuin/gopher-lua"
	yaml "gopkg.in/yaml.v3"
)

// RegisterYAMLModule installs `require("yaml")`, letting Lua scripts decode
// and encode YAML — e.g. for reading a station's own config-style files
// via `io` alongside its main script.
func RegisterYAMLModule(L *lua.LState) {
	L.PreloadModule("yaml", func(L *lua.LState) int {
		mod := L.NewTable()
		L.SetFuncs(mod, map[string]lua.LGFunction{
			"decode": yamlDecode,
			"encode": yamlEncode,
		})
		L.Push(mod)
		return 1
	})
}

// yamlDecode implements yaml.decode(str) -> value, err.
func yamlDecode(L *lua.LState) int {
	str := L.CheckString(1)

	var v interface{}
	if err := yaml.Unmarshal([]byte(str), &v); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	L.Push(goValueToLuaGeneric(L, v))
	L.Push(lua.LNil)
	return 2
}

// yamlEncode implements yaml.encode(value) -> str, err.
func yamlEncode(L *lua.LState) int {
	v := luaValueToGoGeneric(L.CheckAny(1))

	data, err := yaml.Marshal(v)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	L.Push(lua.LString(string(data)))
	L.Push(lua.LNil)
	return 2
}
