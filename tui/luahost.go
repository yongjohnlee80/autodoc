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
//	autodoc.agents.resume([ref])           ok, err: a SIGCONT to the agent's tab, ref as send's, none
//	                                       for the agents panel's tab now: an agent CLI stopped by
//	                                       its own Ctrl+Z (or a SIGTSTP from elsewhere) runs again; a
//	                                       running one is not disturbed. err "stale", "unavailable",
//	                                       or why
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
	resumeAgent(to AgentRef) error
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
	s.bindCard(L, ui) // ui.card, ui.charts, app.stats (luacard.go)
	eng.SetFunction("widgets", s.luaWidgets)
	// autodoc.plugin: a plugin's package's own plugin. The empty-capability gate asks for no grant;
	// it makes the gated proxy wrap these functions, which is what sets Engine.Caller (ADR 1792300990
	// §4.5); an ungated user script has no caller and is refused
	pt := L.NewTable()
	L.SetFuncs(pt, map[string]lua.LGFunction{"run": s.luaPluginRun})
	L.SetField(eng.Global(), "plugin", pt)
	eng.SetDomainGate("plugin", func(string, *lua.LState) string { return "" })
	ag := L.NewTable()
	L.SetFuncs(ag, map[string]lua.LGFunction{"list": s.luaAgentsList, "send": s.luaAgentsSend, "resume": s.luaAgentsResume})
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
		s.c.Notify(s.c.Trf("autodoc.notice.luahost.a_lua_callback_failed", e.Error()))
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

// luaAgentRef is argument n as an agent: a codename, or {codename=, instance=}; with optional, none
// is the zero ref.
func luaAgentRef(L *lua.LState, n int, verb string, optional bool) AgentRef {
	switch r := L.Get(n).(type) {
	case lua.LString:
		return AgentRef{Codename: string(r)}
	case *lua.LTable:
		return AgentRef{Codename: lua.LVAsString(r.RawGetString("codename")), Instance: lua.LVAsString(r.RawGetString("instance"))}
	case *lua.LNilType:
		if optional {
			return AgentRef{}
		}
	}
	L.ArgError(n, verb+" needs an agent: a codename, or {codename=, instance=}")
	return AgentRef{}
}

func (s *scripting) luaAgentsSend(L *lua.LState) int {
	ref := luaAgentRef(L, 1, "send", false)
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

// luaAgentsResume is autodoc.agents.resume([ref]): ok, or nil and why.
func (s *scripting) luaAgentsResume(L *lua.LState) int {
	ref := luaAgentRef(L, 1, "resume", true)
	err := ErrAgentUnavailable
	if s.host != nil {
		err = s.host.resumeAgent(ref)
	}
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(agentErr(err)))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
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

// widgetLister is the program's widgets, for autodoc.widgets (widgets.go).
type widgetLister interface{ widgets() []widgetTarget }

// luaWidgets is autodoc.widgets(): the places the keyboard can go, {name, title, open, focused}, in
// the order :go and F6 know them. autodoc.run("widget.go", name) goes to one.
func (s *scripting) luaWidgets(L *lua.LState) int {
	out := L.NewTable()
	if wl, ok := s.host.(widgetLister); ok {
		for _, w := range wl.widgets() {
			t := L.NewTable()
			L.SetField(t, "name", lua.LString(w.name))
			L.SetField(t, "title", lua.LString(w.title))
			L.SetField(t, "open", lua.LBool(w.open))
			L.SetField(t, "focused", lua.LBool(w.focused))
			out.Append(t)
		}
	}
	L.Push(out)
	return 1
}

// pluginRunner runs a plugin package's own plugin's commands (luaHostOf).
type pluginRunner interface {
	runOwnPlugin(script, id string, args []string) error
}

// luaPluginRun is autodoc.plugin.run(id, args…): a [[commands]] id of the calling package's own
// plugin, with args. ok, err; err "not a plugin's package" for any other caller.
func (s *scripting) luaPluginRun(L *lua.LState) int {
	id := L.CheckString(1)
	var args []string
	for i := 2; i <= L.GetTop(); i++ {
		args = append(args, L.CheckString(i))
	}
	script, _, ok := s.eng.Caller()
	pr, has := s.host.(pluginRunner)
	if !ok || !has {
		L.Push(lua.LFalse)
		L.Push(lua.LString("autodoc.plugin.run: not a plugin's package"))
		return 2
	}
	if err := pr.runOwnPlugin(script, id, args); err != nil {
		L.Push(lua.LFalse)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	L.Push(lua.LNil)
	return 2
}
