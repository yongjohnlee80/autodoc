package script

import (
	"errors"
	"slices"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// eval_test.go: a shell's environment and Eval (ADR 1792256103 §4.3, §5 cells 5-8).

func shellEval(t *testing.T, e *Engine, sh *Shell, chunk string) []string {
	t.Helper()
	vals, err := e.Eval(sh, chunk)
	if err != nil {
		t.Fatalf("%q: %v", chunk, err)
	}
	return vals
}

// An expression's values come back; a statement's none; globals are the shell's own; several
// values are each one line; the stack is as it was after a value, many values or an error.
func TestEvalValuesAndAShellsGlobals(t *testing.T) {
	e, _, _ := engine(t, "")
	sh, other := e.NewShell(nil), e.NewShell(nil)
	if got := shellEval(t, e, sh, "1+1"); !slices.Equal(got, []string{"2"}) {
		t.Errorf("1+1: %q", got)
	}
	if got := shellEval(t, e, sh, "x = 5"); len(got) != 0 {
		t.Errorf("x = 5: %q, want nothing", got)
	}
	if got := shellEval(t, e, sh, "x"); !slices.Equal(got, []string{"5"}) {
		t.Errorf("x in its shell: %q", got)
	}
	if got := shellEval(t, e, other, "x"); !slices.Equal(got, []string{"nil"}) {
		t.Errorf("x in another shell: %q", got)
	}
	if v := e.L.GetGlobal("x"); v != lua.LNil {
		t.Errorf("x leaked into the engine's globals: %v", v)
	}
	top := e.L.GetTop()
	if got := shellEval(t, e, sh, `return 1, "a\nb", {2, k = true}`); !slices.Equal(got, []string{"1", `"a\nb"`, "{2, k = true}"}) {
		t.Errorf("three values: %q", got)
	}
	if _, err := e.Eval(sh, `error("boom")`); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("an error: %v", err)
	}
	if e.L.GetTop() != top {
		t.Errorf("the stack is %d, was %d", e.L.GetTop(), top)
	}
	if got := shellEval(t, e, sh, "string.upper('q')"); !slices.Equal(got, []string{`"Q"`}) {
		t.Errorf("a library through the globals: %q", got)
	}
}

// ed is the page, and a chunk's edits are one undo step; print writes to the shell's out.
func TestEvalReachesThePageAndPrints(t *testing.T) {
	e, core, said := engine(t, "first\nsecond")
	var printed []string
	sh := e.NewShell(func(s string) { printed = append(printed, s) })
	if got := shellEval(t, e, sh, "ed:line(1)"); !slices.Equal(got, []string{`"first"`}) {
		t.Errorf("ed:line(1): %q", got)
	}
	shellEval(t, e, sh, `ed:insert_at(1, 1, "a") ed:insert_at(1, 1, "b")`)
	if !strings.HasPrefix(core.Value(), "bafirst") {
		t.Fatalf("the page: %q", core.Value())
	}
	undo(core)
	if core.Value() != "first\nsecond" {
		t.Errorf("one undo left %q: the chunk was not one step", core.Value())
	}
	shellEval(t, e, sh, `print("x", 1)`)
	if !slices.Equal(printed, []string{"x\t1"}) || len(*said) != 0 {
		t.Errorf("print: the shell got %q, the notices %q", printed, *said)
	}
}

// A callback made in a shell is the engine's while the shell is open, and stale once it is closed.
func TestAShellsCallbacksAreTheEngines(t *testing.T) {
	e, _, _ := engine(t, "")
	var printed []string
	sh := e.NewShell(func(s string) { printed = append(printed, s) })
	shellEval(t, e, sh, `cb = function(v) print("got " .. v) end`)
	fn, ok := sh.env.RawGetString("cb").(*lua.LFunction)
	if !ok {
		t.Fatal("cb is not the shell's")
	}
	if err := e.Callback("cb", fn, lua.LString("x")); err != nil {
		t.Fatalf("a shell's callback: %v", err)
	}
	if !slices.Equal(printed, []string{"got x"}) {
		t.Errorf("printed %q", printed)
	}
	e.CloseShell(sh)
	if err := e.Callback("cb", fn, lua.LString("y")); !errors.Is(err, ErrStale) {
		t.Errorf("a closed shell's callback: %v, want ErrStale", err)
	}
}

// A chunk past the deadline is stopped with the engine's message, and the shell goes on.
func TestEvalStopsAtTheDeadline(t *testing.T) {
	e, _, _ := engine(t, "")
	sh := e.NewShell(nil)
	if _, err := e.Eval(sh, "while true do end"); err == nil || !strings.Contains(err.Error(), "ran past") {
		t.Fatalf("a loop: %v", err)
	}
	if got := shellEval(t, e, sh, "2*3"); !slices.Equal(got, []string{"6"}) {
		t.Errorf("after the deadline: %q", got)
	}
}

// incompleteCases are Lector r1 R4's corpus: chunks a next line can finish, and chunks no line can.
var incompleteCases = []struct {
	chunk string
	more  bool
}{
	{"for i=1,2 do", true},
	{"x =", true},
	{"f(", true},
	{"if x then", true},
	{"function f()\n  return 1", true},
	{"s = [[abc", true},
	{"s = [==[a\nb", true},
	{"s = [==[a]]b", true},  // ]] closes level 0, not level 2
	{"--[[ note", true},     // a long comment
	{"--[==[ a ]] b", true}, // still inside: ]] is not ]==]
	{"x = 1 -- c\nif x then", true},
	{"t = {1, 2,", true},
	{`s = "a" .. 'b' ..`, true},
	{"x = = 1", false},
	{"return )", false},
	{`"abc`, false},
	{`'abc`, false},
	{`"`, false},
	{`''..'`, false},
	{`s = "abc\`, false}, // an escaped newline is not continued: the REPL's policy
	{"s = \"a\nb\"", false},
	{`s = "a\"b`, false}, // an escaped quote does not end the string
	{"x = 1 -- \"not a string", false},
}

// Eval classifies each case: more input, or a problem. The r1 predicate (the EOF position alone)
// calls `"abc` more input; this one does not.
func TestEvalTellsUnfinishedFromWrong(t *testing.T) {
	e, _, _ := engine(t, "")
	for _, c := range incompleteCases {
		_, err := e.Eval(e.NewShell(nil), c.chunk)
		if got := errors.Is(err, ErrIncomplete); got != c.more {
			t.Errorf("%q: incomplete %v (err %v), want %v", c.chunk, got, err, c.more)
		}
	}
}

// tailState against the pinned scanner, a test-only cross-check: on every case the parser fails at
// EOF, tailState says "short string" exactly when the scanner's error is its unterminated string.
// The test reads the dependency's message to hold the scan to it; production never does.
func TestTailStateAgreesWithTheScanner(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	checked := 0
	for _, c := range incompleteCases {
		_, err := L.Load(strings.NewReader(c.chunk), "=lua")
		var api *lua.ApiError
		var perr *parse.Error
		if !errors.As(err, &api) || !errors.As(api.Cause, &perr) || perr.Pos.Line != parse.EOF {
			continue
		}
		if strings.Contains(perr.Message, "escape") {
			continue // a \ at EOF: the scanner names the escape, not the string; tailState says short, by policy
		}
		checked++
		scannerShort := perr.Message == "unterminated string"
		if short := tailState(c.chunk) == tailShortString; short != scannerShort {
			t.Errorf("%q: tailState short %v, the scanner's %q", c.chunk, short, perr.Message)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d cases failed at EOF: the corpus no longer exercises the scanner", checked)
	}
}

func TestTailState(t *testing.T) {
	for _, c := range []struct {
		in   string
		want tailKind
	}{
		{"x = 1", tailCode},
		{`x = "a`, tailShortString},
		{`x = "a" y`, tailCode},
		{"x = [[a", tailLongString},
		{"x = [[a]]", tailCode},
		{"x = [=[a]]", tailLongString},
		{"x = [=[a]=]", tailCode},
		{"--[[a", tailLongComment},
		{"--[[a]] x", tailCode},
		{"-- [[a", tailCode},
		{"-- a\n'b", tailShortString},
		{`'a\'`, tailShortString},
		{"\"a\nb", tailCode}, // a raw newline ends the string (the lexer's error)
	} {
		if got := tailState(c.in); got != c.want {
			t.Errorf("tailState(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// Lector PR r0 R1: a value's __tostring is the chunk's Lua, written inside its protected call and
// deadline: a normal one is the value's text, a raising one an error and never a panic, an endless
// one stopped; the stack is as it was and the next chunk runs.
func TestEvalWritesValuesUnderTheDeadline(t *testing.T) {
	e, _, _ := engine(t, "")
	sh := e.NewShell(nil)
	top := e.L.GetTop()
	if got := shellEval(t, e, sh, `setmetatable({}, {__tostring = function() return "kestrel" end})`); !slices.Equal(got, []string{"kestrel"}) {
		t.Errorf("a __tostring: %q", got)
	}
	if _, err := e.Eval(sh, `setmetatable({}, {__tostring = function() error("tostring-raised") end})`); err == nil || !strings.Contains(err.Error(), "tostring-raised") {
		t.Errorf("a raising __tostring: %v", err)
	}
	if _, err := e.Eval(sh, `setmetatable({}, {__tostring = function() while true do end end})`); err == nil || !strings.Contains(err.Error(), "ran past") {
		t.Errorf("an endless __tostring: %v", err)
	}
	if e.L.GetTop() != top {
		t.Errorf("the stack is %d, was %d", e.L.GetTop(), top)
	}
	if got := shellEval(t, e, sh, "1, 2"); !slices.Equal(got, []string{"1", "2"}) {
		t.Errorf("after them: %q", got)
	}
}
