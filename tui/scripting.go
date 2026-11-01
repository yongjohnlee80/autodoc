package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/script"
	lua "github.com/yuin/gopher-lua"
)

// SCRIPTS CALL THE DAEMON — the daemon's verbs as Lua functions
// (docs/decisions/contract.md#scripts-call-the-daemon-through-its-contracts).
//
//	autodoc.call(verb, params, cb)   any verb but the protocol's own; params a list, as the wire's
//	autodoc.run(id, args…)           a command catalog entry, by id (catalog.go): ok, err; never
//	                                 raises. err is "unknown: <id>", the entry's own, or a refusal:
//	                                 a plugin's command (plugin.*) is never Lua's to run
//	autodoc.current_workspace()      the workspace in use; nil before one is entered
//	:lua-reload                      every script loaded again on a new engine (Host.reloadScripts)
//	autodoc.<domain>.<verb>(…, cb)   each verb the daemon offers scripts, its params in order:
//	                                 autodoc.search.query("kb", "words", {limit = 5}, cb)
//
// A call never waits on the loop: it goes to the daemon on its own goroutine, and cb(result, err)
// runs later on the loop, as a command does (script.Engine.Callback). err is nil, or a string: the
// daemon's code and message ("-32061 not found"), or what went wrong before the daemon answered. A
// verb whose contract is the protocol's own (audience internal: sys.hello, sys.shutdown) is refused
// before any call, its refusal cb's err.
//
// The namespaces are made from the daemon's contracts (sys.contracts) each time the program
// connects, so they are the verbs this daemon offers at this TUI's protocol. A namespace's function
// checks its arity and raises a Lua error at the call, as a Lua function given the wrong arguments
// does; autodoc.call leaves that to the daemon, whose invalid params reach cb. A verb with a dot in
// its own part (domain.a.b) has no namespace function; autodoc.call reaches it.

// scripting is the feature binding the daemon's verbs into the Lua engine.
type scripting struct {
	c   Core
	eng *script.Engine
	// verbs are the daemon's contracts, by verb, as sys.contracts last answered; nil before it has.
	// Replaced, never changed in place: a call's goroutine reads the one it was given.
	verbs map[string]luaVerb
	// domains are the namespace tables made, by domain: kept and refilled on a reconnect, so a
	// script holding one (local s = autodoc.search) holds the new functions
	domains map[string]*lua.LTable
	seq     uint64 // the contracts' fetches: only the latest one's answer is kept
	// run runs a catalog entry for autodoc.run (Host.runFromLua); reload is :lua-reload's
	// (Host.reloadScripts)
	run     func(id string, args []string) error
	reload  func() error
	install func(url string) error // :lua-install's (Host.installPackage)
	// line runs a key's or a menu row's command line (Host: core.RunCommand); ui is the running
	// engine's keys; rows the menu rows, scripts' and plugins' (menurows.go), and menuRows the
	// Plugins menu's model of those it shows (luakeys.go)
	line func(string) error
	// who is who the invocation beginning now acts for (Host.authorityNow), and as runs fn as a's
	// invocation (Host.actingAs): the host's, set with line (scripts.go)
	who      func() authority
	as       func(a authority, fn func() error) error
	ui       luaUI
	rows     menuRows
	menuRows *tuidecl.ListModel
	// host is the clipboard and the agents for the host functions (luahost.go); pick is
	// autodoc.ui.pick's picker, pickCB the callback of the one open; inputCB is the callback of
	// autodoc.ui.input's line while it is open (LuaInput.qml)
	host    luaHost
	pick    *pick[luaItem]
	pickCB  *lua.LFunction
	inputCB *lua.LFunction
	// card is the card autodoc.ui.card shows (luacard.go), nil for none; cardGen the latest card's
	// number, cardEpoch the panel's opening, which a tick chain belongs to
	card      *luaCard
	cardGen   uint64
	cardEpoch uint64
	// shell opens a Lua shell in the terminal (terminals.newLuaShell); oneShot is :lua <chunk>'s
	// environment on the engine oneShotOf, made at the first such chunk after a new engine
	shell     func()
	oneShot   *script.Shell
	oneShotOf *script.Engine
}

// Catalog is :lua-reload (catalog.go).
func (s *scripting) Catalog() []Entry {
	return []Entry{{ID: "scripts.reload", Title: "reload the Lua scripts", Verb: "lua-reload",
		Help: "load every Lua script again: AutoDoc's, then the config folder's lua/",
		Run: func(Invocation) error {
			if s.reload == nil {
				return errors.New("lua-reload: the scripts have not started")
			}
			return s.reload()
		}},
		{ID: "scripts.shell", Title: "a Lua shell, or run Lua", Verb: "lua", Args: "[chunk]", UserOnly: true,
			Help: "alone, a Lua shell in the terminal; with a chunk, run it and show its values",
			Run: func(inv Invocation) error {
				if inv.Line == "" {
					if s.shell == nil {
						return errors.New("lua: the scripts have not started")
					}
					s.shell()
					return nil
				}
				return s.evalOnce(inv.Line)
			}},
		{ID: "scripts.install", Title: "install a Lua package", Verb: "lua-install", Args: "<name or git-url>",
			Help: "clone a Lua package into the config folder's lua/, once you agree",
			Run: func(inv Invocation) error {
				if s.install == nil {
					return errors.New("lua-install: the scripts have not started")
				}
				return s.install(inv.Line)
			}},
	}
}

// luaVerb is what a script's call needs of a contract.
type luaVerb struct {
	verb, audience string
	effect         string // read, write or settings; "" from a daemon older than effects
	params         []luaParam
	required       int
}

type luaParam struct {
	name, typ string
	optional  bool
}

// luaFeature is the host's scripting feature: one, whether the registry (features) or the engine
// (loadScripts) asks first.
func (h *Host) luaFeature() *scripting {
	if h.scripting == nil {
		h.scripting = &scripting{c: core{h}, domains: map[string]*lua.LTable{}}
	}
	return h.scripting
}

func (*scripting) Name() string { return "scripting" }
func (s *scripting) State() map[string]any {
	st := map[string]any{"App.luaPickRows": s.luaPicker().list.Model(), "App.luaPickField": "",
		"App.luaKeysText": s.luaKeysText(), "App.luaMenus": s.luaMenuRows(),
		"App.luaPickStatus": "items", "App.luaPickTitle": "choose",
		"App.luaPickPreviewTitle": "", "App.luaPickPreviewText": "", "App.luaPickPreviewAt": 0,
		"App.luaInputTitle": "", "App.luaInputLabel": "", "App.luaInputText": ""}
	maps.Copy(st, s.cardState())
	return st
}
func (s *scripting) Commands() map[string]Command {
	return map[string]Command{
		"App.luaKey":         oneString("a Lua key's letter", s.luaKey),
		"App.luaMenu":        oneString("a Lua menu row", s.luaMenu),
		"App.luaInputSet":    oneString("a script's answer", s.inputSet),
		"App.luaInputCancel": none(s.inputCancel),
	}
}

// Keys is SPC u, the Lua keys' card (views/LuaKeys.qml): its letters are the scripts', bound as
// they load, so it is not a key card of the catalog's.
func (*scripting) Keys() []Key {
	return []Key{{Card: leader, Seq: "u", Show: "u", Does: "your Lua keys", Run: "luaKeys.open()", Group: groupWindow}}
}

// Cards is none: SPC u's card is the scripts' own (views/LuaKeys.qml).
func (*scripting) Cards() []Card { return nil }

// bind gives the engine autodoc.call and autodoc.run, which runs through run; the namespaces follow
// once the daemon says its contracts.
func (s *scripting) bind(eng *script.Engine, run func(id string, args []string) error) {
	s.eng, s.run = eng, run
	// a gated script's calls need the capability the verb's effect asks
	// (docs/decisions/contract.md#gated-scripts-and-their-capabilities)
	eng.SetGate("call", func(_ string, L *lua.LState) string { return s.capabilityOf(L.CheckString(1)) })
	eng.SetGate("run", func(string, *lua.LState) string { return "commands" })
	// keys and rows run any command line: commands, as run (luakeys.go)
	s.mapUI(eng)
	// map and menu always register: a gated script's key or row checks commands when it runs, so a
	// grant given after the script loaded works at once (luakeys.go)
	eng.SetGate("map", func(string, *lua.LState) string { return "" })
	eng.SetGate("menu", func(string, *lua.LState) string { return "" })
	// a new engine (a reload): the namespaces are made again in it, from the contracts known, and
	// the old engine's card ends with its panel closed (luacard.go)
	s.resetCard()
	s.domains = map[string]*lua.LTable{}
	if s.verbs != nil {
		defer s.define(s.verbs)
	}
	eng.SetFunction("call", s.luaCall)
	eng.SetFunction("run", s.luaRun)
	eng.SetFunction("current_workspace", func(L *lua.LState) int {
		if ws := s.c.Workspace(); ws != "" {
			L.Push(lua.LString(ws))
		} else {
			L.Push(lua.LNil)
		}
		return 1
	})
}

// Connected fetches the daemon's contracts and makes the namespaces from them.
func (s *scripting) Connected() {
	t := s.c.Ticket(&s.seq, TicketSession)
	s.c.Go(func(ctx context.Context) any {
		verbs, err := fetchVerbs(ctx, s.c)
		if err != nil {
			return err
		}
		return verbs
	}, func(v any) {
		if !t.Current() {
			return
		}
		verbs, ok := v.(map[string]luaVerb)
		if !ok {
			s.c.Notify(s.c.Trf("autodoc.notice.scripting.lua_s_autodoc_domain_functions_the_daemon_s_cont", luaErr(v.(error))))
			return
		}
		s.define(verbs)
	})
}

// fetchVerbs asks the daemon for its contracts.
func fetchVerbs(ctx context.Context, c Core) (map[string]luaVerb, error) {
	res, err := c.Call(ctx, "sys.contracts")
	if err != nil {
		return nil, err
	}
	return decodeVerbs(res), nil
}

// decodeVerbs reads sys.contracts' answer as it comes off the wire.
func decodeVerbs(res any) map[string]luaVerb {
	out := map[string]luaVerb{}
	for _, raw := range asList(asMap(res)["verbs"]) {
		m := asMap(raw)
		v := luaVerb{verb: str(m, "verb"), audience: str(m, "audience"), effect: str(m, "effect")}
		ps := asList(m["params"])
		v.required = len(ps)
		for i, rp := range ps {
			pm := asMap(rp)
			p := luaParam{name: str(pm, "name"), typ: str(pm, "type")}
			p.optional, _ = pm["optional"].(bool)
			if p.optional && v.required == len(ps) {
				v.required = i
			}
			v.params = append(v.params, p)
		}
		if v.verb != "" {
			out[v.verb] = v
		}
	}
	return out
}

// define makes autodoc.<domain>.<verb> for every verb scripts may call, refilling the tables a
// connection before made and emptying those of domains the daemon no longer has. A domain whose
// name the global table already holds for something else (command, notify, call) gets none.
func (s *scripting) define(verbs map[string]luaVerb) {
	s.verbs = verbs
	if s.eng == nil {
		return
	}
	L, global := s.eng.L, s.eng.Global()
	byDomain := map[string][]luaVerb{}
	for _, v := range verbs {
		domain, name, ok := strings.Cut(v.verb, ".")
		if !ok || strings.Contains(name, ".") || v.audience == "internal" {
			continue
		}
		byDomain[domain] = append(byDomain[domain], v)
	}
	for domain, t := range s.domains {
		clearTable(t)
		if _, still := byDomain[domain]; !still {
			if global.RawGetString(domain) == t {
				global.RawSetString(domain, lua.LNil)
			}
			delete(s.domains, domain)
		}
	}
	for _, domain := range slices.Sorted(maps.Keys(byDomain)) {
		t, ours := s.domains[domain]
		if !ours {
			if global.RawGetString(domain) != lua.LNil {
				continue // the engine's own, or a script's
			}
			t = L.NewTable()
			s.domains[domain] = t
			s.eng.SetDomainGate(domain, func(name string, _ *lua.LState) string { return s.capabilityOf(domain + "." + name) })
		}
		global.RawSetString(domain, t)
		for _, v := range byDomain[domain] {
			_, name, _ := strings.Cut(v.verb, ".")
			t.RawSetString(name, L.NewFunction(s.namespaced(v)))
		}
	}
}

// capabilityOf is the capability a gated script needs to call verb: its effect's. A verb whose
// effect is not known (an older daemon, no contracts yet, a verb it does not list) is refused
// outright, script.Unclassified: mapped to any one capability, a grant of it could cover what it
// does not (an old daemon's doc.remove under settings).
func (s *scripting) capabilityOf(verb string) string {
	switch s.verbs[verb].effect {
	case "read":
		return "workspace.read"
	case "write":
		return "workspace.write"
	case "settings":
		return "settings"
	}
	return script.Unclassified
}

func clearTable(t *lua.LTable) {
	var keys []lua.LValue
	t.ForEach(func(k, _ lua.LValue) { keys = append(keys, k) })
	for _, k := range keys {
		t.RawSet(k, lua.LNil)
	}
}

// luaCall is autodoc.call(verb, params, cb): params a list of the verb's positional params (nil for
// none), cb optional.
func (s *scripting) luaCall(L *lua.LState) int {
	verb := L.CheckString(1)
	var params []any
	switch p := L.Get(2).(type) {
	case *lua.LNilType:
	case *lua.LTable:
		v, err := script.ToGo(p)
		if err != nil {
			L.ArgError(2, err.Error())
		}
		switch l := v.(type) {
		case []any:
			params = l
		case map[string]any:
			if len(l) > 0 {
				L.ArgError(2, "params is a list, the verb's params in order: {\"kb\", \"words\"}")
			}
		}
	default:
		L.ArgError(2, "params is a list, the verb's params in order: {\"kb\", \"words\"}")
	}
	s.call(s.authority(), verb, params, L.OptFunction(3, nil))
	return 0
}

// luaRun is autodoc.run(id, args…): the entry run now, on the loop, as a key runs it. Its
// arguments are strings, numbers or booleans, passed as their text. It returns true, or false and
// why; it never raises, so a script can try a command another may not have.
func (s *scripting) luaRun(L *lua.LState) int {
	fail := func(msg string) int {
		L.Push(lua.LFalse)
		L.Push(lua.LString(msg))
		return 2
	}
	id, ok := L.Get(1).(lua.LString)
	if !ok {
		return fail("autodoc.run: want a command's id, a string, not " + L.Get(1).Type().String())
	}
	var args []string
	for i := 2; i <= L.GetTop(); i++ {
		switch v := L.Get(i).(type) {
		case lua.LString, lua.LNumber, lua.LBool:
			args = append(args, v.String())
		default:
			return fail(fmt.Sprintf("autodoc.run: argument %d is a %s; want a string or a number", i-1, v.Type()))
		}
	}
	if err := s.run(string(id), args); err != nil {
		return fail(err.Error())
	}
	L.Push(lua.LTrue)
	L.Push(lua.LNil)
	return 2
}

// namespaced is v as autodoc.<domain>.<verb>(…, cb): its params in order, the callback last.
// Trailing nils are params left out.
func (s *scripting) namespaced(v luaVerb) lua.LGFunction {
	return func(L *lua.LState) int {
		n := L.GetTop()
		var cb *lua.LFunction
		if f, ok := L.Get(n).(*lua.LFunction); ok {
			cb, n = f, n-1
		}
		for n > 0 && L.Get(n) == lua.LNil {
			n--
		}
		if n < v.required || n > len(v.params) {
			L.RaiseError("autodoc.%s takes %s, then a callback", v.verb, v.arity())
		}
		params := make([]any, n)
		for i := range n {
			p, err := script.ToGo(L.Get(i + 1))
			if err != nil {
				L.ArgError(i+1, err.Error())
			}
			params[i] = p
		}
		s.call(s.authority(), v.verb, params, cb)
		return 0
	}
}

// arity is the params v takes, said: "2 to 3 params (workspace, query, opts?)".
func (v luaVerb) arity() string {
	if len(v.params) == 0 {
		return "no params"
	}
	names := make([]string, len(v.params))
	for i, p := range v.params {
		names[i] = p.name
		if p.optional {
			names[i] += "?"
		}
	}
	count := fmt.Sprint(v.required)
	if v.required != len(v.params) {
		count += fmt.Sprintf(" to %d", len(v.params))
	}
	unit := " params ("
	if count == "1" {
		unit = " param ("
	}
	return count + unit + strings.Join(names, ", ") + ")"
}

// luaAnswer is a call's outcome, carried back to the loop.
type luaAnswer struct {
	res any
	err error
}

// authority is who the Lua call beginning now acts for: the gated script calling (its wrapper sets
// Engine.Caller), else the user's own scripts. A host that set no who (a test of the feature alone)
// runs everything as the user's.
func (s *scripting) authority() authority {
	if s.who == nil {
		return userAuthority()
	}
	return s.who()
}

// call sends verb to the daemon off the loop, as a acts, and gives cb the answer on it. Without the
// contracts yet (a script run before the first connect has answered), it fetches them first: a verb
// is refused by its contract's audience before it is ever sent. A gated script's call is refused a
// preference that holds rights or commands (AS-2b), and goes out with its authority on the ctx, so
// the one send path (Host.call) gives it its bounds and checks its workspace is still the one in
// use (docs/decisions/contract.md#who-an-invocation-acts-for).
func (s *scripting) call(a authority, verb string, params []any, cb *lua.LFunction) {
	known := s.verbs
	if a.gated() {
		if err := refusedPreference(verb, params); err != nil {
			s.c.Post(func() { s.answer(verb, cb, luaAnswer{err: err}) })
			return
		}
	}
	s.c.Go(func(ctx context.Context) any {
		verbs := known
		if verbs == nil {
			var err error
			if verbs, err = fetchVerbs(ctx, s.c); err != nil {
				return luaAnswer{err: err}
			}
		}
		v, ok := verbs[verb]
		if ok && v.audience == "internal" {
			return luaAnswer{err: fmt.Errorf("%s is the protocol's own (audience internal): scripts cannot call it", verb)}
		}
		if ok {
			params = v.shape(params)
		}
		if a.gated() {
			if !ok {
				return luaAnswer{err: fmt.Errorf("%s: the daemon does not say its parameters, so a script's call cannot carry its bounds", verb)}
			}
			// the send path bounds it, and refuses it once the TUI has left its workspace (h.call):
			// one path for a script's own calls and for the work it starts
			a.verbs = verbs
			ctx = withAuthority(ctx, a)
		}
		res, err := s.c.Call(ctx, verb, params...)
		return luaAnswer{res: res, err: err}
	}, func(a any) { s.answer(verb, cb, a.(luaAnswer)) })
}

// bounded is params cut to, or padded with nil to, the verb's own, and then the gate's bounds: a
// script that passes more (a within of its own) reaches only what the gate allows.
func (v luaVerb) bounded(params []any, bounds map[string]any) []any {
	out := make([]any, len(v.params), len(v.params)+1)
	copy(out, params)
	return append(out, bounds)
}

// userPreferences are the preferences whose value is rights (the grants) or a command (the agent
// profiles' commands, run as /bin/sh -c): the user's to write, never a gated script's (AS-2b).
var userPreferences = map[string]bool{prefLuaGrants: true, prefAgentProfiles: true}

// refusedPreference is the refusal of a gated script's preference.set of one of userPreferences.
func refusedPreference(verb string, params []any) error {
	if verb != "preference.set" || len(params) == 0 {
		return nil
	}
	if name, _ := params[0].(string); userPreferences[name] {
		return fmt.Errorf("%s is the user's: a script may not write it", name)
	}
	return nil
}

// shape makes an empty table a list where the contract says a param is one: Lua's {} is a map
// (script.ToGo), and the daemon decodes a list param from a list only.
func (v luaVerb) shape(params []any) []any {
	for i, p := range params {
		if i < len(v.params) && strings.HasPrefix(v.params[i].typ, "[]") {
			if m, ok := p.(map[string]any); ok && len(m) == 0 {
				params[i] = []any{}
			}
		}
	}
	return params
}

// answer runs cb(result, err) on the loop. A callback's own error is the notifications'; so is a
// failed call with no callback to hear it.
func (s *scripting) answer(verb string, cb *lua.LFunction, a luaAnswer) {
	if s.eng == nil {
		return
	}
	res, errv := lua.LValue(lua.LNil), lua.LValue(lua.LNil)
	if a.err != nil {
		errv = lua.LString(luaErr(a.err))
	} else if v, err := script.ToLua(s.eng.L, a.res); err != nil {
		errv = lua.LString("the answer could not reach Lua: " + err.Error())
	} else {
		res = v
	}
	if cb == nil {
		if errv != lua.LNil {
			s.c.Notify(s.c.Trf("autodoc.notice.scripting.autodoc_call", verb, errv))
		}
		return
	}
	if err := s.eng.Callback(verb+"'s callback", cb, res, errv); err != nil && !errors.Is(err, script.ErrStale) {
		s.c.Notify(s.c.Trf("autodoc.notice.scripting.a_lua_callback_failed", err.Error()))
	}
}

// luaErr is a failed call as a script reads it: the daemon's code and message, or the error.
func luaErr(err error) string {
	if c := code(err); c != 0 {
		return fmt.Sprintf("%d %s", c, wireMessage(err))
	}
	return err.Error()
}

// evalOnce is :lua <chunk>: run in the engine's one-shot shell, its values one notice. The shell
// lives as long as the engine, so a callback the chunk left prints later, as a notice too, and the
// globals it sets stay for the next :lua <chunk>. An unfinished chunk is a problem here: there is
// no next line.
func (s *scripting) evalOnce(chunk string) error {
	if s.eng == nil {
		return errors.New("lua: the scripts have not started")
	}
	if s.oneShot == nil || s.oneShotOf != s.eng {
		s.oneShot, s.oneShotOf = s.eng.NewShell(func(text string) { s.c.Notify(text) }), s.eng
	}
	vals, err := s.eng.Eval(s.oneShot, chunk)
	switch {
	case errors.Is(err, script.ErrIncomplete):
		return errors.New("lua: the chunk is not finished: :lua alone opens a shell for more lines")
	case err != nil:
		return err
	}
	if len(vals) > 0 {
		s.c.Notify(strings.Join(vals, "\t"))
	}
	return nil
}
