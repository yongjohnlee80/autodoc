package script

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// eval runs a Lua expression in e's state and returns its value.
func eval(t *testing.T, e *Engine, expr string) lua.LValue {
	t.Helper()
	if err := e.L.DoString("__v = " + expr); err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return e.L.GetGlobal("__v")
}

// Lua's values reach Go as msgpack carries them: keys 1..n a list, any other table a map with
// string keys, integral numbers int64.
func TestToGoIsMsgpacksShape(t *testing.T) {
	e, _, _ := engine(t, "")
	for _, c := range []struct {
		expr string
		want any
	}{
		{"nil", nil},
		{"true", true},
		{"3", int64(3)},
		{"-2.0", int64(-2)},
		{"2.5", 2.5},
		{"2^63", math.Pow(2, 63)}, // past int64: stays a float
		{"'kb'", "kb"},
		{"{}", map[string]any{}},
		{"{'a', 'b'}", []any{"a", "b"}},
		{"{[1] = 'a', [2] = 'b'}", []any{"a", "b"}},
		{"{'a', nil, 'c'}", map[string]any{"1": "a", "3": "c"}}, // a hole: not 1..n
		{"{[0] = 'z', 'a'}", map[string]any{"0": "z", "1": "a"}},
		{"{[1.5] = 'x'}", map[string]any{"1.5": "x"}},
		{"{[true] = 1}", map[string]any{"true": int64(1)}},
		{"{limit = 5, stages = {'lexical'}, deep = {x = {1, 2.5}}}", map[string]any{
			"limit": int64(5), "stages": []any{"lexical"}, "deep": map[string]any{"x": []any{int64(1), 2.5}}}},
	} {
		got, err := ToGo(eval(t, e, c.expr))
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %#v, %v; want %#v", c.expr, got, err, c.want)
		}
	}
}

// A function, a table key that is a table, and a table holding itself cannot cross.
func TestToGoRefusesWhatCannotCross(t *testing.T) {
	e, _, _ := engine(t, "")
	if err := e.L.DoString("loop = {}; loop.me = loop"); err != nil {
		t.Fatal(err)
	}
	for _, expr := range []string{"function() end", "{f = print}", "{[{}] = 1}", "loop"} {
		if _, err := ToGo(eval(t, e, expr)); !errors.Is(err, ErrNotCarried) {
			t.Errorf("%s: %v, want ErrNotCarried", expr, err)
		}
	}
	if _, err := ToGo(e.editorValue()); !errors.Is(err, ErrNotCarried) {
		t.Errorf("the editor object: %v, want ErrNotCarried", err)
	}
}

// stamp is a struct a fmt.Stringer: its string crosses. A named string type crosses as a string.
type stamp struct{ s string }

func (n stamp) String() string { return "stamp " + n.s }

type opaque struct{ ch chan int }

// Go's values reach Lua as a script reads them; typed slices, maps and numbers too.
func TestToLuaIsWhatAScriptReads(t *testing.T) {
	e, _, _ := engine(t, "")
	n := int64(7)
	v, err := ToLua(e.L, map[string]any{
		"hits":  []any{map[string]any{"path": "a.md", "score": 0.5}, nil},
		"n":     int64(3),
		"u":     uint64(4),
		"i32":   int32(-5),
		"f32":   float32(1.5),
		"bytes": []byte("raw"),
		"ok":    true,
		"strs":  []string{"x", "y"},
		"arr":   [2]int{1, 2},
		"m":     map[string]int{"k": 9},
		"im":    map[int]string{1: "one"},
		"ptr":   &n,
		"nilp":  (*int64)(nil),
		"s":     stamp{"x"},
		"lv":    lua.LString("as is"),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.L.SetGlobal("v", v)
	got := eval(t, e, `table.concat({v.hits[1].path, v.hits[1].score, tostring(v.hits[2]), v.n, v.u, v.i32, v.f32, v.bytes,
		tostring(v.ok), v.strs[2], v.arr[2], v.m.k, v.im[1], v.ptr, tostring(v.nilp), v.s, v.lv}, " ")`)
	if want := "a.md 0.5 nil 3 4 -5 1.5 raw true y 2 9 one 7 nil stamp x as is"; got.String() != want {
		t.Fatalf("got %q\nwant %q", got.String(), want)
	}
	for _, bad := range []any{opaque{}, make(chan int), func() {}, []any{opaque{}}, map[string]any{"x": opaque{}}, map[any]int{nil: 1}} {
		if _, err := ToLua(e.L, bad); !errors.Is(err, ErrNotCarried) {
			t.Errorf("%T: %v, want ErrNotCarried", bad, err)
		}
	}
}

// A callback runs later, as a command does: the editor reachable, its edits one undo step, its
// error named, a runaway one stopped, and the engine usable after.
func TestACallbackRunsAsACommandDoes(t *testing.T) {
	e, core, said := engine(t, "text")
	if err := e.Load("cb.lua", []byte(`
editor.command{ name = "ask", run = function(ed)
  pending = function(answer, err)
    ed:insert(answer .. " ")
    ed:insert("again ")
    editor.notify(tostring(err))
  end
  failing = function() ed:insert("x"); error("it broke", 0) end
  spinning = function() while true do end end
end }
`)); err != nil {
		t.Fatal(err)
	}
	if err := core.RunCommand("ask"); err != nil {
		t.Fatal(err)
	}
	fn := func(name string) *lua.LFunction { return e.L.GetGlobal(name).(*lua.LFunction) }
	if err := e.Callback("ask's callback", fn("pending"), lua.LString("hello"), lua.LNil); err != nil {
		t.Fatal(err)
	}
	if core.Value() != "hello again text" || strings.Join(*said, "|") != "nil" {
		t.Fatalf("text %q, said %v", core.Value(), *said)
	}
	undo(core) // both inserts, one step
	if core.Value() != "text" {
		t.Fatalf("one undo left %q", core.Value())
	}
	if err := e.Callback("ask's callback", fn("failing")); err == nil || err.Error() != "ask's callback: it broke" {
		t.Fatalf("a failing callback: %v", err)
	}
	start := time.Now()
	if err := e.Callback("spin", fn("spinning")); err == nil || !strings.Contains(err.Error(), "stopped") || time.Since(start) > 2*time.Second {
		t.Fatalf("a runaway callback: %v after %v", err, time.Since(start))
	}
	// outside a command or a callback the editor is not reachable again
	if err := e.L.DoString(`pending("late")`); err == nil || !strings.Contains(err.Error(), "only reachable") {
		t.Fatalf("the editor outside: %v", err)
	}
	if err := e.Callback("ask's callback", fn("pending"), lua.LString("after"), lua.LString("e")); err != nil || !strings.Contains(core.Value(), "after again ") {
		t.Fatalf("the engine after a runaway: %v, %q", err, core.Value())
	}
}

// A command run inside another (ed:run) keeps the outer one's deadline after it returns: a runaway
// outer command is still stopped.
func TestAnInnerCommandRestoresTheOuterDeadline(t *testing.T) {
	core := widget.NewEditorCore(widget.CoreInitialText(""))
	e := New(core, WithTimeout(200*time.Millisecond))
	defer e.Close()
	if err := e.Load("n.lua", []byte(`
editor.command{ name = "inner", run = function(ed) end }
editor.command{ name = "outer", run = function(ed) ed:run("inner"); while true do end end }
`)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- core.RunCommand("outer") }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "outer: the script ran past") {
			t.Fatalf("outer = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the outer command ran on past its deadline")
	}
}
