package script

import (
	"slices"

	"github.com/yongjohnlee80/golib/tui"
	lua "github.com/yuin/gopher-lua"
)

// textFunction is one function of the global table's text: its documentation and its function.
type textFunction struct {
	name, args, returns, notes string
	fn                         lua.LGFunction
}

// textFunctions are <global>.text's: a string counted in characters, as the editor's columns are,
// where Lua's # and string.sub count bytes.
var textFunctions = []textFunction{
	{"columns", "s", "number", "How many characters (grapheme clusters) s has: the columns it takes in the editor. `#s` counts bytes.", textColumns},
	{"sub", "s, i, j", "string", "Characters i to j of s, both included, as `string.sub` takes bytes: `j` is the last by default, and a negative position counts from the end (`-1` the last).", textSub},
	{"chars", "s", "list of strings", "s's characters, in order: `é` written as `e` and an accent is one.", textChars},
}

// TextDocs is the global table's text functions as the reference lists them, the table named
// global ("autodoc" for "autodoc.text.columns(s)").
func TextDocs(global string) []Doc {
	out := make([]Doc, len(textFunctions))
	for i, f := range textFunctions {
		out[i] = Doc{Call: global + ".text." + f.name + "(" + f.args + ")", Returns: f.returns, Notes: f.notes}
	}
	return out
}

// registerText adds the text table to the global table.
func (e *Engine) registerText() {
	t := e.L.NewTable()
	for _, f := range textFunctions {
		e.L.SetField(t, f.name, e.L.NewFunction(f.fn))
	}
	e.L.SetField(e.global, "text", t)
}

func textColumns(L *lua.LState) int {
	n := 0
	for range tui.Graphemes(L.CheckString(1)) {
		n++
	}
	L.Push(lua.LNumber(n))
	return 1
}

func textChars(L *lua.LState) int {
	t := L.NewTable()
	for c := range tui.Graphemes(L.CheckString(1)) {
		t.Append(lua.LString(c))
	}
	L.Push(t)
	return 1
}

func textSub(L *lua.LState) int {
	cs := slices.Collect(tui.Graphemes(L.CheckString(1)))
	n := len(cs)
	// string.sub's positions: negative from the end, then clamped to 1..n
	at := func(i int) int {
		if i < 0 {
			i += n + 1
		}
		return i
	}
	i, j := at(L.OptInt(2, 1)), at(L.OptInt(3, -1))
	i = max(i, 1)
	j = min(j, n)
	if i > j {
		L.Push(lua.LString(""))
		return 1
	}
	var out []byte
	for _, c := range cs[i-1 : j] {
		out = append(out, c...)
	}
	L.Push(lua.LString(out))
	return 1
}
