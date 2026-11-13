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
