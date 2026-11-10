//go:build windows

package proglook

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// On Windows a program is a file with one of PATHEXT's extensions: claude.cmd and claude.exe are
// claude, each folder's in PATHEXT's order; a claude.txt, or a bare claude, is none.
func TestEveryProgramOnPathInOrderOnWindows(t *testing.T) {
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	work := t.TempDir()
	cmd, exe, both, none := filepath.Join(work, "cmd"), filepath.Join(work, "exe"), filepath.Join(work, "both"), filepath.Join(work, "none")
	for _, d := range []string{cmd, exe, both, none} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(cmd, "claude.cmd"), filepath.Join(exe, "claude.exe"), filepath.Join(both, "claude.cmd"),
		filepath.Join(both, "claude.exe"), filepath.Join(none, "claude.txt"), filepath.Join(none, "claude")} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := strings.Join([]string{none, cmd, exe, both, cmd}, string(os.PathListSeparator))
	got := All("claude", path, work)
	want := []string{filepath.Join(cmd, "claude.cmd"), filepath.Join(exe, "claude.exe"), filepath.Join(both, "claude.exe")}
	if len(got) != len(want) {
		t.Fatalf("All = %v, want %v", got, want)
	}
	for i := range want {
		if !strings.EqualFold(got[i], want[i]) {
			t.Errorf("All[%d] = %s, want %s (all: %v)", i, got[i], want[i], got)
		}
	}
	if slices.ContainsFunc(got, func(p string) bool { return strings.EqualFold(filepath.Ext(p), ".txt") }) {
		t.Errorf("a .txt was taken for a program: %v", got)
	}
}
