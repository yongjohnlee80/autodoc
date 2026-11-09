package fileid

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The contract every OS keeps: a file has one identity through every name it has (itself, a hardlink,
// a symlink to it), another file has another, and a path that is not there is os.ErrNotExist.
func TestAFileHasOneIdentityThroughEachOfItsNames(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte(p), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	idA, err := ID(a)
	if err != nil {
		t.Fatalf("ID(a): %v", err)
	}
	if again, err := ID(a); err != nil || again != idA {
		t.Errorf("ID(a) twice: %q then %q, %v; want the same", idA, again, err)
	}
	if idB, err := ID(b); err != nil || idB == idA {
		t.Errorf("ID(b) = %q, %v; want another file's own, not a's %q", idB, err, idA)
	}
	link := filepath.Join(dir, "hard")
	if err := os.Link(a, link); err != nil {
		t.Fatalf("a hardlink: %v", err)
	}
	if got, err := ID(link); err != nil || got != idA {
		t.Errorf("ID(hardlink to a) = %q, %v; want a's %q", got, err, idA)
	}
	sym := filepath.Join(dir, "sym")
	if err := os.Symlink(a, sym); err != nil {
		t.Logf("no symlink here (%v): the symlink case is not checked", err)
	} else if got, err := ID(sym); err != nil || got != idA {
		t.Errorf("ID(symlink to a) = %q, %v; want a's %q", got, err, idA)
	}
	if _, err := ID(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ID of a missing path: %v, want os.ErrNotExist", err)
	}
}
