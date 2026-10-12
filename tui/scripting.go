package tui

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/script"
	lua "github.com/yuin/gopher-lua"
)

// SCRIPTS CALL THE DAEMON — the daemon's verbs as Lua functions (ADR 1791719881 §4.8).
//
//	autodoc.call(verb, params, cb)   any verb but the protocol's own; params a list, as the wire's
//	autodoc.run(id, args…)           a command catalog entry, by id (catalog.go): ok, err; never
//	                                 raises. err is "unknown: <id>", the entry's own, or a refusal:
//	                                 a plugin's command (plugin.*) is never Lua's to run
//	autodoc.current_workspace()      the workspace in use; nil before one is entered
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
	// run runs a catalog entry for autodoc.run (Host.runFromLua)
	run func(id string, args []string) error
}

// luaVerb is what a script's call needs of a contract.
type luaVerb struct {
	verb, audience string
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

func (*scripting) Name() string                 { return "scripting" }
func (*scripting) State() map[string]any        { return nil }
func (*scripting) Commands() map[string]Command { return nil }

// bind gives the engine autodoc.call and autodoc.run, which runs through run; the namespaces follow
// once the daemon says its contracts.
func (s *scripting) bind(eng *script.Engine, run func(id string, args []string) error) {
	s.eng, s.run = eng, run
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
			s.c.Notify(fmt.Sprintf("Lua's autodoc.<domain> functions: the daemon's contracts: %s", luaErr(v.(error))))
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
		v := luaVerb{verb: str(m, "verb"), audience: str(m, "audience")}
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
		}
		global.RawSetString(domain, t)
		for _, v := range byDomain[domain] {
			_, name, _ := strings.Cut(v.verb, ".")
			t.RawSetString(name, L.NewFunction(s.namespaced(v)))
		}
	}
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
	s.call(verb, params, L.OptFunction(3, nil))
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
		s.call(v.verb, params, cb)
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

// call sends verb to the daemon off the loop and gives cb the answer on it. Without the contracts
// yet (a script run before the first connect has answered), it fetches them first: a verb is
// refused by its contract's audience before it is ever sent.
func (s *scripting) call(verb string, params []any, cb *lua.LFunction) {
	known := s.verbs
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
		res, err := s.c.Call(ctx, verb, params...)
		return luaAnswer{res: res, err: err}
	}, func(a any) { s.answer(verb, cb, a.(luaAnswer)) })
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
			s.c.Notify(fmt.Sprintf("autodoc.call(%s): %s", verb, errv))
		}
		return
	}
	if err := s.eng.Callback(verb+"'s callback", cb, res, errv); err != nil {
		s.c.Notify("a Lua callback failed: " + err.Error())
	}
}

// luaErr is a failed call as a script reads it: the daemon's code and message, or the error.
func luaErr(err error) string {
	if c := code(err); c != 0 {
		return fmt.Sprintf("%d %s", c, wireMessage(err))
	}
	return err.Error()
}
