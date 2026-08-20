package luastation

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	lua "github.com/yuin/gopher-lua"
)

const dbTypeName = "sql.DB"

// RegisterSQLModule installs `require("sql")`, giving Lua scripts real
// MySQL access via database/sql, per the "full trusted access" design
// decision: station authors are trusted operators.
func RegisterSQLModule(L *lua.LState) {
	registerDBType(L)

	L.PreloadModule("sql", func(L *lua.LState) int {
		mod := L.NewTable()
		L.SetFuncs(mod, map[string]lua.LGFunction{
			"open": sqlOpen,
		})
		L.Push(mod)
		return 1
	})
}

func registerDBType(L *lua.LState) {
	mt := L.NewTypeMetatable(dbTypeName)
	methods := L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"query": dbQuery,
		"exec":  dbExec,
		"close": dbClose,
	})
	L.SetField(mt, "__index", methods)
}

// sql.open(dsn) -> db, err (Go MySQL DSN format, e.g. "user:pass@tcp(host:3306)/dbname")
func sqlOpen(L *lua.LState) int {
	dsn := L.CheckString(1)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	if err := db.Ping(); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	ud := L.NewUserData()
	ud.Value = db
	L.SetMetatable(ud, L.GetTypeMetatable(dbTypeName))
	L.Push(ud)
	return 1
}

func checkDB(L *lua.LState) *sql.DB {
	ud := L.CheckUserData(1)
	db, ok := ud.Value.(*sql.DB)
	if !ok {
		L.ArgError(1, "sql.DB expected")
	}
	return db
}

// db:query(sql [, args...]) -> rows (array of {column = value, ...}), err
func dbQuery(L *lua.LState) int {
	db := checkDB(L)
	query := L.CheckString(2)
	args := luaArgsToAny(L, 3)

	rows, err := db.Query(query, args...)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	result := L.NewTable()
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		rowTbl := L.NewTable()
		for i, col := range cols {
			rowTbl.RawSetString(col, goValueToLua(vals[i]))
		}
		result.Append(rowTbl)
	}

	L.Push(result)
	return 1
}

// db:exec(sql [, args...]) -> ok, rows_affected, last_insert_id (or ok=false, err)
func dbExec(L *lua.LState) int {
	db := checkDB(L)
	query := L.CheckString(2)
	args := luaArgsToAny(L, 3)

	res, err := db.Exec(query, args...)
	if err != nil {
		L.Push(lua.LFalse)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()

	L.Push(lua.LTrue)
	L.Push(lua.LNumber(affected))
	L.Push(lua.LNumber(lastID))
	return 3
}

func dbClose(L *lua.LState) int {
	_ = checkDB(L).Close()
	return 0
}

func luaArgsToAny(L *lua.LState, from int) []interface{} {
	top := L.GetTop()
	out := make([]interface{}, 0, top-from+1)
	for i := from; i <= top; i++ {
		out = append(out, luaValueToGo(L.Get(i)))
	}
	return out
}

func luaValueToGo(v lua.LValue) interface{} {
	switch val := v.(type) {
	case lua.LString:
		return string(val)
	case lua.LNumber:
		return float64(val)
	case lua.LBool:
		return bool(val)
	default:
		return v.String()
	}
}

func goValueToLua(v interface{}) lua.LValue {
	switch val := v.(type) {
	case nil:
		return lua.LNil
	case []byte:
		return lua.LString(string(val))
	case string:
		return lua.LString(val)
	case int64:
		return lua.LNumber(val)
	case float64:
		return lua.LNumber(val)
	case bool:
		return lua.LBool(val)
	default:
		return lua.LString(fmt.Sprintf("%v", val))
	}
}
