//go:build windows

package fileinuse

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// A file another handle holds open (os.Open shares no delete) refuses its removal, and that refusal
// is a file in use; a refusal of another kind is not.
func TestAFileHeldOpenIsInUse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "held.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = os.Remove(p)
	if err == nil {
		t.Skip("this filesystem removes a file held open: nothing to tell apart")
	}
	if !Is(fmt.Errorf("remove: %w", err)) {
		t.Errorf("the removal of a held file: %v; want it read as a file in use", err)
	}
	if Is(&fs.PathError{Op: "open", Path: p, Err: windows.ERROR_ACCESS_DENIED}) || Is(fs.ErrNotExist) {
		t.Error("an access refusal, or a missing file, read as a file in use")
	}
}

// A rename over a file another handle holds open (os.Open shares no delete) is refused, and that
// refusal is a replace refused; a refusal of another kind, or a missing file, is not.
func TestARenameOverAFileHeldOpenIsAReplaceRefused(t *testing.T) {
	dir := t.TempDir()
	held, part := filepath.Join(dir, "held.csv"), filepath.Join(dir, "held.csv.part")
	for _, p := range []string{held, part} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(held)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = os.Rename(part, held)
	if err == nil {
		t.Skip("this filesystem renames over a file held open: nothing to tell apart")
	}
	if !ReplaceRefused(err) {
		t.Errorf("the rename over a held file: %v; want it read as a replace refused", err)
	}
	if ReplaceRefused(fs.ErrNotExist) || ReplaceRefused(windows.ERROR_FILE_NOT_FOUND) {
		t.Error("a missing file read as a replace refused")
	}
}
