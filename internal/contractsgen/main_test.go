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

// TestLuaContractsAreGenerated: autodoc.nvim's contracts.lua is what this binary's contracts say,
// whole; a hand edit, or a contract changed without go generate ./rpc, fails here.
func TestLuaContractsAreGenerated(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "lua", "autodoc", "contracts.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != Lua(app.Contracts("")) {
		t.Error("lua/autodoc/contracts.lua differs from the contracts: run go generate ./rpc")
	}
}

// TestLuaString: a literal LuaJIT reads back as s: quotes, backslashes, newlines and control bytes
// escaped, UTF-8 kept.
func TestLuaString(t *testing.T) {
	for in, want := range map[string]string{
		"":             `""`,
		`say "hi"`:     `"say \"hi\""`,
		`a\b`:          `"a\\b"`,
		"two\nlines":   `"two\nlines"`,
		"tab\there":    `"tab\009here"`,
		"bell\x07\x7f": `"bell\007\127"`,
		"café →":       `"café →"`,
	} {
		if got := luaString(in); got != want {
			t.Errorf("luaString(%q) = %s, want %s", in, got, want)
		}
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

// TestRun: -agents names the file run rewrites, and a flag it does not know is refused.
func TestRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte(start+"\n"+end+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-agents", path}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "### `graph`") {
		t.Errorf("run wrote %s", got)
	}
	lua := filepath.Join(t.TempDir(), "contracts.lua")
	for range 2 { // the second run finds the file current and leaves it
		if err := run([]string{"-agents", path, "-lua", lua}); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(lua); !strings.HasPrefix(string(got), luaHeader) || !strings.Contains(string(got), `["todos.get"]`) {
			t.Fatalf("-lua wrote %.200s", got)
		}
	}
	if err := run([]string{"-agents", path, "-lua", filepath.Join(t.TempDir(), "missing", "c.lua")}); err == nil {
		t.Error("-lua into a missing directory succeeded")
	}
	if err := run([]string{"-agents", filepath.Join(t.TempDir(), "none.md")}); err == nil {
		t.Error("a missing AGENTS.md was rewritten")
	}
	if err := run([]string{"-nope"}); err == nil {
		t.Error("an unknown flag was taken")
	}
}
