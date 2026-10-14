package script

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
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
	if err := e.core.Commands().Set(widget.Command{Name: "boom", Run: func(widget.CommandContext) error { ran++; return nil }}); err != nil {
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
