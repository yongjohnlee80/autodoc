//go:build unix

package crew

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// On Unix a program is a file with an execute bit: every one on PATH, in order and once, a
// relative folder taken from dir; a file without the bit, or a folder of the name, is none.
func TestEveryProgramOnPathInOrderOnUnix(t *testing.T) {
	work := t.TempDir()
	a, b, plain := filepath.Join(work, "a"), filepath.Join(work, "b"), filepath.Join(work, "plain")
	for _, d := range []string{a, b, plain, filepath.Join(work, "rel")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string, mode os.FileMode) {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(a, "claude"), 0o755)
	write(filepath.Join(b, "claude"), 0o755)
	write(filepath.Join(plain, "claude"), 0o644) // no execute bit: not a program
	write(filepath.Join(work, "rel", "claude"), 0o755)
	if err := os.Mkdir(filepath.Join(work, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, "dir", "claude"), 0o755); err != nil { // a folder of the name
		t.Fatal(err)
	}
	path := strings.Join([]string{plain, a, filepath.Join(work, "dir"), "rel", a, b}, string(os.PathListSeparator))
	want := []string{filepath.Join(a, "claude"), filepath.Join(work, "rel", "claude"), filepath.Join(b, "claude")}
	if got := programsOn("claude", path, work); !slices.Equal(got, want) {
		t.Errorf("programsOn = %v, want %v", got, want)
	}
}

// writeClaude writes a file at p that reads as a Claude Code knowing SessionStart, executable.
func writeClaude(t *testing.T, p string) string {
	t.Helper()
	if err := os.WriteFile(p, []byte(`padding x="SessionStart"; more`), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// On Unix a dotted name is itself: mise.sh is not mise, and claude.sh is not claude. So a shim
// linking to mise.sh is probed as the file it is, never run as mise, and a claude.sh that reads as
// no Claude Code still falls back to the claude on PATH.
func TestADottedNameIsItselfOnUnix(t *testing.T) {
	tools, shims, wrappers, later, work := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	mise, miseSh := filepath.Join(tools, "mise"), writeClaude(t, filepath.Join(tools, "mise.sh"))
	if err := os.WriteFile(mise, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isProgram(mise, "mise") || isProgram(miseSh, "mise") {
		t.Fatalf("isProgram: mise %v, mise.sh %v; want mise alone", isProgram(mise, "mise"), isProgram(miseSh, "mise"))
	}
	shim := filepath.Join(shims, "claude")
	if err := os.Symlink(miseSh, shim); err != nil {
		t.Fatal(err)
	}
	if got, k, err := ClaudeOf("claude", shims, work, ""); err != nil || got != shim || k == nil {
		t.Errorf("a claude linking to mise.sh: %q, %v, %v; want the file itself, %q, read as a Claude Code", got, k, err, shim)
	}
	if err := os.WriteFile(filepath.Join(wrappers, "claude.sh"), []byte("#!/bin/sh\nexec claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := writeClaude(t, filepath.Join(later, "claude"))
	if got, _, err := ClaudeOf("claude.sh", wrappers+string(os.PathListSeparator)+later, work, ""); err != nil || got != real {
		t.Errorf("claude.sh, a wrapper, then claude on PATH: %q, %v; want the claude it runs, %q", got, err, real)
	}
}
