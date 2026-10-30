package script

import (
	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// BUFFERS — the host's open documents, as a script sees them
// (docs/decisions/editor.md#buffers-in-lua-and-the-editor-socket).
//
//	<global>.buffers()    { {n, path, workspace, kind, modified, active}, … } in opening order
//	<global>.buffer(n)    an editor object for buffer n, shown or not, or nil, "no buffer n"
//
// A buffer's editor object has ed's methods. Each call runs as a command's does: the buffer's
// editor is the one edited (the host's headless one for a buffer not shown, by the code the user's
// keys run), and an edit is one Batch, one undo step. A gated script's handle carries its gate:
// run needs the commands capability, as its ed's does.

// bufferType is the metatable of a buffer's editor object.
const bufferType = "autodoc.buffer"

// BufferSource is what a host gives the engine for its buffers (WithBuffers).
type BufferSource interface {
	// Buffers are the open buffers, in opening order.
	Buffers() []BufferInfo
	// Editor is buffer n's editor: the shown one, or a headless one; ok is false with no buffer n.
	Editor(n int) (*widget.EditorCore, bool)
	// Edited tells the host buffer n's text changed through a handle: dirty, its change event.
	Edited(n int)
}

// BufferInfo is one buffer as buffers() lists it.
type BufferInfo struct {
	N         int
	Path      string // "" for the untitled draft
	Workspace string // "" for a file outside every workspace, and for the draft
	Kind      string
	Modified  bool
	Active    bool
}

// WithBuffers gives scripts the host's buffers: buffers() and buffer(n). Without it, neither exists.
func WithBuffers(s BufferSource) Option { return func(c *config) { c.buffers = s } }

// bufferHandle is buffer(n)'s editor object: the buffer's number, and the gated script that asked
// for it (nil for an ungated one).
type bufferHandle struct {
	n    int
	gate *gatedScript
}

// registerBuffers adds buffers() and buffer(n), and the handle's methods.
func (e *Engine) registerBuffers(src BufferSource) {
	L := e.L
	L.SetField(e.global, "buffers", L.NewFunction(func(L *lua.LState) int {
		list := src.Buffers()
		t := L.CreateTable(len(list), 0)
		for _, b := range list {
			row := L.CreateTable(0, 6)
			row.RawSetString("n", lua.LNumber(b.N))
			if b.Path != "" {
				row.RawSetString("path", lua.LString(b.Path))
			}
			if b.Workspace != "" {
				row.RawSetString("workspace", lua.LString(b.Workspace))
			}
			row.RawSetString("kind", lua.LString(b.Kind))
			row.RawSetString("modified", lua.LBool(b.Modified))
			row.RawSetString("active", lua.LBool(b.Active))
			t.Append(row)
		}
		L.Push(t)
		return 1
	}))
	L.SetField(e.global, "buffer", L.NewFunction(func(L *lua.LState) int {
		n := L.CheckInt(1)
		if _, ok := src.Editor(n); !ok {
			L.Push(lua.LNil)
			L.Push(lua.LString("no buffer " + lua.LNumber(n).String()))
			return 2
		}
		h := &bufferHandle{n: n}
		if e.calling != nil {
			h.gate = e.calling // the gated script asking: its handle keeps its gate
		}
		ud := L.NewUserData()
		ud.Value = h
		L.SetMetatable(ud, L.GetTypeMetatable(bufferType))
		L.Push(ud)
		return 1
	}))
	// buffer is gated "" (no capability of its own): a gated caller's call goes through with Caller
	// set, so its handle can carry the gate
	e.SetGate("buffer", func(string, *lua.LState) string { return "" })

	methods := make(map[string]lua.LGFunction, len(editorMethods))
	for _, m := range editorMethods {
		methods[m.name] = func(L *lua.LState) int {
			h := L.CheckUserData(1).Value.(*bufferHandle)
			if m.name == "run" && h.gate != nil && !h.gate.grants.Granted(h.gate.script, "commands") {
				L.Push(lua.LNil)
				L.Push(lua.LString("needs commands: not granted"))
				return 2
			}
			core, ok := src.Editor(h.n)
			if !ok {
				L.RaiseError("no buffer %d", h.n)
			}
			before := core.Revision()
			prev := e.cur
			e.cur = core
			defer func() {
				e.cur = prev
				if core.Revision() != before {
					src.Edited(h.n)
				}
			}()
			var n int
			_ = core.Batch(func() error { n = m.fn(e, L); return nil })
			return n
		}
	}
	mt := L.NewTypeMetatable(bufferType)
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), methods))
	// getmetatable(buffer) is a copy: a script may list the methods, and change no one else's
	L.SetField(mt, "__metatable", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{}))
	L.SetField(L.GetField(mt, "__metatable"), "__index", L.SetFuncs(L.NewTable(), methods))
}
