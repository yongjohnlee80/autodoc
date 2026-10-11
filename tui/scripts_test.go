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
