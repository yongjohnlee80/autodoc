package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/app"
)

// TestAgentsMDIsGenerated: AGENTS.md's tables are what the contracts say; a hand edit between the
// markers, or a contract changed without go generate ./rpc, fails here.
func TestAgentsMDIsGenerated(t *testing.T) {
	path := filepath.Join("..", "..", "AGENTS.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := splice(string(b), app.Contracts("").Markdown())
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Error("AGENTS.md's tables differ from the contracts: run go generate ./rpc")
	}
}

// TestRewrite: only the text between the markers changes, a file already current is left as it is,
// and a file with no markers is refused.
func TestRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	doc := "# Guide\n\nprose\n" + start + "\nstale\n" + end + "\n\nmore prose\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := rewrite(path, "| a | b |\n"); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		if want := "# Guide\n\nprose\n" + start + "\n" + note + "\n\n| a | b |\n\n" + end + "\n\nmore prose\n"; string(got) != want {
			t.Fatalf("rewrote\n%s\nwant\n%s", got, want)
		}
	}
	if err := os.WriteFile(path, []byte("no markers"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rewrite(path, "x"); err == nil || !strings.Contains(err.Error(), "no "+start) {
		t.Errorf("a file with no markers: %v", err)
	}
	if err := rewrite(filepath.Join(t.TempDir(), "none.md"), "x"); err == nil {
		t.Error("a missing file was rewritten")
	}
}
