package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui"
)

// The key file is checked with no UI or daemon: beside the config by default, or the file named; a
// good one is valid, a misspelled id is refused with its line; the reference lists every command.
func TestKeymapAuthoringNeedsNoDaemon(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.toml")
	file := filepath.Join(dir, "keymap.xml")
	write := func(xml string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(xml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runMain(t, nil, "--config", config, "--check-keymap"); code == 0 {
		t.Fatalf("no file beside the config was valid: %s", out)
	}
	write(`<mapping><shortcut id="autodoc.window.save"><key value="F2"/></shortcut></mapping>`)
	if code, out := runMain(t, nil, "--config", config, "--check-keymap"); code != 0 || !strings.Contains(out, file+": valid") {
		t.Fatalf("beside the config: %d %s", code, out)
	}
	write("<mapping>\n<shortcut id=\"autodoc.window.nope\"><key value=\"F2\"/></shortcut></mapping>")
	if code, out := runMain(t, nil, "--check-keymap", file); code == 0 || !strings.Contains(out, "autodoc.window.nope") || !strings.Contains(out, "line 2") {
		t.Fatalf("an unknown id: %d %s", code, out)
	}
	code, out := runMain(t, nil, "--keymap-reference")
	if code != 0 {
		t.Fatalf("reference: %d %s", code, out)
	}
	for _, c := range tui.KeyCommandsForReference() {
		if !strings.Contains(out, "`"+c.ID+"`") {
			t.Errorf("the reference lacks %s", c.ID)
		}
	}
}

// --commands-reference prints the commands AutoDoc is built with, with no UI or daemon: :open among
// them, with its aliases.
func TestTheCommandsReferenceIsPrinted(t *testing.T) {
	code, out := runMain(t, nil, "--commands-reference")
	if code != 0 || !strings.Contains(out, "| `file.open_path` | `:open`, `:e`, `:edit` |") || !strings.Contains(out, "editor.commands") {
		t.Fatalf("--commands-reference: %d %s", code, out)
	}
}
