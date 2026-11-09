package script

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget/editor"
	lua "github.com/yuin/gopher-lua"
)

// fakeGrants is Grants in a map: script → capability → granted; requested records requires{…}.
type fakeGrants struct {
	granted   map[string]map[string]bool
	requested map[string][]string
}

func (g *fakeGrants) Granted(script, c string) bool { return g.granted[script][c] }
func (g *fakeGrants) Request(script string, caps []string) {
	g.requested[script] = append(g.requested[script], caps...)
}

func gatedEngine(t *testing.T) (*Engine, *fakeGrants, *[]string) {
	t.Helper()
	e, _, said := engine(t, "")
	// editor.secret(x) is gated by "secrets"; editor.call(verb) by what the verb's prefix says;
	// editor.box.open() and .peek() by "box", .peek free
	e.SetFunction("secret", func(L *lua.LState) int { L.Push(lua.LString("secret " + L.CheckString(1))); return 1 })
	e.SetGate("secret", func(string, *lua.LState) string { return "secrets" })
	e.SetFunction("call", func(L *lua.LState) int { L.Push(lua.LString("called " + L.CheckString(1))); return 1 })
	e.SetGate("call", func(_ string, L *lua.LState) string {
		if strings.HasPrefix(L.CheckString(1), "write.") {
			return "workspace.write"
		}
		return "workspace.read"
	})
	box := e.L.NewTable()
	e.L.SetField(box, "open", e.L.NewFunction(func(L *lua.LState) int { L.Push(lua.LString("opened")); return 1 }))
	e.L.SetField(box, "peek", e.L.NewFunction(func(L *lua.LState) int { L.Push(lua.LString("peeked")); return 1 }))
	e.L.SetField(e.Global(), "box", box)
	e.SetDomainGate("box", func(name string, _ *lua.LState) string {
		if name == "peek" {
			return ""
		}
		return "box"
	})
	g := &fakeGrants{granted: map[string]map[string]bool{}, requested: map[string][]string{}}
	return e, g, said
}

// A gated script's call without the grant answers nil and why and does nothing; granted later, the
// same function works with no reload. The gate reads the call's arguments; a domain is gated per
// function; requires{…} reaches Grants.Request.
func TestAGatedCallNeedsItsGrant(t *testing.T) {
	e, g, said := gatedEngine(t)
	err := e.LoadGated("pkg", "pkg/init.lua", []byte(`
editor.requires{"secrets", "workspace.read", 7}
editor.command{ name = "try", run = function(ed, args)
  local function say(a, b) editor.notify(tostring(a) .. "|" .. tostring(b)) end
  say(editor.secret("x"))
  say(editor.call("read.doc"))
  say(editor.call("write.doc"))
  say(editor.box.open())
  say(editor.box.peek())
end }`), g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(g.requested["pkg"], ",") != "secrets,workspace.read" {
		t.Fatalf("requested %v", g.requested)
	}
	run := func() []string {
		t.Helper()
		*said = nil
		if err := e.core.RunCommand("try"); err != nil {
			t.Fatal(err)
		}
		return *said
	}
	got := run()
	want := "nil|needs secrets: not granted,nil|needs workspace.read: not granted,nil|needs workspace.write: not granted,nil|needs box: not granted,peeked|nil"
	if strings.Join(got, ",") != want {
		t.Fatalf("ungranted:\n%s\nwant\n%s", strings.Join(got, ","), want)
	}
	g.granted["pkg"] = map[string]bool{"secrets": true, "workspace.read": true, "box": true}
	got = run()
	want = "secret x|nil,called read.doc|nil,nil|needs workspace.write: not granted,opened|nil,peeked|nil"
	if strings.Join(got, ",") != want {
		t.Fatalf("granted:\n%s\nwant\n%s", strings.Join(got, ","), want)
	}
}

// An ungated script is never asked, and has no requires.
func TestAnUngatedScriptIsNotGated(t *testing.T) {
	e, _, said := gatedEngine(t)
	if err := e.Load("mine.lua", []byte(`editor.command{ name = "mine", run = function(ed)
  editor.notify(editor.secret("y") .. " " .. editor.call("write.doc") .. " " .. editor.box.open() .. " " .. tostring(editor.requires))
end }`)); err != nil {
		t.Fatal(err)
	}
	if err := e.core.RunCommand("mine"); err != nil || strings.Join(*said, "|") != "secret y called write.doc opened nil" {
		t.Fatalf("%v %q", err, *said)
	}
}

// A gated script's globals are its own, its libraries copies, and the tables it reaches proxies: it
// changes nothing another script sees, and reads the live global table (a field added after it
// loaded). Its metatables are locked; its callbacks are the engine's, not stale.
func TestAGatedScriptIsIsolated(t *testing.T) {
	e, g, said := gatedEngine(t)
	if err := e.LoadGated("pkg", "pkg.lua", []byte(`
shared = "pkg's"
string.upper = function() return "hijacked" end
editor.text.columns = function() return -1 end
editor.mine = "mine"
editor.command{ name = "late", run = function(ed)
  editor.notify(tostring(editor.later) .. " " .. tostring(rawget(editor, "secret")) .. " " .. tostring(editor.mine))
  editor.notify(tostring(getmetatable(editor)) .. " " .. tostring(getmetatable("")) .. " " .. tostring(pcall(setmetatable, editor, {})))
  editor.notify(tostring(editor.box == editor.box) .. " " .. tostring(editor.secret == editor.secret))
  later_cb = function() editor.notify("called back") end
end }`), g); err != nil {
		t.Fatal(err)
	}
	if err := e.Load("other.lua", []byte(`editor.command{ name = "other", run = function(ed)
  editor.notify(tostring(shared) .. " " .. ("x"):upper() .. " " .. string.upper("y") .. " " .. editor.text.columns("abc") .. " " .. tostring(editor.mine))
  local mt = getmetatable(ed)
  mt.__index.value = function() return "evil" end
end }`)); err != nil {
		t.Fatal(err)
	}
	e.SetFunction("later", func(L *lua.LState) int { return 0 })
	for _, cmd := range []string{"other", "late"} {
		if err := e.core.RunCommand(cmd); err != nil {
			t.Fatal(cmd, err)
		}
	}
	want := "nil X Y 3 nil|function: |a gated table the string library false|true true"
	got := strings.Join(*said, "|")
	if !strings.HasPrefix(got, "nil X Y 3 nil|function: ") || !strings.HasSuffix(got, "|a gated table the string library false|true true") {
		t.Fatalf("got %q\nwant like %q", got, want)
	}
	// getmetatable(ed) was a copy: ed:value is still the engine's
	*said = nil
	if err := e.Load("v.lua", []byte(`editor.command{ name = "v", run = function(ed) editor.notify(ed:value()) end }`)); err != nil {
		t.Fatal(err)
	}
	_ = e.core.RunCommand("v")
	if strings.Join(*said, "|") != "" {
		t.Fatalf("ed:value was replaced through getmetatable(ed): %q", *said)
	}
	// a gated script's function is the engine's to call back
	cb, ok := e.L.GetGlobal("later_cb").(*lua.LFunction)
	if ok {
		t.Fatal("the gated script's global leaked into the shared globals")
	}
	for env := range e.envs { // the one gated script's
		cb, _ = env.RawGetString("later_cb").(*lua.LFunction)
	}
	*said = nil
	if err := e.Callback("later_cb", cb); err != nil || strings.Join(*said, "|") != "called back" {
		t.Fatalf("a gated callback: %v %q", err, *said)
	}
}

// A call its gate cannot classify is refused whatever the script was granted.
func TestAnUnclassifiedCallIsRefusedWhateverIsGranted(t *testing.T) {
	e, g, said := gatedEngine(t)
	e.SetFunction("odd", func(L *lua.LState) int { L.Push(lua.LString("ran")); return 1 })
	e.SetGate("odd", func(string, *lua.LState) string { return Unclassified })
	g.granted["pkg"] = map[string]bool{Unclassified: true, "settings": true, "workspace.write": true}
	if err := e.LoadGated("pkg", "pkg.lua", []byte(`editor.requires{"settings"}
editor.command{ name = "odd", run = function(ed) local a, b = editor.odd(); editor.notify(tostring(a) .. "|" .. tostring(b)) end }`), g); err != nil {
		t.Fatal(err)
	}
	if err := e.core.RunCommand("odd"); err != nil || strings.Join(*said, ",") != "nil|refused: odd's effect is not known, so no grant covers it" {
		t.Fatalf("%v %q", err, *said)
	}
}

// A gated script's ed:run runs any command, so it needs the commands grant, as autodoc.run does:
// refused as a value without it, run with it. The editor object an event handler gets is the
// gated script's too; an ungated script's ed:run is as it was.
func TestAGatedEdRunNeedsCommands(t *testing.T) {
	e, g, said := gatedEngine(t)
	ran := 0
	if err := e.core.Commands().Set(editor.Command{Name: "boom", Run: func(editor.CommandContext) error { ran++; return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := e.LoadGated("pkg", "pkg.lua", []byte(`
editor.command{ name = "go", run = function(ed) local a, b = ed:run("boom"); editor.notify(tostring(a) .. "|" .. tostring(b)) end }
editor.on("ping", function(ed) local a, b = ed:run("boom"); editor.notify("handler " .. tostring(a) .. "|" .. tostring(b)) end)`), g); err != nil {
		t.Fatal(err)
	}
	if err := e.core.RunCommand("go"); err != nil {
		t.Fatal(err)
	}
	e.Emit("ping", nil)
	if strings.Join(*said, ",") != "nil|needs commands: not granted,handler nil|needs commands: not granted" || ran != 0 {
		t.Fatalf("ungranted: %q, boom ran %d", *said, ran)
	}
	g.granted["pkg"] = map[string]bool{"commands": true}
	*said = nil
	if err := e.core.RunCommand("go"); err != nil || ran != 1 || strings.Join(*said, ",") != "nil|nil" {
		t.Fatalf("granted: %v %q, boom ran %d", err, *said, ran)
	}
	if err := e.Load("mine.lua", []byte(`editor.command{ name = "mine", run = function(ed) ed:run("boom"); ed:run("nope") end }`)); err != nil {
		t.Fatal(err)
	}
	if err := e.core.RunCommand("mine"); err == nil || !strings.Contains(err.Error(), "not an editor command: nope") || ran != 2 {
		t.Fatalf("an ungated ed:run: %v, boom ran %d", err, ran)
	}
}

// TestAnEventForAScriptReachesItsHandlersOnly: EmitFor reaches the handlers the gated script it
// names added, and neither another gated script's nor an ungated one's; HandlesFor says so first.
func TestAnEventForAScriptReachesItsHandlersOnly(t *testing.T) {
	e, g, said := gatedEngine(t)
	hear := func(who string) string {
		return `editor.on("grant", function(ed, info)
  editor.notify("` + who + ` " .. tostring(info.allowed) .. " " .. table.concat(info.capabilities, ","))
end)`
	}
	for _, s := range []string{"pkg", "other"} {
		if err := e.LoadGated(s, s+"/init.lua", []byte(hear(s)), g); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Load("mine.lua", []byte(hear("mine"))); err != nil {
		t.Fatal(err)
	}
	for who, want := range map[string]bool{"pkg": true, "other": true, "mine.lua": false, "nobody": false} {
		if got := e.HandlesFor(who, "grant"); got != want {
			t.Errorf("HandlesFor(%q): %v, want %v", who, got, want)
		}
	}
	for _, ev := range []struct {
		script  string
		allowed bool
		want    string
	}{{"pkg", true, "pkg true secrets,box"}, {"other", false, "other false secrets,box"}} {
		*said = nil
		if errs := e.EmitFor(ev.script, "grant", map[string]any{"capabilities": []string{"secrets", "box"}, "allowed": ev.allowed}); errs != nil {
			t.Fatal(errs)
		}
		if strings.Join(*said, ";") != ev.want {
			t.Fatalf("grant for %s: heard %q, want only %q", ev.script, *said, ev.want)
		}
	}
	*said = nil
	if errs := e.EmitFor("mine.lua", "grant", map[string]any{"capabilities": []string{"x"}, "allowed": true}); errs != nil || len(*said) != 0 {
		t.Fatalf("grant for an ungated script's name: %v, heard %q, want nothing", errs, *said)
	}
}

// TestAHandlerIsItsDefinersWhoeverRegistersIt: a function pkg defines and other registers as its
// grant handler hears pkg's events, not other's, as it runs with pkg's grants (gate.go).
func TestAHandlerIsItsDefinersWhoeverRegistersIt(t *testing.T) {
	e, g, said := gatedEngine(t)
	var kept lua.LValue = lua.LNil
	e.SetFunction("keep", func(L *lua.LState) int { kept = L.CheckFunction(1); return 0 })
	e.SetFunction("kept", func(L *lua.LState) int { L.Push(kept); return 1 })
	if err := e.LoadGated("pkg", "pkg/init.lua", []byte(`editor.keep(function(ed, info) editor.notify("pkg's function " .. tostring(info.allowed)) end)`), g); err != nil {
		t.Fatal(err)
	}
	if err := e.LoadGated("other", "other/init.lua", []byte(`editor.on("grant", editor.kept())`), g); err != nil {
		t.Fatal(err)
	}
	if !e.HandlesFor("pkg", "grant") || e.HandlesFor("other", "grant") {
		t.Fatalf("HandlesFor pkg %v, other %v: want the handler pkg's, whoever registered it",
			e.HandlesFor("pkg", "grant"), e.HandlesFor("other", "grant"))
	}
	if errs := e.EmitFor("other", "grant", map[string]any{"capabilities": []string{"x"}, "allowed": false}); errs != nil || len(*said) != 0 {
		t.Fatalf("grant for other: %v, heard %q, want nothing", errs, *said)
	}
	if errs := e.EmitFor("pkg", "grant", map[string]any{"capabilities": []string{"x"}, "allowed": true}); errs != nil || strings.Join(*said, ";") != "pkg's function true" {
		t.Fatalf("grant for pkg: %v, heard %q, want pkg's function", errs, *said)
	}
}
