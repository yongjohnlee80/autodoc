package script

import (
	"context"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// GATED SCRIPTS — a script that must ask before it uses what it could misuse (ADR 1792004705 §4.2).
// A gated script runs in an environment of its own: its globals are its own, its libraries copies,
// and its global table (autodoc) a proxy over the engine's. A function looked up through the proxy
// that a host gated (SetGate, SetDomainGate) comes back wrapped: it asks the script's Grants
// before every call, and a call without the grant answers nil and why. A function the script
// defines keeps its environment, and so its proxy, wherever it runs later (a callback, a handler,
// a mapped key): the grants are always the script's own, with nothing to track at call time.
//
//	eng.SetGate("call", func(_ string, L *lua.LState) string { return capabilityOf(L.CheckString(1)) })
//	eng.SetDomainGate("clipboard", func(string, *lua.LState) string { return "clipboard" })
//	err := eng.LoadGated("wordcount", "wordcount/init.lua", src, grants)
//
// The script declares what it needs as it loads, autodoc.requires{"workspace.read"}: the host's
// Grants.Request hears it and asks the user. Ungated scripts (Load, LoadDir) see none of this, and
// have no requires.

// Grants is the host's record of what the user let each gated script do.
type Grants interface {
	// Granted reports whether script may use capability now.
	Granted(script, capability string) bool
	// Request is the script's requires{…}: the capabilities it declared, for the host to ask about.
	Request(script string, capabilities []string)
}

// Unclassified is a Gate's answer for a call it cannot classify (a daemon verb whose effect the
// daemon does not say): refused whatever the script was granted, since no grant can be known to
// cover it.
const Unclassified = "\x00unclassified"

// Gate names the capability a call of a gated function needs, "" for none, Unclassified for one it
// cannot say. name is the function's
// key in its table ("call", "read"); L holds the call's arguments, from 1, for a gate that depends
// on them (autodoc.call: its verb).
type Gate func(name string, L *lua.LState) string

// SetGate gates the global table's function name. Functions SetFunction adds are ungated until
// gated here.
func (e *Engine) SetGate(name string, g Gate) { e.gates[name] = g }

// SetDomainGate gates every function of the global table's table domain: autodoc.clipboard, or a
// daemon domain, autodoc.search.
func (e *Engine) SetDomainGate(domain string, g Gate) { e.domainGates[domain] = g }

// LoadGated runs one gated script, src, as script (the name its grants are kept under); name is how
// its errors name it.
func (e *Engine) LoadGated(script, name string, src []byte, grants Grants) error {
	env := e.gatedEnv(script, grants)
	fn, err := e.L.Load(bytesReader(src), name)
	if err != nil {
		return e.failure(name, context.Background(), err)
	}
	fn.Env = env // every function it defines inherits it (gopher-lua's closures take their maker's)
	return e.run(name, fn)
}

// gatedEnv is a gated script's environment: its own globals, the sandbox's libraries as copies (so
// a script that changes string.lower changes only its own), and the global table as a proxy.
func (e *Engine) gatedEnv(script string, grants Grants) *lua.LTable {
	L := e.L
	env := L.NewTable()
	e.base.ForEach(func(k, v lua.LValue) {
		if t, ok := v.(*lua.LTable); ok {
			cp := L.NewTable()
			t.ForEach(func(k2, v2 lua.LValue) { cp.RawSet(k2, v2) })
			v = cp
		}
		env.RawSet(k, v)
	})
	env.RawSetString("_G", env)
	proxy := e.proxy(e.global, script, grants, func(key string) Gate { return e.gates[key] })
	proxy.RawSetString("requires", L.NewFunction(func(L *lua.LState) int {
		t := L.CheckTable(1)
		var caps []string
		t.ForEach(func(_, v lua.LValue) {
			if s, ok := v.(lua.LString); ok {
				caps = append(caps, string(s))
			}
		})
		grants.Request(script, caps)
		return 0
	}))
	env.RawSetString(e.globalName, proxy)
	e.envs[env] = &gatedScript{script: script, grants: grants}
	return env
}

// proxy is a gated view of real: a read finds real's field now (a domain the host filled since is
// seen), a gated function comes back wrapped and a gated domain as a proxy of its own; a write stays
// in the proxy.
func (e *Engine) proxy(real *lua.LTable, script string, grants Grants, gateOf func(key string) Gate) *lua.LTable {
	L := e.L
	p := L.NewTable()
	wrapped := map[lua.LValue]lua.LValue{} // each function or domain wrapped once, so == holds
	mt := L.NewTable()
	mt.RawSetString("__index", L.NewFunction(func(L *lua.LState) int {
		key := L.Get(2)
		v := real.RawGet(key)
		name, _ := key.(lua.LString)
		if w, ok := wrapped[v]; ok {
			L.Push(w)
			return 1
		}
		switch x := v.(type) {
		case *lua.LFunction:
			if g := gateOf(string(name)); g != nil {
				wrapped[v] = e.gated(x, string(name), g, script, grants)
				v = wrapped[v]
			}
		case *lua.LTable:
			// every table a proxy too, gated or not: autodoc.text is everyone's, not this script's to change
			g := e.domainGates[string(name)]
			wrapped[v] = e.proxy(x, script, grants, func(string) Gate { return g })
			v = wrapped[v]
		}
		L.Push(v)
		return 1
	}))
	mt.RawSetString("__metatable", lua.LString("a gated table"))
	L.SetMetatable(p, mt)
	return p
}

// gated is fn behind g: a call whose capability script has not been granted answers nil and why,
// and does nothing; any other is fn's.
func (e *Engine) gated(fn *lua.LFunction, name string, g Gate, script string, grants Grants) *lua.LFunction {
	return e.L.NewFunction(func(L *lua.LState) int {
		switch c := g(name, L); {
		case c == Unclassified:
			L.Push(lua.LNil)
			L.Push(lua.LString(fmt.Sprintf("refused: %s's effect is not known, so no grant covers it", name)))
			return 2
		case c != "" && !grants.Granted(script, c):
			L.Push(lua.LNil)
			L.Push(lua.LString(fmt.Sprintf("needs %s: not granted", c)))
			return 2
		}
		top := L.GetTop()
		L.Push(fn)
		for i := 1; i <= top; i++ {
			L.Push(L.Get(i))
		}
		prev := e.calling
		e.calling = &gatedScript{script: script, grants: grants}
		defer func() { e.calling = prev }()
		L.Call(top, lua.MultRet)
		return L.GetTop() - top
	})
}

// Caller is the gated script calling the host function running now, through its proxy: its name
// and grants, for a host function that keeps something of the script's (a key) and checks its
// grant later, when it runs. ok is false for an ungated caller. A host function gated with a Gate
// that answers "" is one that checks later: the call goes through, with Caller set.
func (e *Engine) Caller() (script string, grants Grants, ok bool) {
	if e.calling == nil {
		return "", nil, false
	}
	return e.calling.script, e.calling.grants, true
}

// owns reports whether fn is this engine's: made in its globals or in one of its gated scripts'.
func (e *Engine) owns(fn *lua.LFunction) bool { return fn.Env == e.L.G.Global || e.envs[fn.Env] != nil }

// gatedScript is a gated environment's script and grants: what its editor object asks.
type gatedScript struct {
	script string
	grants Grants
}
