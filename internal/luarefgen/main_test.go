package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheLuaReferenceIsGenerated: reference.md's generated tables are what the engine documents;
// a hand edit between the markers, or a method added without go generate ./tui, fails here.
func TestTheLuaReferenceIsGenerated(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "lua", "reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := generate(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Error("docs/lua/reference.md's tables differ from the engine's: run go generate ./tui")
	}
}

// TestGenerate: only the text between each block's markers changes, a file already current is
// left as it is, and a file missing a block is refused.
func TestGenerate(t *testing.T) {
	edStart, edEnd := markers("ed")
	textStart, textEnd := markers("text")
	doc := "# Ref\n\n" + edStart + "\nstale\n" + edEnd + "\n\nprose\n\n" + textStart + "\n" + textEnd + "\n"
	got, err := generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "| `ed:find_all(term, opts)` |") || !strings.Contains(got, "| `autodoc.text.columns(s)` |") ||
		strings.Contains(got, "stale") || !strings.HasPrefix(got, "# Ref\n\n"+edStart+"\n"+note+"\n\n| Method | Returns | Notes |\n") ||
		!strings.Contains(got, edEnd+"\n\nprose\n\n"+textStart+"\n"+note+"\n\n| Function |") {
		t.Fatalf("generated\n%s", got)
	}
	if again, err := generate(got); err != nil || again != got {
		t.Fatalf("a current file changed: %v\n%s", err, again)
	}
	if _, err := generate("# Ref\n" + edStart + "\n" + edEnd + "\n"); err == nil || !strings.Contains(err.Error(), "no "+textStart) {
		t.Errorf("a file without the text block: %v", err)
	}
}

// TestRun: -ref names the file run rewrites; a missing file and an unknown flag are refused.
func TestRun(t *testing.T) {
	edStart, edEnd := markers("ed")
	textStart, textEnd := markers("text")
	path := filepath.Join(t.TempDir(), "reference.md")
	if err := os.WriteFile(path, []byte(edStart+"\n"+edEnd+"\n"+textStart+"\n"+textEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-ref", path}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "`ed:mode()`") {
		t.Errorf("run wrote %s", got)
	}
	if err := run([]string{"-ref", filepath.Join(t.TempDir(), "none.md")}); err == nil {
		t.Error("a missing reference was rewritten")
	}
	if err := run([]string{"-nope"}); err == nil {
		t.Error("an unknown flag was taken")
	}
}
