package script

import (
	"bytes"
	"io"
	"maps"

	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// editorType is the metatable of the editor object a command's run gets; gatedEditorType a gated
// script's, whose run needs the commands grant (ADR 1792004705 §4.2).
const (
	editorType      = "autodoc.editor"
	gatedEditorType = "autodoc.editor.gated"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Doc is one function of the script API as its reference lists it: how it is called, what it
// returns, and what to know (Markdown, one row of a table). EditorDocs and TextDocs are the
// engine's own tables, so the reference made from them lists what the engine has.
type Doc struct {
	Call    string // "ed:line(n)"
	Returns string // "string"
	Notes   string // "Line n's text, …"
}

// method is one method of the editor object: its documentation and its function.
type method struct {
	name, args, returns, notes string
	fn                         func(*Engine, *lua.LState) int
}

// editorMethods are the editor object's methods, in the order the reference lists them.
var editorMethods = []method{
	{"value", "", "string", "The whole text, lines joined by `\"\\n\"`.", (*Engine).edValue},
	{"set_value", "text", "nothing", "Replaces the whole text, as one edit: `u` brings the old text back.", (*Engine).edSetValue},
	{"line_count", "", "number", "How many lines. A text ending in `\"\\n\"` has an empty last line.", (*Engine).edLineCount},
	{"line", "n", "string", "Line n's text, without its line break. Raises `no such line` for n outside 1 to `ed:line_count()`.", (*Engine).edLine},
	{"lines", "", "list of strings", "Every line, a copy.", (*Engine).edLines},
	{"cursor", "", "line, col", "Where the cursor is.", (*Engine).edCursor},
	{"set_cursor", "line, col", "nothing", "Moves the cursor, clamped, and scrolls to it.", (*Engine).edSetCursor},
	{"insert", "text", "line, col", "Inserts at the cursor, and returns the position just after the text. That position is exact even when Vim's Normal mode then rests the cursor on the last character (see Positions). `\"\\n\"`, `\"\\r\\n\"` and `\"\\r\"` start lines. In Vim's Normal mode the text goes before the character under the cursor.", (*Engine).edInsert},
	{"insert_at", "line, col, text", "line, col", "`ed:insert` at a position.", (*Engine).edInsertAt},
	{"replace", "l1, c1, l2, c2, text", "line, col", "Puts text in place of the range; returns the position just after it.", (*Engine).edReplace},
	{"delete", "l1, c1, l2, c2", "nothing", "Removes the range. The cursor goes to its start.", (*Engine).edDelete},
	{"text", "l1, c1, l2, c2", "string", "The range's text, line breaks as `\"\\n\"`.", (*Engine).edText},
	{"selection", "", "l1, c1, l2, c2, or `nil`", "The selection's range. A line-wise selection (`V`) runs from column 1 of its first line to the end of its last. `nil` outside Visual mode.", (*Engine).edSelection},
	{"select", "l1, c1, l2, c2", "nothing", "Selects the range, as Visual mode does, with the cursor on its last character: `ed:mode()` is then `\"visual\"`, whatever the keys. An empty range leaves the selection as it was.", (*Engine).edSelect},
	{"unselect", "", "nothing", "Ends the selection; the cursor stays where it is. Nothing without one.", (*Engine).edUnselect},
	{"find", "term, opts", "l1, c1, l2, c2, or `nil`", "The next match of term after the cursor. It does not move the cursor. `opts` is optional: `{ignore_case = true, whole_word = true, backward = true, wrap = true, at_cursor = true}`.", (*Engine).edFind},
	{"find_all", "term, opts", "list of `{l1, c1, l2, c2}`", "Every match of term, in order: `{}` with none. `opts` as `ed:find`'s; only `ignore_case` and `whole_word` apply.", (*Engine).edFindAll},
	{"replace_all", "term, text, opts", "number", "Puts text in place of every match of term, as one edit, and returns how many. `opts` as `ed:find_all`'s.", (*Engine).edReplaceAll},
	{"mode", "", "string", "The editor's mode: `\"normal\"`, `\"insert\"`, `\"replace\"`, `\"visual\"`, `\"visual_line\"` or `\"visual_block\"`. Without Vim's keys it is `\"insert\"`, or `\"visual\"` with a selection.", (*Engine).edMode},
	{"read_only", "", "boolean", "Whether edits are refused: a PDF's or DOCX's derived text, say.", (*Engine).edReadOnly},
	{"revision", "", "number", "Names the text as it is now: every edit and every new document gives a new one, and an undo gives back the one the text had. A callback compares the revision its command read with `ed:revision()` to know whether the page changed while it waited.", (*Engine).edRevision},
	{"modified", "", "boolean", "Whether the text has changed since it was opened or last saved, as `:x` asks before it writes.", (*Engine).edModified},
	{"run", "line", "nothing", "Runs a command line as the prompt does, without the `:`: `\"12\"`, `\"find TODO\"`, `\"table 2 3\"`. Raises the command's error.", (*Engine).edRun},
}

// EditorDocs is the editor object's methods as the reference lists them, in its order.
func EditorDocs() []Doc {
	out := make([]Doc, len(editorMethods))
	for i, m := range editorMethods {
		out[i] = Doc{Call: "ed:" + m.name + "(" + m.args + ")", Returns: m.returns, Notes: m.notes}
	}
	return out
}

// registerEditorType gives the editor object its methods.
func (e *Engine) registerEditorType() {
	L := e.L
	mt := L.NewTypeMetatable(editorType)
	methods := make(map[string]lua.LGFunction, len(editorMethods))
	for _, m := range editorMethods {
		methods[m.name] = func(L *lua.LState) int { return m.fn(e, L) }
	}
	lock := func(mt *lua.LTable, methods map[string]lua.LGFunction) {
		L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), methods))
		// getmetatable(ed) is a copy: a script may list the methods, and change no one else's
		L.SetField(mt, "__metatable", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{}))
		L.SetField(L.GetField(mt, "__metatable"), "__index", L.SetFuncs(L.NewTable(), methods))
	}
	lock(mt, methods)
	// a gated script's ed: the same methods, but run runs any command (:w, a catalog entry's), which
	// is the commands capability's to allow, as autodoc.run is
	gated := maps.Clone(methods)
	gated["run"] = func(L *lua.LState) int {
		g := L.CheckUserData(1).Value.(*gatedScript)
		if !g.grants.Granted(g.script, "commands") {
			L.Push(lua.LNil)
			L.Push(lua.LString("needs commands: not granted"))
			return 2
		}
		return e.edRun(L)
	}
	lock(L.NewTypeMetatable(gatedEditorType), gated)
}

// editorValue is the editor object for the command running now.
func (e *Engine) editorValue() lua.LValue {
	ud := e.L.NewUserData()
	ud.Value = e
	e.L.SetMetatable(ud, e.L.GetTypeMetatable(editorType))
	return ud
}

// editorValueFor is the editor object fn is given: a gated script's own when fn is one of its.
func (e *Engine) editorValueFor(fn *lua.LFunction) lua.LValue {
	g := e.envs[fn.Env]
	if g == nil {
		return e.editorValue()
	}
	ud := e.L.NewUserData()
	ud.Value = g
	e.L.SetMetatable(ud, e.L.GetTypeMetatable(gatedEditorType))
	return ud
}

// ed is the core a method acts on; outside a command, none.
func (e *Engine) ed(L *lua.LState) *widget.EditorCore {
	if e.cur == nil {
		L.RaiseError("the editor is only reachable inside a command's run")
	}
	return e.cur
}

// pos reads a 1-based (line, col) from arguments i and i+1 into a TextPos.
func pos(L *lua.LState, i int) widget.TextPos {
	return widget.TextPos{Line: L.CheckInt(i) - 1, Col: L.CheckInt(i+1) - 1}
}

// rng reads a 1-based range from arguments i..i+3.
func rng(L *lua.LState, i int) widget.TextRange {
	return widget.TextRange{From: pos(L, i), To: pos(L, i+2)}
}

// pushPos pushes a TextPos as two 1-based numbers.
func pushPos(L *lua.LState, p widget.TextPos) {
	L.Push(lua.LNumber(p.Line + 1))
	L.Push(lua.LNumber(p.Col + 1))
}

func pushRange(L *lua.LState, r widget.TextRange) int {
	pushPos(L, r.From)
	pushPos(L, r.To)
	return 4
}

// check raises a Lua error for a refused edit (a read-only editor).
func check(L *lua.LState, err error) {
	if err != nil {
		L.RaiseError("%s", err.Error())
	}
}

func (e *Engine) edValue(L *lua.LState) int {
	L.Push(lua.LString(e.ed(L).Value()))
	return 1
}

func (e *Engine) edSetValue(L *lua.LState) int {
	c := e.ed(L)
	all := widget.TextRange{To: widget.TextPos{Line: c.LineCount() - 1, Col: 1 << 30}}
	_, err := c.Replace(all, L.CheckString(2))
	check(L, err)
	return 0
}

func (e *Engine) edLineCount(L *lua.LState) int {
	L.Push(lua.LNumber(e.ed(L).LineCount()))
	return 1
}

func (e *Engine) edLine(L *lua.LState) int {
	c := e.ed(L)
	n := L.CheckInt(2)
	if n < 1 || n > c.LineCount() {
		L.ArgError(2, "no such line")
	}
	L.Push(lua.LString(c.LineAt(n - 1)))
	return 1
}

func (e *Engine) edLines(L *lua.LState) int {
	t := L.NewTable()
	for _, l := range e.ed(L).Lines() {
		t.Append(lua.LString(l))
	}
	L.Push(t)
	return 1
}

func (e *Engine) edCursor(L *lua.LState) int {
	pushPos(L, e.ed(L).Cursor())
	return 2
}

func (e *Engine) edSetCursor(L *lua.LState) int {
	e.ed(L).SetCursor(pos(L, 2))
	return 0
}

func (e *Engine) edInsert(L *lua.LState) int {
	c := e.ed(L)
	end, err := c.Insert(c.Cursor(), L.CheckString(2))
	check(L, err)
	pushPos(L, end)
	return 2
}

func (e *Engine) edInsertAt(L *lua.LState) int {
	end, err := e.ed(L).Insert(pos(L, 2), L.CheckString(4))
	check(L, err)
	pushPos(L, end)
	return 2
}

func (e *Engine) edReplace(L *lua.LState) int {
	end, err := e.ed(L).Replace(rng(L, 2), L.CheckString(6))
	check(L, err)
	pushPos(L, end)
	return 2
}

func (e *Engine) edDelete(L *lua.LState) int {
	check(L, e.ed(L).Delete(rng(L, 2)))
	return 0
}

func (e *Engine) edText(L *lua.LState) int {
	L.Push(lua.LString(e.ed(L).TextIn(rng(L, 2))))
	return 1
}

func (e *Engine) edSelection(L *lua.LState) int {
	r, ok := e.ed(L).Selection()
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	return pushRange(L, r)
}

func (e *Engine) edSelect(L *lua.LState) int {
	e.ed(L).Select(rng(L, 2))
	return 0
}

func (e *Engine) edUnselect(L *lua.LState) int {
	e.ed(L).Unselect()
	return 0
}

// findOptions reads a find's opts table, argument i, if there is one; atCursor is its at_cursor.
func findOptions(L *lua.LState, i int) (o widget.FindOptions, atCursor bool) {
	switch v := L.Get(i).(type) {
	case *lua.LNilType:
	case *lua.LTable:
		o.IgnoreCase = lua.LVAsBool(v.RawGetString("ignore_case"))
		o.WholeWord = lua.LVAsBool(v.RawGetString("whole_word"))
		o.Backward = lua.LVAsBool(v.RawGetString("backward"))
		o.Wrap = lua.LVAsBool(v.RawGetString("wrap"))
		atCursor = lua.LVAsBool(v.RawGetString("at_cursor"))
	default:
		L.TypeError(i, lua.LTTable)
	}
	return o, atCursor
}

func (e *Engine) edFind(L *lua.LState) int {
	c := e.ed(L)
	term := L.CheckString(2)
	o, atCursor := findOptions(L, 3)
	var (
		r  widget.TextRange
		ok bool
	)
	if atCursor {
		r, ok = findFrom(c.FindAll(term, o), c.Cursor(), o)
	} else {
		r, ok = c.Find(term, c.Cursor(), o)
	}
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	return pushRange(L, r)
}

// findFrom is Find's choice among all with a match starting at the cursor counted: forward, the
// first starting at or after at; backward, the last starting at or before it; Wrap goes round.
func findFrom(all []widget.TextRange, at widget.TextPos, o widget.FindOptions) (widget.TextRange, bool) {
	if len(all) == 0 {
		return widget.TextRange{}, false
	}
	cmp := func(r widget.TextRange) int {
		switch {
		case r.From.Line != at.Line:
			return r.From.Line - at.Line
		default:
			return r.From.Col - at.Col
		}
	}
	if !o.Backward {
		for _, r := range all {
			if cmp(r) >= 0 {
				return r, true
			}
		}
		return all[0], o.Wrap
	}
	for i := len(all) - 1; i >= 0; i-- {
		if cmp(all[i]) <= 0 {
			return all[i], true
		}
	}
	return all[len(all)-1], o.Wrap
}

func (e *Engine) edFindAll(L *lua.LState) int {
	c := e.ed(L)
	term := L.CheckString(2)
	o, _ := findOptions(L, 3)
	t := L.NewTable()
	for _, r := range c.FindAll(term, o) {
		m := L.CreateTable(4, 0)
		for _, n := range []int{r.From.Line, r.From.Col, r.To.Line, r.To.Col} {
			m.Append(lua.LNumber(n + 1))
		}
		t.Append(m)
	}
	L.Push(t)
	return 1
}

func (e *Engine) edReplaceAll(L *lua.LState) int {
	c := e.ed(L)
	term, with := L.CheckString(2), L.CheckString(3)
	o, _ := findOptions(L, 4)
	n, err := c.ReplaceAll(term, with, o)
	check(L, err)
	L.Push(lua.LNumber(n))
	return 1
}

// modeNames are ed:mode's answers.
var modeNames = map[widget.EditorMode]string{
	widget.ModeNormal:      "normal",
	widget.ModeInsert:      "insert",
	widget.ModeReplace:     "replace",
	widget.ModeVisual:      "visual",
	widget.ModeVisualLine:  "visual_line",
	widget.ModeVisualBlock: "visual_block",
}

// ModeName is m as ed:mode() says it: "normal", "insert", "visual_line", ….
func ModeName(m widget.EditorMode) string {
	if name, ok := modeNames[m]; ok {
		return name
	}
	return m.String() // a mode golib added since: its own name, rather than nothing
}

func (e *Engine) edMode(L *lua.LState) int {
	L.Push(lua.LString(ModeName(e.ed(L).Mode())))
	return 1
}

func (e *Engine) edReadOnly(L *lua.LState) int {
	L.Push(lua.LBool(e.ed(L).ReadOnly()))
	return 1
}

func (e *Engine) edRevision(L *lua.LState) int {
	L.Push(lua.LNumber(e.ed(L).Revision()))
	return 1
}

func (e *Engine) edModified(L *lua.LState) int {
	L.Push(lua.LBool(e.ed(L).Modified()))
	return 1
}

func (e *Engine) edRun(L *lua.LState) int {
	check(L, e.ed(L).RunCommand(L.CheckString(2)))
	return 0
}
