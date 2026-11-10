//go:build unix

package proglook

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
	if got := All("claude", path, work); !slices.Equal(got, want) {
		t.Errorf("All = %v, want %v", got, want)
	}
}
