package tui

import (
	"context"
	"errors"
	"fmt"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/script"
	lua "github.com/yuin/gopher-lua"
)

// SCRIPTS REACH THE CLIPBOARD, A PICKER AND THE AGENTS — host functions beside the daemon's:
//
//	autodoc.clipboard.write(text)          ok, err: the system clipboard set (a terminal's, by OSC 52)
//	autodoc.clipboard.read(cb)             cb(text, err) later. err "unsupported" where the program
//	                                       cannot read it (a terminal: its own paste is the way in),
//	                                       "timeout", "closed"; an empty clipboard, or one holding no
//	                                       text, is "" with no error
//	autodoc.ui.pick(spec, cb)              a picker over spec.items, titled spec.title: cb(value, err)
//	                                       once one is chosen; err "cancelled", or "busy" while
//	                                       another script's picker is open. An item is a string, or a
//	                                       table: label (shown), detail (previewed), value (given to
//	                                       cb; the item itself when it has none)
//	autodoc.agents.list()                  AutoDoc's agents: { codename, name, running, closing,
//	                                       instance, orphan }, saved profiles first; a closing one
//	                                       refuses a send
//	autodoc.agents.send(ref, text, cb)     text pasted to an agent, no Enter added (Core.SendText):
//	                                       ref a codename, or { codename, instance } for that exact
//	                                       incarnation. A
//	                                       stopped agent is started once the user agrees. cb(true) once
//	                                       typed, or cb(nil, err): err "cancelled", "stale" (that
//	                                       incarnation is gone), "unavailable", or why
//
// Every cb runs later on the loop, never inside the call, as the daemon's do: a refusal known at
// once is a cb on the next turn too. A script reads it as cb(result, err).

// luaHost is what the script functions need of the program: its clipboard and its agents.
type luaHost interface {
	copyText(text string) bool
	readText(done func(text string, err error)) error
	agentEntries() []AgentEntry
	sendText(to AgentRef, text string, done func(error)) error
	post(fn func())
}

// luaItem is one of a script's picker items.
type luaItem struct {
	label, detail string
	value         lua.LValue
}

// bindHost gives the engine autodoc.clipboard, autodoc.ui and autodoc.agents.
func (s *scripting) bindHost(eng *script.Engine, host luaHost) {
	s.host = host
	L := eng.L
	clip := L.NewTable()
	L.SetFuncs(clip, map[string]lua.LGFunction{"write": s.luaClipWrite, "read": s.luaClipRead})
	L.SetField(eng.Global(), "clipboard", clip)
	ui := L.NewTable()
	L.SetFuncs(ui, map[string]lua.LGFunction{"pick": s.luaPick})
	L.SetField(eng.Global(), "ui", ui)
	ag := L.NewTable()
	L.SetFuncs(ag, map[string]lua.LGFunction{"list": s.luaAgentsList, "send": s.luaAgentsSend})
	L.SetField(eng.Global(), "agents", ag)
	eng.SetDomainGate("clipboard", func(string, *lua.LState) string { return "clipboard" })
	eng.SetDomainGate("agents", func(string, *lua.LState) string { return "agents" })
}

// later runs cb(res, err) on a later turn of the loop: a refusal is never a callback inside the call.
func (s *scripting) later(name string, cb *lua.LFunction, res lua.LValue, err string) {
	if cb == nil || s.host == nil {
		return
	}
	s.host.post(func() { s.callback(name, cb, res, err) })
}

// callback runs cb(res, err) now, on the loop; err "" is none.
func (s *scripting) callback(name string, cb *lua.LFunction, res lua.LValue, err string) {
	if cb == nil || s.eng == nil {
		return
	}
	errv := lua.LValue(lua.LNil)
	if err != "" {
		errv = lua.LString(err)
	}
	if res == nil {
		res = lua.LNil
	}
	if e := s.eng.Callback(name+"'s callback", cb, res, errv); e != nil && !errors.Is(e, script.ErrStale) {
		s.c.Notify("a Lua callback failed: " + e.Error())
	}
}

func (s *scripting) luaClipWrite(L *lua.LState) int {
	if s.host == nil || !s.host.copyText(L.CheckString(1)) {
		L.Push(lua.LFalse)
		L.Push(lua.LString("the clipboard could not be set here"))
		return 2
	}
	L.Push(lua.LTrue)
	L.Push(lua.LNil)
	return 2
}

func (s *scripting) luaClipRead(L *lua.LState) int {
	cb := L.CheckFunction(1)
	if s.host == nil {
		s.later("clipboard.read", cb, nil, "unsupported")
		return 0
	}
	if err := s.host.readText(func(text string, err error) {
		if err != nil {
			s.callback("clipboard.read", cb, nil, clipErr(err))
			return
		}
		s.callback("clipboard.read", cb, lua.LString(text), "")
	}); err != nil {
		s.later("clipboard.read", cb, nil, clipErr(err))
	}
	return 0
}

// clipErr is a clipboard read's failure as a script reads it.
func clipErr(err error) string {
	switch {
	case errors.Is(err, tuicore.ErrClipboardUnsupported):
		return "unsupported"
	case errors.Is(err, tuicore.ErrClipboardTimeout):
		return "timeout"
	case errors.Is(err, tuicore.ErrClipboardClosed):
		return "closed"
	}
	return err.Error()
}

// newLuaPick is the picker autodoc.ui.pick opens (LuaPick.qml).
func (s *scripting) newLuaPick() *pick[luaItem] {
	return &pick[luaItem]{c: s.c, dialog: "luaPicker", name: "luaPick",
		list:   newRows(tuidecl.NewListModel("key", "label"), func(i int, x luaItem) rowOf { return rowOf{"key": fmt.Sprint(i), "label": x.label} }),
		field:  "App.luaPickField",
		status: "App.luaPickStatus", noun: "items",
		match: func(x luaItem) string { return x.label },
		preview: func(x luaItem) func(context.Context) answerOf[pane] {
			pn := pane{title: x.label, text: x.detail}
			return func(context.Context) answerOf[pane] { return answerOf[pane]{v: pn} }
		},
		scope:  TicketSession,
		choose: func(x luaItem) { s.picked(x.value, "") },
		cancel: func() { s.picked(nil, "cancelled") },
	}
}

// Picks is autodoc.ui.pick's picker, for Picker.qml's handlers.
func (s *scripting) Picks() map[string]picker { return map[string]picker{"luaPick": s.luaPicker()} }

func (s *scripting) luaPicker() *pick[luaItem] {
	if s.pick == nil {
		s.pick = s.newLuaPick()
	}
	return s.pick
}

// picked answers the open picker's script, once.
func (s *scripting) picked(v lua.LValue, err string) {
	cb := s.pickCB
	s.pickCB = nil
	s.callback("ui.pick", cb, v, err)
}

func (s *scripting) luaPick(L *lua.LState) int {
	spec := L.CheckTable(1)
	cb := L.CheckFunction(2)
	if s.pickCB != nil {
		s.later("ui.pick", cb, nil, "busy")
		return 0
	}
	items, ok := spec.RawGetString("items").(*lua.LTable)
	if !ok {
		L.ArgError(1, "pick needs items: a list of strings or {label=, detail=, value=}")
	}
	var all []luaItem
	items.ForEach(func(_, v lua.LValue) {
		switch it := v.(type) {
		case lua.LString:
			all = append(all, luaItem{label: string(it), value: it})
		case *lua.LTable:
			x := luaItem{label: lua.LVAsString(it.RawGetString("label")), detail: lua.LVAsString(it.RawGetString("detail")), value: it.RawGetString("value")}
			if x.value == lua.LNil {
				x.value = it
			}
			all = append(all, x)
		}
	})
	title := lua.LVAsString(spec.RawGetString("title"))
	if title == "" {
		title = "choose"
	}
	p := s.luaPicker()
	s.pickCB = cb
	s.c.Set("App.luaPickTitle", title)
	p.setAll(all)
	p.open()
	return 0
}

func (s *scripting) luaAgentsList(L *lua.LState) int {
	out := L.NewTable()
	if s.host != nil {
		for _, e := range s.host.agentEntries() {
			t := L.NewTable()
			L.SetField(t, "codename", lua.LString(e.Ref.Codename))
			L.SetField(t, "name", lua.LString(e.Name))
			L.SetField(t, "running", lua.LBool(e.Running))
			L.SetField(t, "closing", lua.LBool(e.Closing))
			L.SetField(t, "orphan", lua.LBool(e.Orphan))
			if e.Ref.Instance != "" {
				L.SetField(t, "instance", lua.LString(e.Ref.Instance))
			}
			out.Append(t)
		}
	}
	L.Push(out)
	return 1
}

func (s *scripting) luaAgentsSend(L *lua.LState) int {
	var ref AgentRef
	switch r := L.Get(1).(type) {
	case lua.LString:
		ref.Codename = string(r)
	case *lua.LTable:
		ref = AgentRef{Codename: lua.LVAsString(r.RawGetString("codename")), Instance: lua.LVAsString(r.RawGetString("instance"))}
	default:
		L.ArgError(1, "send needs an agent: a codename, or {codename=, instance=}")
	}
	text := L.CheckString(2)
	cb := L.OptFunction(3, nil)
	if s.host == nil {
		s.later("agents.send", cb, nil, "unavailable")
		return 0
	}
	if err := s.host.sendText(ref, text, func(err error) {
		if err != nil {
			s.callback("agents.send", cb, nil, agentErr(err))
			return
		}
		s.callback("agents.send", cb, lua.LTrue, "")
	}); err != nil {
		s.later("agents.send", cb, nil, agentErr(err))
	}
	return 0
}

// agentErr is a delivery's failure as a script reads it.
func agentErr(err error) string {
	switch {
	case errors.Is(err, ErrAgentCancelled):
		return "cancelled"
	case errors.Is(err, ErrAgentStale):
		return "stale"
	case errors.Is(err, ErrAgentUnavailable):
		return "unavailable"
	case errors.Is(err, ErrAgentShuttingDown):
		return "closed"
	}
	return err.Error()
}
