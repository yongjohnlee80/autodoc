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

// A variable's name is matched as Windows matches it, case-blind: the environment's LOCALAPPDATA is
// what a caller asks for as LocalAppData, and With replaces it whatever case either is spelled in.
func TestWindowsNamesMatchWhateverTheirCase(t *testing.T) {
	e := Env{"LOCALAPPDATA=C:\\data"}
	if got := e.Get("LocalAppData"); got != `C:\data` {
		t.Errorf("Get(LocalAppData) = %q, want the LOCALAPPDATA set", got)
	}
	w := e.With("LocalAppData=D:\\other")
	if len(w) != 1 || w.Get("LOCALAPPDATA") != `D:\other` {
		t.Errorf("With(LocalAppData=…) = %q, want it to replace LOCALAPPDATA", w)
	}
}
