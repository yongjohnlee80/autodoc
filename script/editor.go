package script

import (
	"bytes"
	"io"

	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// editorType is the metatable of the editor object a command's run gets.
const editorType = "autodoc.editor"

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// registerEditorType gives the editor object its methods.
func (e *Engine) registerEditorType() {
	L := e.L
	mt := L.NewTypeMetatable(editorType)
	methods := map[string]lua.LGFunction{
		"value":      e.edValue,
		"set_value":  e.edSetValue,
		"line_count": e.edLineCount,
		"line":       e.edLine,
		"lines":      e.edLines,
		"cursor":     e.edCursor,
		"set_cursor": e.edSetCursor,
		"insert":     e.edInsert,
		"insert_at":  e.edInsertAt,
		"replace":    e.edReplace,
		"delete":     e.edDelete,
		"text":       e.edText,
		"selection":  e.edSelection,
		"find":       e.edFind,
		"run":        e.edRun,
	}
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), methods))
}

// editorValue is the editor object for the command running now.
func (e *Engine) editorValue() lua.LValue {
	ud := e.L.NewUserData()
	ud.Value = e
	e.L.SetMetatable(ud, e.L.GetTypeMetatable(editorType))
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

func (e *Engine) edFind(L *lua.LState) int {
	c := e.ed(L)
	var o widget.FindOptions
	if t, ok := L.Get(3).(*lua.LTable); ok {
		o.IgnoreCase = lua.LVAsBool(t.RawGetString("ignore_case"))
		o.Backward = lua.LVAsBool(t.RawGetString("backward"))
		o.Wrap = lua.LVAsBool(t.RawGetString("wrap"))
	}
	r, ok := c.Find(L.CheckString(2), c.Cursor(), o)
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	return pushRange(L, r)
}

func (e *Engine) edRun(L *lua.LState) int {
	check(L, e.ed(L).RunCommand(L.CheckString(2)))
	return 0
}
