package script

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// EVENTS (ADR 1792004705 §4.4): a script asks to hear what happens to the page,
//
//	editor.on("save", function(ed, info) … end)
//
// and the host emits each as it happens (Emit): which events there are, and what info holds, is
// the host's to say. A handler runs as a callback does (the deadline, ed reachable, its edits one
// undo step), in the order the handlers were added; one that fails is said and the rest still run.

// luaOn is <global>.on(event, fn): fn hears event from now on, until the engine is replaced.
func (e *Engine) luaOn(L *lua.LState) int {
	event := L.CheckString(1)
	fn := L.CheckFunction(2)
	if event == "" {
		L.ArgError(1, "an event's name")
	}
	e.handlers[event] = append(e.handlers[event], fn)
	return 0
}

// Handles reports whether a script listens for event: a host may skip the work of an emit nobody
// hears.
func (e *Engine) Handles(event string) bool { return len(e.handlers[event]) > 0 }

// Emit runs event's handlers, each as fn(ed, info), info as Lua values (ToLua). It returns their
// errors, each naming the event; a handler that fails stays.
func (e *Engine) Emit(event string, info map[string]any) []error {
	hs := e.handlers[event]
	if len(hs) == 0 {
		return nil
	}
	v, err := ToLua(e.L, info)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", event, err)}
	}
	var errs []error
	for _, fn := range hs {
		if err := e.Callback(event+"'s handler", fn, e.editorValueFor(fn), v); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
