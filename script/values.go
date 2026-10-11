package script

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// VALUES — Lua's values and Go's, converted as msgpack carries them: what a host hands a script
// from a call (an RPC's answer) and what a script hands the host for one (its params).
//
//	Lua                       Go
//	nil                       nil
//	boolean                   bool
//	number, integral          int64
//	number, not integral      float64
//	string                    string ([]byte becomes a string too)
//	table, keys 1..n          []any
//	any other table           map[string]any (keys as strings: 1 is "1")
//
// An empty table is a map: {} is the empty options a verb takes most often. A host that knows a
// param is a list makes the empty map an empty list itself.

// maxDepth is how deep a table may nest before ToGo gives up: a table holding itself never ends.
const maxDepth = 100

// ErrNotCarried is a value that cannot cross between Lua and Go: a function, a coroutine, a host's
// userdata, a table nested past maxDepth, a Go channel.
var ErrNotCarried = errors.New("script: a value that cannot cross between Lua and Go")

// ToGo is v as Go sees it (the table above).
func ToGo(v lua.LValue) (any, error) { return toGo(v, 0) }

func toGo(v lua.LValue, depth int) (any, error) {
	switch x := v.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(x), nil
	case lua.LNumber:
		return number(float64(x)), nil
	case lua.LString:
		return string(x), nil
	case *lua.LTable:
		if depth >= maxDepth {
			return nil, fmt.Errorf("%w: tables nested past %d (a table holding itself?)", ErrNotCarried, maxDepth)
		}
		return tableToGo(x, depth+1)
	}
	return nil, fmt.Errorf("%w: a %s", ErrNotCarried, v.Type())
}

// number is an integral Lua number as int64, any other as float64.
func number(f float64) any {
	if f == math.Trunc(f) && f >= math.MinInt64 && f < math.MaxInt64 {
		return int64(f)
	}
	return f
}

// tableToGo is t as a list when its keys are exactly 1..n, else as a map.
func tableToGo(t *lua.LTable, depth int) (any, error) {
	n, top, list := 0, 0.0, true
	t.ForEach(func(k, _ lua.LValue) {
		n++
		num, ok := k.(lua.LNumber)
		if !ok || float64(num) != math.Trunc(float64(num)) || num < 1 {
			list = false
			return
		}
		top = max(top, float64(num))
	})
	// n keys, every one a positive integer: they are 1..n exactly when the largest is n
	if list && n > 0 && top == float64(n) {
		out := make([]any, n)
		for i := range n {
			v, err := toGo(t.RawGetInt(i+1), depth)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	out := make(map[string]any, n)
	var err error
	t.ForEach(func(k, v lua.LValue) {
		if err != nil {
			return
		}
		var key string
		switch x := k.(type) {
		case lua.LString:
			key = string(x)
		case lua.LNumber:
			key = fmt.Sprint(number(float64(x)))
		case lua.LBool:
			key = strconv.FormatBool(bool(x))
		default:
			err = fmt.Errorf("%w: a %s as a table's key", ErrNotCarried, k.Type())
			return
		}
		out[key], err = toGo(v, depth)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ToLua is v as a script sees it (the table above): every integer and float a number, a slice or
// array a list, a map a table, a pointer what it points to. A fmt.Stringer that is none of those
// is its string; anything else is ErrNotCarried.
func ToLua(L *lua.LState, v any) (lua.LValue, error) {
	switch x := v.(type) {
	case nil:
		return lua.LNil, nil
	case lua.LValue:
		return x, nil
	case bool:
		return lua.LBool(x), nil
	case string:
		return lua.LString(x), nil
	case []byte:
		return lua.LString(x), nil
	case int64:
		return lua.LNumber(x), nil
	case float64:
		return lua.LNumber(x), nil
	case []any:
		t := L.CreateTable(len(x), 0)
		for i, e := range x {
			lv, err := ToLua(L, e)
			if err != nil {
				return nil, err
			}
			t.RawSetInt(i+1, lv)
		}
		return t, nil
	case map[string]any:
		t := L.CreateTable(0, len(x))
		for k, e := range x {
			lv, err := ToLua(L, e)
			if err != nil {
				return nil, err
			}
			t.RawSetString(k, lv)
		}
		return t, nil
	}
	return reflectToLua(L, reflect.ValueOf(v))
}

// reflectToLua is ToLua for the kinds msgpack's own types do not cover: other numbers, typed
// slices and maps, pointers.
func reflectToLua(L *lua.LState, rv reflect.Value) (lua.LValue, error) {
	switch rv.Kind() {
	case reflect.Bool:
		return lua.LBool(rv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return lua.LNumber(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return lua.LNumber(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return lua.LNumber(rv.Float()), nil
	case reflect.String:
		return lua.LString(rv.String()), nil
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return lua.LNil, nil
		}
		return ToLua(L, rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return lua.LString(rv.Bytes()), nil
		}
		t := L.CreateTable(rv.Len(), 0)
		for i := range rv.Len() {
			lv, err := ToLua(L, rv.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			t.RawSetInt(i+1, lv)
		}
		return t, nil
	case reflect.Map:
		t := L.CreateTable(0, rv.Len())
		for it := rv.MapRange(); it.Next(); {
			k, err := ToLua(L, it.Key().Interface())
			if err != nil {
				return nil, err
			}
			if k == lua.LNil {
				return nil, fmt.Errorf("%w: a nil map key", ErrNotCarried)
			}
			e, err := ToLua(L, it.Value().Interface())
			if err != nil {
				return nil, err
			}
			t.RawSet(k, e)
		}
		return t, nil
	}
	if s, ok := rv.Interface().(fmt.Stringer); ok {
		return lua.LString(s.String()), nil
	}
	return nil, fmt.Errorf("%w: a Go %s", ErrNotCarried, rv.Type())
}
