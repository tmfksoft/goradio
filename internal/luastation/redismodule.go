package luastation

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
	lua "github.com/yuin/gopher-lua"
)

const redisClientTypeName = "redis.Client"

const redisCallTimeout = 10 * time.Second

// RegisterRedisModule installs `require("redis")`, giving Lua scripts real
// Redis access (KV, lists, pub/sub) with no restrictions, per the same
// "full trusted access" decision as the http and sql modules — station
// authors are trusted operators. A common use case: an external
// application RPUSHes song requests into a list, or PUBLISHes them to a
// channel, and the station script pulls/subscribes and calls radio.queue.
//
// It's an Engine method (unlike the free-function http/sql registration)
// because client:subscribe needs Engine.ctx (for cancellation) and
// Engine.redisMsgCh (to hand received messages back to the single
// goroutine that owns the Lua state).
func (e *Engine) RegisterRedisModule(L *lua.LState) {
	e.registerRedisClientType(L)

	L.PreloadModule("redis", func(L *lua.LState) int {
		mod := L.NewTable()
		L.SetFuncs(mod, map[string]lua.LGFunction{
			"open": e.redisOpen,
		})
		L.Push(mod)
		return 1
	})
}

func (e *Engine) registerRedisClientType(L *lua.LState) {
	mt := L.NewTypeMetatable(redisClientTypeName)
	methods := L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"get":       e.redisGet,
		"set":       e.redisSet,
		"del":       e.redisDel,
		"exists":    e.redisExists,
		"lpush":     e.redisLPush,
		"rpush":     e.redisRPush,
		"lpop":      e.redisLPop,
		"rpop":      e.redisRPop,
		"llen":      e.redisLLen,
		"publish":   e.redisPublish,
		"subscribe": e.redisSubscribe,
		"close":     redisClose,
	})
	L.SetField(mt, "__index", methods)
}

// redis.open(addr [, options]) -> client, err
// options: {password = "...", db = 0}
func (e *Engine) redisOpen(L *lua.LState) int {
	addr := L.CheckString(1)

	opts := &goredis.Options{Addr: addr}
	if L.GetTop() >= 2 {
		if t, ok := L.Get(2).(*lua.LTable); ok {
			if pw, ok := t.RawGetString("password").(lua.LString); ok {
				opts.Password = string(pw)
			}
			if db, ok := t.RawGetString("db").(lua.LNumber); ok {
				opts.DB = int(db)
			}
		}
	}

	client := goredis.NewClient(opts)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	ud := L.NewUserData()
	ud.Value = client
	L.SetMetatable(ud, L.GetTypeMetatable(redisClientTypeName))
	L.Push(ud)
	return 1
}

func checkRedisClient(L *lua.LState) *goredis.Client {
	ud := L.CheckUserData(1)
	client, ok := ud.Value.(*goredis.Client)
	if !ok {
		L.ArgError(1, "redis.Client expected")
	}
	return client
}

// client:get(key) -> value (nil if not found), err
func (e *Engine) redisGet(L *lua.LState) int {
	client := checkRedisClient(L)
	key := L.CheckString(2)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	val, err := client.Get(ctx, key).Result()
	switch {
	case err == goredis.Nil:
		L.Push(lua.LNil)
		return 1
	case err != nil:
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	default:
		L.Push(lua.LString(val))
		return 1
	}
}

// client:set(key, value [, ttl_seconds]) -> ok, err
func (e *Engine) redisSet(L *lua.LState) int {
	client := checkRedisClient(L)
	key := L.CheckString(2)
	value := L.CheckString(3)
	ttlSeconds := L.OptNumber(4, 0)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	ttl := time.Duration(float64(ttlSeconds) * float64(time.Second))
	if err := client.Set(ctx, key, value, ttl).Err(); err != nil {
		L.Push(lua.LFalse)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// client:del(key [, key2, ...]) -> removed_count, err
func (e *Engine) redisDel(L *lua.LState) int {
	client := checkRedisClient(L)
	keys := checkStringVarargs(L, 2)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	n, err := client.Del(ctx, keys...).Result()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LNumber(n))
	return 1
}

// client:exists(key) -> found (bool), err
func (e *Engine) redisExists(L *lua.LState) int {
	client := checkRedisClient(L)
	key := L.CheckString(2)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	n, err := client.Exists(ctx, key).Result()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LBool(n > 0))
	return 1
}

// client:lpush(key, value) -> new_length, err
func (e *Engine) redisLPush(L *lua.LState) int {
	client := checkRedisClient(L)
	key := L.CheckString(2)
	value := L.CheckString(3)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	n, err := client.LPush(ctx, key, value).Result()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LNumber(n))
	return 1
}

// client:rpush(key, value) -> new_length, err
func (e *Engine) redisRPush(L *lua.LState) int {
	client := checkRedisClient(L)
	key := L.CheckString(2)
	value := L.CheckString(3)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	n, err := client.RPush(ctx, key, value).Result()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LNumber(n))
	return 1
}

// client:lpop(key) -> value (nil if empty), err
func (e *Engine) redisLPop(L *lua.LState) int {
	client := checkRedisClient(L)
	key := L.CheckString(2)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	val, err := client.LPop(ctx, key).Result()
	switch {
	case err == goredis.Nil:
		L.Push(lua.LNil)
		return 1
	case err != nil:
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	default:
		L.Push(lua.LString(val))
		return 1
	}
}

// client:rpop(key) -> value (nil if empty), err
func (e *Engine) redisRPop(L *lua.LState) int {
	client := checkRedisClient(L)
	key := L.CheckString(2)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	val, err := client.RPop(ctx, key).Result()
	switch {
	case err == goredis.Nil:
		L.Push(lua.LNil)
		return 1
	case err != nil:
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	default:
		L.Push(lua.LString(val))
		return 1
	}
}

// client:llen(key) -> length, err
func (e *Engine) redisLLen(L *lua.LState) int {
	client := checkRedisClient(L)
	key := L.CheckString(2)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	n, err := client.LLen(ctx, key).Result()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LNumber(n))
	return 1
}

// client:publish(channel, message) -> ok, err
func (e *Engine) redisPublish(L *lua.LState) int {
	client := checkRedisClient(L)
	channel := L.CheckString(2)
	message := L.CheckString(3)

	ctx, cancel := context.WithTimeout(e.ctx, redisCallTimeout)
	defer cancel()

	if err := client.Publish(ctx, channel, message).Err(); err != nil {
		L.Push(lua.LFalse)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// client:subscribe(channel, fn): fn(payload, channel) is called on the
// engine's single Lua goroutine every time a message arrives on channel,
// for as long as the process runs (or until the client is closed). Starts
// a background goroutine to receive messages; it exits cleanly when the
// engine's context is cancelled or the subscription's channel closes.
func (e *Engine) redisSubscribe(L *lua.LState) int {
	client := checkRedisClient(L)
	channel := L.CheckString(2)
	fn := L.CheckFunction(3)

	pubsub := client.Subscribe(e.ctx, channel)
	msgCh := pubsub.Channel()

	go func() {
		defer pubsub.Close()
		for {
			select {
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				select {
				case e.redisMsgCh <- redisCallback{fn: fn, channel: msg.Channel, payload: msg.Payload}:
				case <-e.ctx.Done():
					return
				}
			case <-e.ctx.Done():
				return
			}
		}
	}()

	return 0
}

func redisClose(L *lua.LState) int {
	_ = checkRedisClient(L).Close()
	return 0
}

func checkStringVarargs(L *lua.LState, from int) []string {
	top := L.GetTop()
	out := make([]string, 0, top-from+1)
	for i := from; i <= top; i++ {
		out = append(out, L.CheckString(i))
	}
	return out
}
