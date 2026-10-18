package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	lua "github.com/yuin/gopher-lua"
)

// misaligned is the first Markdown table in text whose rows take different terminal columns, as a
// terminal shows the Markdown as it is; "" when every table lines up.
func misaligned(text string) string {
	var block []string
	check := func() string {
		defer func() { block = block[:0] }()
		for _, row := range block {
			if tuicore.StringWidth(row) != tuicore.StringWidth(block[0]) {
				return strings.Join(block, "\n")
			}
		}
		return ""
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "|") {
			block = append(block, line)
			continue
		}
		if bad := check(); bad != "" {
			return bad
		}
	}
	return check()
}

// usageText runs the shipped :usage against a host of fixed answers, the model's name and id as
// given, and is the card's text after its first tick.
func usageCardText(t *testing.T, modelName, modelID string) string {
	t.Helper()
	src, err := ownScripts.ReadFile("lua/usage.lua")
	if err != nil {
		t.Fatal(err)
	}
	L := lua.NewState()
	defer L.Close()
	tbl := func(fields map[string]lua.LValue) *lua.LTable {
		t := L.NewTable()
		for k, v := range fields {
			L.SetField(t, k, v)
		}
		return t
	}
	fn := func(f lua.LGFunction) lua.LValue { return L.NewFunction(f) }
	stats := tbl(map[string]lua.LValue{
		"pid": lua.LNumber(4242), "started_at": lua.LNumber(1), "cpu_seconds": lua.LNumber(1.5), "rss": lua.LNumber(128 << 20),
		"peak_rss": lua.LNumber(210 << 20), "cpus": lua.LNumber(16), "go_memory": lua.LNumber(123 << 20), "go_heap": lua.LNumber(55 << 20),
		"goroutines": lua.LNumber(179), "uptime": lua.LNumber(16440), "version": lua.LString("v-test"),
	})
	var run, tick *lua.LFunction
	text := ""
	card := tbl(map[string]lua.LValue{"set": fn(func(L *lua.LState) int { text = L.CheckString(2); return 0 })})
	var answer = func(v lua.LValue) lua.LGFunction {
		return func(L *lua.LState) int {
			cb := L.CheckFunction(L.GetTop())
			L.Push(cb)
			L.Push(v)
			L.Call(1, 0)
			return 0
		}
	}
	models := L.NewTable()
	models.Append(tbl(map[string]lua.LValue{"name": lua.LString(modelName), "vectors": lua.LNumber(14496), "state": lua.LString("active"),
		"f32_bytes": lua.LNumber(50e6), "bits_bytes": lua.LNumber(10e6), "key_bytes": lua.LNumber(1e6)}))
	status := tbl(map[string]lua.LValue{"docs": lua.LNumber(1234), "chunks": lua.LNumber(14801), "pending_jobs": lua.LNumber(0),
		"failing": L.NewTable(), "held": lua.LNumber(0),
		"embeddings": tbl(map[string]lua.LValue{"semantic": lua.LString("ready"), "model": lua.LString(modelID)})})
	L.SetGlobal("autodoc", tbl(map[string]lua.LValue{
		"command": fn(func(L *lua.LState) int { run = L.CheckTable(1).RawGetString("run").(*lua.LFunction); return 0 }),
		"now":     fn(func(L *lua.LState) int { L.Push(lua.LNumber(100)); return 1 }),
		"ui": tbl(map[string]lua.LValue{
			"card": fn(func(L *lua.LState) int {
				tick = L.CheckTable(1).RawGetString("tick").(*lua.LFunction)
				L.Push(card)
				return 1
			}),
			"charts": fn(func(L *lua.LState) int { L.Push(lua.LFalse); return 1 }),
			"width":  fn(luaWidth),
		}),
		"app":               tbl(map[string]lua.LValue{"stats": fn(func(L *lua.LState) int { L.Push(stats); return 1 })}),
		"sys":               tbl(map[string]lua.LValue{"stats": fn(answer(stats))}),
		"current_workspace": fn(func(L *lua.LState) int { L.Push(lua.LString("kb")); return 1 }),
		"index":             tbl(map[string]lua.LValue{"status": fn(answer(status)), "models": fn(answer(models))}),
	}))
	if err := L.DoString(string(src)); err != nil {
		t.Fatal(err)
	}
	if err := L.CallByParam(lua.P{Fn: run, Protect: true}); err != nil {
		t.Fatal(err)
	}
	if err := L.CallByParam(lua.P{Fn: tick, Protect: true}, card); err != nil {
		t.Fatal(err)
	}
	return text
}

// :usage's tables line up by terminal columns, whatever the model's name is made of: ASCII, a
// narrow accented letter, wide CJK characters, or a letter and a combining mark.
func TestUsageTablesLineUpByDisplayColumns(t *testing.T) {
	const digest = "5de93a84837d0ff00da872e90830df5d973f616cbf1e5c198731ab19dd7b776b"
	for _, name := range []string{"snowflake-arctic-embed2:latest", "café", "模型", "émbed"} {
		text := usageCardText(t, name, "ollama|"+name+"|"+digest+"|1024")
		if bad := misaligned(text); bad != "" {
			t.Errorf("%q: a table does not line up:\n%s", name, bad)
		}
		if !strings.Contains(text, "ready (ollama "+name+", 1024 dims)") || strings.Contains(text, digest) {
			t.Errorf("%q: the model is not shortened to provider, name and dims:\n%s", name, text)
		}
	}
}

// An id that is not provider|name|digest|dims is shown as it is.
func TestUsageShowsAnOddModelIDAsItIs(t *testing.T) {
	for _, id := range []string{"plain-model", "a|b", "a|b|c|d|e", "a||"} {
		if text := usageCardText(t, "m", id); !strings.Contains(text, "ready ("+id+")") {
			t.Errorf("%q was not kept as it is:\n%s", id, text)
		}
	}
}
