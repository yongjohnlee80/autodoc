package script

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE CONFORMANCE OF package script (M10): it is AutoDoc's engine and anyone's. It imports nothing of
// AutoDoc's, so auto-rag and other programs import it with golib alone; and a script written for
// AutoDoc that uses the engine's own API (command, ed, notify, text, now, date, on) runs unchanged on
// a bare golib editor: AutoDoc's own table.lua and mermaid.lua, read from tui/lua, are run here.

// TestScriptImportsNothingOfAutoDocs: no file of the package, its tests included, imports another
// AutoDoc package.
func TestScriptImportsNothingOfAutoDocs(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no files: %v", err)
	}
	for _, f := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(p, "github.com/yongjohnlee80/autodoc") {
				t.Errorf("%s imports %s: package script is anyone's, it imports golib and gopher-lua only", f, p)
			}
		}
	}
}

// TestAutoDocsScriptsRunOnABareEditor: AutoDoc's embedded table.lua and mermaid.lua load into an
// engine over a golib Editor with no AutoDoc around it, under the global AutoDoc gives them, and
// their commands edit as they do in AutoDoc.
func TestAutoDocsScriptsRunOnABareEditor(t *testing.T) {
	ed := widget.NewEditor(widget.WithInitialText("Intro"))
	core := ed.Core()
	e := New(core, WithGlobal("autodoc"))
	t.Cleanup(e.Close)
	for _, name := range []string{"table.lua", "mermaid.lua"} {
		src, err := os.ReadFile(filepath.Join("..", "tui", "lua", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Load(name, src); err != nil {
			t.Fatalf("%s does not load on a bare editor: %v", name, err)
		}
	}
	if err := core.RunCommand("table 2 1"); err != nil {
		t.Fatal(err)
	}
	if got := core.Value(); got != "Intro\n\n| Column1 | Column2 |\n| ----- | ----- |\n| Item1.1 | Item2.1 |" {
		t.Fatalf(":table 2 1 on a bare editor: %q", got)
	}
	core.SetValue("")
	if err := core.RunCommand("mermaid flowchart"); err != nil {
		t.Fatal(err)
	}
	if got := core.Value(); !strings.HasPrefix(got, "```mermaid\nflowchart TD\n") || !strings.HasSuffix(got, "\n```") {
		t.Fatalf(":mermaid flowchart on a bare editor: %q", got)
	}
	// its completion works there too: golib's prompt reads the same Complete
	c, _ := core.Commands().Lookup("mermaid")
	if c.Complete == nil || !strings.Contains(strings.Join(c.Complete(widget.CompleteContext{Core: core}), " "), "sequence") {
		t.Fatal(":mermaid offers no types on a bare editor")
	}
}
