package script

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// EVENTS (docs/decisions/contract.md#script-events): a script asks to hear what happens to the
// page,
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
	return e.emit(event, info, func(*lua.LFunction) bool { return true })
}

// HandlesFor is Handles for one gated script's own handlers (EmitFor's).
func (e *Engine) HandlesFor(script, event string) bool {
	for _, fn := range e.handlers[event] {
		if e.ownedBy(fn, script) {
			return true
		}
	}
	return false
}

// EmitFor is Emit for the handlers gated script added (LoadGated, LoadPackage, under script's
// name) and no one else's: an event about that script alone, which another script has no business
// hearing (grant: the user's answer to its own requires{…}). An ungated script's handlers never hear
// it.
func (e *Engine) EmitFor(script, event string, info map[string]any) []error {
	return e.emit(event, info, func(fn *lua.LFunction) bool { return e.ownedBy(fn, script) })
}

// ownedBy reports whether fn was defined by gated script: its environment is that script's.
func (e *Engine) ownedBy(fn *lua.LFunction, script string) bool {
	g := e.envs[fn.Env]
	return g != nil && g.script == script
}

func (e *Engine) emit(event string, info map[string]any, hears func(*lua.LFunction) bool) []error {
	var hs []*lua.LFunction
	for _, fn := range e.handlers[event] {
		if hears(fn) {
			hs = append(hs, fn)
		}
	}
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
