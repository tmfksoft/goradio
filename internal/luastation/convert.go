package luastation

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// luaTableIsArray reports whether tb should round-trip as a JSON/YAML array
// rather than an object: non-empty and keyed by a contiguous 1..N integer
// sequence with no other keys.
func luaTableIsArray(tb *lua.LTable) bool {
	n := tb.Len()
	count := 0
	isArray := true
	tb.ForEach(func(k, _ lua.LValue) {
		count++
		num, ok := k.(lua.LNumber)
		if !ok {
			isArray = false
			return
		}
		i := int(num)
		if float64(i) != float64(num) || i < 1 || i > n {
			isArray = false
		}
	})
	return isArray && count == n
}

// luaValueToGoGeneric converts an arbitrary Lua value (as decoded by, or
// about to be handed to, encoding/json or yaml.v3) into plain Go values:
// nil, bool, float64, string, []interface{}, map[string]interface{}.
func luaValueToGoGeneric(v lua.LValue) interface{} {
	if v == lua.LNil {
		return nil
	}
	switch val := v.(type) {
	case lua.LBool:
		return bool(val)
	case lua.LNumber:
		return float64(val)
	case lua.LString:
		return string(val)
	case *lua.LTable:
		if luaTableIsArray(val) {
			n := val.Len()
			arr := make([]interface{}, n)
			for i := 1; i <= n; i++ {
				arr[i-1] = luaValueToGoGeneric(val.RawGetInt(i))
			}
			return arr
		}
		m := make(map[string]interface{})
		val.ForEach(func(k, v lua.LValue) {
			m[lua.LVAsString(k)] = luaValueToGoGeneric(v)
		})
		return m
	default:
		return v.String()
	}
}

// goValueToLuaGeneric converts a Go value produced by encoding/json or
// yaml.v3 decoding (nil, bool, string, numeric types, []interface{},
// map[string]interface{}) into a Lua value.
func goValueToLuaGeneric(L *lua.LState, v interface{}) lua.LValue {
	switch val := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(val)
	case string:
		return lua.LString(val)
	case int:
		return lua.LNumber(val)
	case int64:
		return lua.LNumber(val)
	case float64:
		return lua.LNumber(val)
	case []interface{}:
		tb := L.NewTable()
		for _, item := range val {
			tb.Append(goValueToLuaGeneric(L, item))
		}
		return tb
	case map[string]interface{}:
		tb := L.NewTable()
		for k, item := range val {
			tb.RawSetString(k, goValueToLuaGeneric(L, item))
		}
		return tb
	default:
		return lua.LString(fmt.Sprintf("%v", val))
	}
}
