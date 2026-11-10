//go:build linux || darwin

package procenv

import (
	"os"
	"path/filepath"
	"testing"
)

// writeProgram writes a program named name in dir, one that may be run when runnable.
func writeProgram(t *testing.T, dir, name string, runnable bool) string {
	t.Helper()
	mode := os.FileMode(0o644)
	if runnable {
		mode = 0o755
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// On Unix a program's name is its file's whole base name: mise.sh is not mise, Claude not claude.
func TestAProgramsNameIsItsWholeBaseNameOnUnix(t *testing.T) {
	for file, want := range map[string]string{"claude": "claude", "/usr/bin/mise": "mise", "mise.sh": "mise.sh", "./bin/Claude": "Claude"} {
		if got := ProgramName(nil, file); got != want {
			t.Errorf("ProgramName(%q) = %q, want %q", file, got, want)
		}
	}
}
