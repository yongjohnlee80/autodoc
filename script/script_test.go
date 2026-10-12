package script

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

func engine(t *testing.T, text string) (*Engine, *widget.EditorCore, *[]string) {
	t.Helper()
	core := widget.NewEditorCore(widget.CoreInitialText(text))
	var said []string
	e := New(core, WithNotify(func(s string) { said = append(said, s) }), WithTimeout(300*time.Millisecond))
	t.Cleanup(e.Close)
	return e, core, &said
}

func undo(c *widget.EditorCore) { c.HandleKey(tui.KeyEvent{Code: 'u', Text: "u"}) }

// A user's script reaches the editor through ed, replaces a built-in of the same name, and is one
// undo step.
func TestAUsersScriptEditsThroughEd(t *testing.T) {
	e, core, said := engine(t, "hello world\nsecond")
	fsys := fstest.MapFS{"lua/upper.lua": {Data: []byte(`
editor.command{ name = "upper", aliases = {"up"}, help = "upper-case a line", run = function(ed, args, line)
  local n = tonumber(args[1]) or ed:cursor()
  ed:replace(n, 1, n, #ed:line(n) + 1, ed:line(n):upper())
  local l, c = ed:cursor()
  editor.notify("upper " .. n .. " at " .. l .. "," .. c .. " of " .. ed:line_count())
end }
editor.command{ name = "table", run = function(ed) ed:insert("mine") end }
`)}}
	if errs := e.LoadDir(fsys, "lua"); len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := core.RunCommand("up 2"); err != nil {
		t.Fatal(err)
	}
	if core.Value() != "hello world\nSECOND" || len(*said) != 1 || (*said)[0] != "upper 2 at 2,6 of 2" {
		t.Fatalf("text %q, said %v", core.Value(), *said)
	}
	// a script's command replaces the editor's own of the same name
	if errs := e.LoadDir(fstest.MapFS{"x/w.lua": {Data: []byte(`editor.command{ name = "w", run = function(ed) ed:insert("mine") end }`)}}, "x"); len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := core.RunCommand("w"); err != nil || !strings.Contains(core.Value(), "mine") {
		t.Fatalf("the script's w did not replace the editor's: %v %q", err, core.Value())
	}
}

// The ed object reads, finds and runs commands.
func TestEdReadsFindsAndRuns(t *testing.T) {
	e, core, said := engine(t, "alpha\nbeta gamma\nbeta")
	fsys := fstest.MapFS{"lua/probe.lua": {Data: []byte(`
editor.command{ name = "probe", run = function(ed)
  local l1, c1, l2, c2 = ed:find("beta")
  local lines = ed:lines()
  editor.notify(table.concat({l1, c1, l2, c2, #lines, ed:text(2, 6, 2, 11), tostring(ed:selection())}, " "))
  ed:run("3")
  local l = ed:cursor()
  editor.notify("line " .. l)
end }
`)}}
	if errs := e.LoadDir(fsys, "lua"); len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := core.RunCommand("probe"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*said, "|") != "2 1 2 5 3 gamma nil|line 3" {
		t.Fatalf("said %q", *said)
	}
}

// A script's error and a runaway script are the command's errors; the editor is untouched and
// usable after.
func TestAFailingOrRunawayScriptIsAnError(t *testing.T) {
	e, core, _ := engine(t, "keep")
	fsys := fstest.MapFS{"lua/bad.lua": {Data: []byte(`
editor.command{ name = "boom", run = function(ed) ed:insert("x"); error("it broke", 0) end }
editor.command{ name = "spin", run = function(ed) while true do end end }
`)}}
	if errs := e.LoadDir(fsys, "lua"); len(errs) > 0 {
		t.Fatal(errs)
	}
	err := core.RunCommand("boom")
	if err == nil || !strings.Contains(err.Error(), "boom: it broke") {
		t.Fatalf("boom = %v", err)
	}
	undo(core) // the edit before the error is one undo away
	start := time.Now()
	err = core.RunCommand("spin")
	if err == nil || !strings.Contains(err.Error(), "stopped") || time.Since(start) > 2*time.Second {
		t.Fatalf("spin = %v after %v", err, time.Since(start))
	}
	if err := e.Load("after.lua", []byte(`editor.command{ name = "ok", run = function(ed) ed:insert("!") end }`)); err != nil {
		t.Fatal(err)
	}
	if err := core.RunCommand("ok"); err != nil || core.Value() != "!keep" {
		t.Fatalf("the engine after a runaway: %v, %q", err, core.Value())
	}
}

// A script cannot reach files, the OS or other modules; a broken file is one error and the others
// still load.
func TestScriptsHaveNoFileOrOSAccess(t *testing.T) {
	e, core, said := engine(t, "")
	fsys := fstest.MapFS{
		"lua/a_broken.lua": {Data: []byte(`this is not lua`)},
		"lua/b_probe.lua": {Data: []byte(`
editor.command{ name = "probe", run = function(ed)
  editor.notify(tostring(os) .. " " .. tostring(io) .. " " .. tostring(dofile) .. " " .. tostring(require) .. " " .. tostring(load))
end }`)},
		"lua/notes.txt": {Data: []byte(`not a script`)},
	}
	errs := e.LoadDir(fsys, "lua")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "a_broken.lua") {
		t.Fatalf("errors: %v", errs)
	}
	if err := core.RunCommand("probe"); err != nil {
		t.Fatal(err)
	}
	if (*said)[0] != "nil nil nil nil nil" {
		t.Fatalf("a script sees %q", (*said)[0])
	}
	if errs := e.LoadDir(fsys, "missing"); errs != nil {
		t.Fatalf("a missing folder: %v", errs)
	}
}

// A host names the global table and adds functions of its own to it.
func TestAHostNamesTheGlobalAndAddsFunctions(t *testing.T) {
	core := widget.NewEditorCore(widget.CoreInitialText("x"))
	e := New(core, WithGlobal("autodoc"))
	defer e.Close()
	e.SetFunction("workspace", func(L *lua.LState) int { L.Push(lua.LString("kb")); return 1 })
	err := e.Load("w.lua", []byte(`autodoc.command{ name = "ws", run = function(ed) ed:insert(autodoc.workspace()) end }`))
	if err != nil {
		t.Fatal(err)
	}
	if err := core.RunCommand("ws"); err != nil || core.Value() != "kbx" {
		t.Fatalf("ws = %v; text %q", err, core.Value())
	}
	if err := e.Load("e.lua", []byte(`editor.command{ name = "nope", run = function() end }`)); err == nil {
		t.Fatal("the default global exists under another name")
	}
}

// TestAHostsRegistrarTakesTheCommands: with a registrar, command{…} goes to it, not the editor's
// registry: its id (lua.<name> unless it names one), its title (its help, else its name), the file
// that declared it, and a Run that edits the core it is given, or the engine's for none. The
// registrar's refusal is the script's error.
func TestAHostsRegistrarTakesTheCommands(t *testing.T) {
	e, core, _ := engine(t, "abc")
	var got []Registration
	e.SetRegistrar(func(r Registration) error {
		if r.Command.Name == "bad" {
			return errors.New("refused here")
		}
		got = append(got, r)
		return nil
	})
	fsys := fstest.MapFS{"lua/a.lua": {Data: []byte(`
editor.command{ name = "up", help = "upper", run = function(ed, args, line) ed:insert(line .. "!") end }
editor.command{ name = "x", id = "lua.mine", title = "my x", run = function(ed) end }
`)}, "lua/b.lua": {Data: []byte(`editor.command{ name = "bad", run = function(ed) end }`)}}
	errs := e.LoadDir(fsys, "lua")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "refused here") {
		t.Fatalf("the registrar's refusal: %v", errs)
	}
	if len(got) != 2 || got[0].ID != "lua.up" || got[0].Title != "upper" || got[0].File != "lua/a.lua" ||
		got[1].ID != "lua.mine" || got[1].Title != "my x" {
		t.Fatalf("registered %+v", got)
	}
	if _, ok := core.Commands().Lookup("up"); ok {
		t.Fatal("the editor's registry got a command the registrar took")
	}
	// run with no core, as a host's key would: the engine's editor
	if err := got[0].Command.Run(widget.CommandContext{Line: "x y"}); err != nil || core.Value() != "x y!abc" {
		t.Fatalf("run with no core: %v, %q", err, core.Value())
	}
	other := widget.NewEditorCore()
	if err := got[0].Command.Run(widget.CommandContext{Core: other, Line: "z"}); err != nil || other.Value() != "z!" {
		t.Fatalf("run on another core: %v, %q", err, other.Value())
	}
}
