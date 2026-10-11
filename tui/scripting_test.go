package tui

import (
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// luaLoad loads a script into the running program's engine.
func (r *running) luaLoad(t *testing.T, src string) {
	t.Helper()
	if err := onLoop(r, func() error { return r.h.scripts.Load("test.lua", []byte(src)) }); err != nil {
		t.Fatal(err)
	}
}

// luaRun runs a command line on the loop, as the prompt does; the command's error.
func (r *running) luaRun(line string) error {
	return onLoop(r, func() error { return r.h.core.RunCommand(line) })
}

// waitNamespaces waits for the daemon's verbs to be autodoc.<domain> functions.
func (r *running) waitNamespaces(t *testing.T) {
	t.Helper()
	r.s.WaitFor(t, "autodoc.search made", func(string) bool {
		return onLoop(r, func() bool {
			t, ok := r.h.scripts.Global().RawGetString("search").(*lua.LTable)
			return ok && t.RawGetString("query") != lua.LNil
		})
	})
}

// TestScriptsCallTheDaemon: a command calls the daemon through a generated namespace and through
// autodoc.call; the answers reach its callbacks on the loop, as Lua tables, and a callback's edits
// are one undo step.
func TestScriptsCallTheDaemon(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n\nkestrel notes\n"}, "other": {"b.md", "b\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	r.luaLoad(t, `
autodoc.command{ name = "ws", run = function(ed)
  autodoc.workspace.list(function(res, err)
    local names = {}
    for _, w in ipairs(res) do names[#names + 1] = w.name end
    table.sort(names)
    autodoc.notify("workspaces: " .. table.concat(names, ",") .. " err " .. tostring(err))
  end)
  autodoc.call("workspace.list", {}, function(res) autodoc.notify("by call: " .. #res) end)
end }
autodoc.command{ name = "hits", run = function(ed, args)
  autodoc.search.query("kb", args[1], {limit = 5}, function(res, err)
    if err then autodoc.notify("search failed: " .. err) return end
    ed:insert("found " .. res.hits[1].path)
    autodoc.notify("hits: " .. #res.hits .. " first " .. res.hits[1].path)
  end)
  -- a param left out at the end is a trailing nil
  autodoc.search.query("kb", args[1], nil, function(res) autodoc.notify("no opts: " .. #res.hits) end)
end }
`)
	if err := r.luaRun("ws"); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "workspaces: kb,other err nil")
	r.waitNotice(t, "by call: 2")
	before := r.editorText()
	if err := r.luaRun("hits kestrel"); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "hits: 1 first a.md")
	r.waitNotice(t, "no opts: 1")
	if got := r.editorText(); got != "found a.md"+before {
		t.Fatalf("the callback's edit: %q", got)
	}
	onLoop(r, func() bool { r.h.core.Undo(); return true })
	if got := r.editorText(); got != before {
		t.Fatalf("one undo after the callback left %q", got)
	}
}

// TestScriptCallErrorsReachTheCallback: an internal verb is refused before any call and has no
// namespace function; the daemon's errors and an unknown verb reach cb as "<code> <message>"; with
// no callback a failure is a notification; a callback that raises is one too.
func TestScriptCallErrorsReachTheCallback(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	r.luaLoad(t, `
autodoc.command{ name = "bad", run = function(ed)
  autodoc.call("sys.shutdown", {}, function(res, err) autodoc.notify("shutdown: " .. tostring(res) .. " " .. err) end)
  autodoc.call("doc.read", {"kb", "missing.md"}, function(res, err) autodoc.notify("read: " .. tostring(res) .. " " .. err) end)
  autodoc.call("no.such", nil, function(res, err) autodoc.notify("unknown: " .. err) end)
  autodoc.call("doc.read", {"kb", "gone.md"})
  autodoc.notify("namespaced: " .. tostring(autodoc.sys.shutdown) .. " " .. tostring(autodoc.sys.hello) .. " " .. type(autodoc.sys.contracts))
end }
autodoc.command{ name = "raise", run = function(ed)
  autodoc.workspace.list(function() error("it broke", 0) end)
end }
`)
	if err := r.luaRun("bad"); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "namespaced: nil nil function")
	r.waitNotice(t, "shutdown: nil sys.shutdown is the protocol's own (audience internal): scripts cannot call it")
	r.waitNotice(t, "read: nil -32061 ")
	r.waitNotice(t, "unknown: -32601 ")
	r.waitNotice(t, "autodoc.call(doc.read): -32061 ")
	if err := r.luaRun("raise"); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "a Lua callback failed: workspace.list's callback: it broke")
	// the daemon was never asked to stop
	r.luaLoad(t, `autodoc.command{ name = "alive", run = function() autodoc.workspace.list(function(res) autodoc.notify("still " .. #res) end) end }`)
	if err := r.luaRun("alive"); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "still 1")
}

// TestScriptCallArguments: a namespace function checks its arity at the call, a Lua error naming
// the params; autodoc.call takes a list; a contract's list param takes {}; before the contracts are
// known a call fetches them first, so an internal verb is still refused.
func TestScriptCallArguments(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	r.luaLoad(t, `
autodoc.command{ name = "few", run = function() autodoc.search.query("kb", function() end) end }
autodoc.command{ name = "many", run = function() autodoc.workspace.list("x", function() end) end }
autodoc.command{ name = "one", run = function() autodoc.workspace.focus() end }
autodoc.command{ name = "map", run = function() autodoc.call("workspace.list", {x = 1}) end }
autodoc.command{ name = "fn", run = function() autodoc.call("workspace.focus", {print}) end }
autodoc.command{ name = "empty", run = function()
  autodoc.workspace.set_text_extensions("kb", {}, function(_, err) autodoc.notify("extensions set: " .. tostring(err)) end)
end }
autodoc.command{ name = "early", run = function()
  autodoc.call("sys.shutdown", {}, function(_, err) autodoc.notify("early: " .. err) end)
end }
`)
	for line, want := range map[string]string{
		"few":  "autodoc.search.query takes 2 to 3 params (workspace, query, opts?), then a callback",
		"many": "autodoc.workspace.list takes no params, then a callback",
		"one":  "autodoc.workspace.focus takes 1 param (workspace), then a callback",
		"map":  "params is a list",
		"fn":   "cannot cross",
	} {
		if err := r.luaRun(line); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v; want %q", line, err, want)
		}
	}
	if err := r.luaRun("empty"); err != nil {
		t.Fatal(err)
	}
	// {} reached the daemon as an empty list: its params decoded, the fixed test daemon refuses the
	// change itself (as a map, {} is -32602 invalid params)
	r.waitNotice(t, "extensions set: -32065 ")
	onLoop(r, func() bool { r.h.scripting.verbs = nil; return true })
	if err := r.luaRun("early"); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "early: sys.shutdown is the protocol's own")
}

// TestScriptNamespacesFollowAReconnect: the namespaces are made again from the next daemon's
// contracts, in the tables a script already holds.
func TestScriptNamespacesFollowAReconnect(t *testing.T) {
	notes := map[string][]string{"kb": {"a.md", "kestrel\n"}}
	x := onARestartableDaemon(t, notes)
	x.r.waitNamespaces(t)
	x.r.luaLoad(t, `
held = autodoc.search
autodoc.command{ name = "again", run = function()
  held.query("kb", "kestrel", function(res, err) autodoc.notify("held: " .. #res.hits .. " " .. tostring(err)) end)
end }
`)
	// what the first connection made, gone: only a new definition brings it back
	onLoop(x.r, func() bool {
		return x.r.h.scripts.L.DoString(`autodoc.workspace = nil; held.query = nil`) == nil
	})
	x.restart(t, notes, true)
	x.r.waitNamespaces(t)
	x.r.s.WaitFor(t, "autodoc.workspace made again", func(string) bool {
		return onLoop(x.r, func() bool { return x.r.h.scripts.Global().RawGetString("workspace") != lua.LNil })
	})
	if err := x.r.luaRun("again"); err != nil {
		t.Fatal(err)
	}
	x.r.waitNotice(t, "held: 1 nil")
}
