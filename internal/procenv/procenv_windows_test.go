//go:build windows

package procenv

import (
	"os"
	"path/filepath"
	"testing"
)

// writeProgram writes a program named name in dir: one Windows runs, name.exe, when runnable, and a
// file of the bare name, which no PATHEXT extension matches, when not.
func writeProgram(t *testing.T, dir, name string, runnable bool) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if runnable {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
