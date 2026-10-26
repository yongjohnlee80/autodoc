package tui

import (
	"os"
	"testing"
)

const (
	commandsDocBegin = "<!-- the commands: generated from the catalog (go test ./tui -run TestTheCommandsReferenceIsGenerated -update) -->\n"
	commandsDocEnd   = "<!-- end of the generated commands -->\n"
)

// TestTheCommandsReferenceIsGenerated: docs/commands.md's table (--commands-reference) is the
// catalog's, every builtin and feature command, as editor.commands lists them; -update rewrites it.
func TestTheCommandsReferenceIsGenerated(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	r, err := newRegistry(h.features()...)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../docs/commands.md")
	if err != nil {
		t.Fatal(err)
	}
	want, err := replaceBlock(string(doc), commandsDocBegin, commandsDocEnd, commandsReference(r.catalog.entries()))
	if err != nil {
		t.Fatal(err)
	}
	if *updateKeys {
		if err := os.WriteFile("../docs/commands.md", []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if string(doc) != want {
		t.Error("docs/commands.md is not the catalog's: go test ./tui -run TestTheCommandsReferenceIsGenerated -update")
	}
}
