package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/script"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// ownCore is a bare editor core with AutoDoc's own Lua commands loaded.
func ownCore(t *testing.T, text string) *widget.EditorCore {
	t.Helper()
	core := widget.NewEditorCore(widget.CoreInitialText(text))
	e := script.New(core, script.WithGlobal("autodoc"))
	t.Cleanup(e.Close)
	if errs := e.LoadDir(ownScripts, "lua"); len(errs) > 0 {
		t.Fatalf("AutoDoc's scripts: %v", errs)
	}
	return core
}

// :table 3 4 is the table Johno's lazyvim plugin made (Column<c>, Item<c>.<r>), with a five-dash
// delimiter row.
func TestTableMakesTheMarkdownTable(t *testing.T) {
	core := ownCore(t, "")
	if err := core.RunCommand("table 3 4"); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"| Column1 | Column2 | Column3 |",
		"| ----- | ----- | ----- |",
		"| Item1.1 | Item2.1 | Item3.1 |",
		"| Item1.2 | Item2.2 | Item3.2 |",
		"| Item1.3 | Item2.3 | Item3.3 |",
		"| Item1.4 | Item2.4 | Item3.4 |",
	}, "\n")
	if got := core.Value(); got != want {
		t.Fatalf("table 3 4:\n%s\nwant:\n%s", got, want)
	}
	if got := core.Cursor(); got != (widget.TextPos{Line: 0, Col: 2}) {
		t.Fatalf("the cursor is at %v, want the first header cell", got)
	}
	core.HandleKey(tuicore.KeyEvent{Code: 'u', Text: "u"})
	if core.Value() != "" {
		t.Fatalf("one undo after :table left %q", core.Value())
	}
}

// Under a line of text the table goes below it, after a blank line; bad sizes are refused.
func TestTableGoesUnderTextAndRefusesBadSizes(t *testing.T) {
	core := ownCore(t, "intro")
	if err := core.RunCommand("table 1 1"); err != nil {
		t.Fatal(err)
	}
	if got := core.Value(); got != "intro\n\n| Column1 |\n| ----- |\n| Item1.1 |" {
		t.Fatalf("under text: %q", got)
	}
	for _, line := range []string{"table 0 2", "table x 2", "table 2.5 1", "table 65 1"} {
		if err := core.RunCommand(line); err == nil {
			t.Errorf("%s was accepted", line)
		}
	}
}

func TestMermaidInsertsATemplateAndNamesTheTypes(t *testing.T) {
	core := ownCore(t, "")
	if err := core.RunCommand("mermaid flowchart"); err != nil {
		t.Fatal(err)
	}
	if got := core.Value(); !strings.HasPrefix(got, "```mermaid\nflowchart TD\n") || !strings.HasSuffix(got, "\n```") {
		t.Fatalf("mermaid flowchart: %q", got)
	}
	for _, kind := range []string{"sequence", "class", "state", "er", "gantt", "pie", "mindmap", "timeline", "journey"} {
		c := ownCore(t, "")
		if err := c.RunCommand("mermaid " + kind); err != nil || !strings.Contains(c.Value(), "```mermaid\n") {
			t.Errorf("mermaid %s: %v, %q", kind, err, c.Value())
		}
	}
	if err := core.RunCommand("mermaid"); err == nil || !strings.Contains(err.Error(), "flowchart") {
		t.Fatalf("mermaid with no type: %v; want the types listed", err)
	}
	if err := core.RunCommand("mermaid nope"); err == nil || !strings.Contains(err.Error(), "no nope template") {
		t.Fatalf("mermaid nope: %v", err)
	}
	cmd, ok := core.Commands().Lookup("mermaid")
	if !ok || !strings.Contains(cmd.Args, "flowchart") || cmd.Help == "" {
		t.Fatalf("mermaid's completion help: %+v", cmd)
	}
}

// In the running program: the user's lua folder adds commands, :table makes a table in the draft,
// and Tab in Insert mode walks its cells.
func TestTheUsersLuaCommandsAndTheTableInTheProgram(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.lua"), []byte(
		`autodoc.command{ name = "hello", help = "say hello", run = function(ed) ed:insert("hello from lua") end }`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.keys(t, key('i'))
	r.keys(t, esc())
	r.command(t, "hello")
	r.s.WaitForText(t, "hello from lua")
	r.command(t, "table 2 1")
	r.s.WaitForText(t, "| Item1.1 | Item2.1 |")
	r.keys(t, key('i'))
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab})
	r.keys(t, key('Z'))
	r.s.WaitForText(t, "| Column1 | Z |")
}

// luaTUI runs the TUI with the Lua files given, by name, in its lua folder, over a workspace whose
// a.md says text.
func luaTUI(t *testing.T, text string, files map[string]string) (*daemon, *running) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", text}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	return d, r
}

// runit runs autodoc.run on its words and writes what it answered into the page.
const runit = `
autodoc.command{ name = "mark", title = "mark the page", run = function(ed, args, line) ed:insert("<" .. line .. ">") end }
autodoc.command{ name = "runit", run = function(ed, args)
  local ok, err = autodoc.run(unpack(args))
  ed:insert("[" .. tostring(ok) .. " " .. tostring(err) .. "]")
end }
autodoc.command{ name = "badarg", run = function(ed)
  local ok, err = autodoc.run("lua.mark", {})
  local ok2, err2 = autodoc.run(42)
  ed:insert("{" .. tostring(ok) .. " " .. err .. "|" .. tostring(ok2) .. " " .. err2 .. "}")
end }
`

// TestAutodocRunRunsTheCatalog (ADR 1791762474 §5.6, §5.8): autodoc.run runs a built-in, a
// feature's and a Lua entry, and answers ok, err, never raising: unknown for an id no entry has, a
// refusal for a plugin's, and for an argument it cannot pass. A Lua entry run from outside ':'
// edits the attached editor.
func TestAutodocRunRunsTheCatalog(t *testing.T) {
	_, r := luaTUI(t, "a\n", map[string]string{"run.lua": runit})
	if e := onLoop(r, func() *Entry { return r.h.cat.lookup("lua.mark") }); e == nil || e.Source != SourceLua || e.Title != "mark the page" || e.Owner != "run.lua" {
		t.Fatalf("the Lua command's entry: %+v", e)
	}
	for line, want := range map[string]string{
		"runit lua.mark 3 4":      "[true nil]",
		"runit no.such":           "[false unknown: no.such]",
		"runit plugin.tetris.new": "[false refused: plugin.tetris.new is a plugin's command, which Lua cannot run]",
		"badarg": "{false autodoc.run: argument 1 is a table; want a string or a number|" +
			"false autodoc.run: want a command's id, a string, not number}",
	} {
		r.command(t, line)
		r.waitPage(t, want)
	}
	r.waitPage(t, "<3 4")
	// a Lua entry run as a key or a plugin runs it, off the prompt: the attached editor
	if err := onLoop(r, func() error { return r.h.cat.run("lua.mark", []string{"by", "id"}) }); err != nil {
		t.Fatal(err)
	}
	r.waitPage(t, "<by id")
	// a built-in, then a feature's (each takes the keyboard, so runit is run by its id)
	// in this order: the todos' view covers the explorer
	for _, c := range []struct{ id, want string }{{"explorer.toggle", "┌ explorer"}, {"todos.open", "todos · kb"}} {
		if err := onLoop(r, func() error { return r.h.cat.run("lua.runit", []string{c.id}) }); err != nil {
			t.Fatal(err)
		}
		r.s.WaitForText(t, c.want)
	}
}

// waitPage waits for the editor's text to hold want.
func (r *running) waitPage(t *testing.T, want string) {
	t.Helper()
	r.s.WaitFor(t, "the page holds "+want, func(string) bool { return strings.Contains(r.editorText(), want) })
}

// TestALuaCommandTakesOnlyTheNameItAsksFor (ADR 1791762474 §4.2, r2): a Lua find replaces :find and
// leaves the page's find runnable by its id; a Lua term takes :term alone, and :terminal still
// opens the terminal. A Lua id outside lua.* does not load.
func TestALuaCommandTakesOnlyTheNameItAsksFor(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir())
	_, r := luaTUI(t, "one\ntwo\n", map[string]string{"over.lua": `
autodoc.command{ name = "find", run = function(ed, args, line) ed:insert("lua find " .. line .. ";") end }
autodoc.command{ name = "term", run = function(ed) ed:insert("lua term;") end }
`, "bad.lua": `autodoc.command{ name = "bad", id = "file.bad", run = function(ed) end }`})
	r.waitNotice(t, "a Lua command did not load: bad.lua")
	r.waitNotice(t, "a Lua id is lua.<name>")
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.command(t, "find two")
	r.waitPage(t, "lua find two;")
	r.command(t, "term")
	r.waitPage(t, "lua term;")
	if r.termShown() {
		t.Fatal(":term opened the terminal")
	}
	if err := onLoop(r, func() error { return r.h.cat.run("find.term", []string{"two"}) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, `finding "two"`)
	r.command(t, "terminal")
	r.s.WaitForText(t, "┌ terminal")
}
