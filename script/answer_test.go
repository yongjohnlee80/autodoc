package script

import (
	"errors"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// fnOf is the Lua global name's function, as a script loaded it.
func fnOf(t *testing.T, e *Engine, name string) *lua.LFunction {
	t.Helper()
	fn, ok := e.L.GetGlobal(name).(*lua.LFunction)
	if !ok {
		t.Fatalf("%s is no function", name)
	}
	return fn
}

// Answer returns the function's first result, and its error as a script's error is said. While it
// runs the engine counts as running (a reload is refused), and after it, on success or an error,
// it doesn't. A replaced engine's function is never called.
func TestAnswerAnswersAndCountsAsRunningWhileItDoes(t *testing.T) {
	e, _, _ := engine(t, "")
	var during []bool
	e.L.SetGlobal("probe", e.L.NewFunction(func(*lua.LState) int { during = append(during, e.Running()); return 0 }))
	if err := e.Load("a.lua", []byte("double = function(x) probe(); return x * 2 end\nfail = function() probe(); error('no') end")); err != nil {
		t.Fatal(err)
	}
	v, err := e.Answer(time.Time{}, "double", fnOf(t, e, "double"), lua.LNumber(21))
	if err != nil || v != lua.LNumber(42) {
		t.Fatalf("Answer: %v, %v; want 42", v, err)
	}
	if e.Running() {
		t.Fatal("still running after an answer")
	}
	if _, err := e.Answer(time.Time{}, "fail", fnOf(t, e, "fail")); err == nil || err.Error() != "fail: a.lua:2: no" {
		t.Fatalf("an error: %v", err)
	}
	if e.Running() {
		t.Fatal("still running after an answer's error")
	}
	if len(during) != 2 || !during[0] || !during[1] {
		t.Fatalf("Running inside the two answers: %v; want true, true", during)
	}
	other := New(widget.NewEditorCore())
	t.Cleanup(other.Close)
	if _, err := other.Answer(time.Time{}, "double", fnOf(t, e, "double"), lua.LNumber(1)); !errors.Is(err, ErrStale) {
		t.Fatalf("another engine's function: %v; want ErrStale", err)
	}
}

// Answer stops at by, before the engine's own deadline, and says the time it was given. A command
// the function runs (through a host function, as autodoc.run does) inherits that deadline: it
// stops at by too, not at the engine's own two seconds.
func TestAnswerStopsAtItsDeadlineAndSoDoesACommandItRuns(t *testing.T) {
	core := widget.NewEditorCore()
	e := New(core, WithTimeout(2*time.Second))
	t.Cleanup(e.Close)
	e.L.SetGlobal("runit", e.L.NewFunction(func(L *lua.LState) int {
		if err := core.RunCommand(L.CheckString(1)); err != nil {
			L.Push(lua.LString(err.Error()))
			return 1
		}
		return 0
	}))
	if err := e.Load("s.lua", []byte(`editor.command{ name = "spin", run = function(ed) while true do end end }
spin = function() while true do end end
nested = function() runit("spin"); return true end`)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := e.Answer(start.Add(150*time.Millisecond), "spin", fnOf(t, e, "spin"))
	if took := time.Since(start); err == nil || !strings.Contains(err.Error(), "ran past 150ms") || took > time.Second {
		t.Fatalf("a spinning answer: %v after %v; want stopped at 150ms", err, took)
	}
	start = time.Now()
	_, err = e.Answer(start.Add(150*time.Millisecond), "nested", fnOf(t, e, "nested"))
	if took := time.Since(start); err == nil || took > time.Second {
		t.Fatalf("an answer whose command spins: %v after %v; want stopped near 150ms, not at the engine's 2s", err, took)
	}
}
