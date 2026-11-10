package fileowner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The contract every OS keeps: a file this process made is its user's; one another user owns is
// refused with ErrNotOurs, naming the owner; a missing path is os.ErrNotExist's.
func TestOwnedByTellsThisUsersFilesFromAnothersOwn(t *testing.T) {
	mine := filepath.Join(t.TempDir(), "mine")
	if err := os.WriteFile(mine, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OwnedBy(mine); err != nil {
		t.Errorf("OwnedBy(a file this process made): %v, want nil", err)
	}
	if err := OwnedBy(filepath.Dir(mine)); err != nil {
		t.Errorf("OwnedBy(a folder this process made): %v, want nil", err)
	}
	other := othersFile(t)
	if err := OwnedBy(other); !errors.Is(err, ErrNotOurs) {
		t.Errorf("OwnedBy(%s, another user's): %v, want ErrNotOurs", other, err)
	}
	if err := OwnedBy(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("OwnedBy(a missing path): %v, want os.ErrNotExist", err)
	}
}

// The entry itself is read, a symlink not followed: a link this user made to another user's file is
// this user's, and the link cannot lend it the target's owner either way.
func TestOwnedByReadsTheLinkNotItsTarget(t *testing.T) {
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(othersFile(t), link); err != nil {
		t.Skipf("no symlink here: %v", err)
	}
	if err := OwnedBy(link); err != nil {
		t.Errorf("OwnedBy(this user's link to another user's file): %v, want nil: the link is read, not its target", err)
	}
}

// OwnedByFile reads the owner of the object an open file holds: this process's file is its user's;
// another user's, opened, is refused with ErrNotOurs.
func TestOwnedByFileReadsTheHandlesOwner(t *testing.T) {
	mine := filepath.Join(t.TempDir(), "mine")
	if err := os.WriteFile(mine, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(mine)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := OwnedByFile(f); err != nil {
		t.Errorf("OwnedByFile(a file this process made): %v, want nil", err)
	}
	other := othersFile(t)
	g, err := os.Open(other)
	if err != nil {
		t.Logf("another user's file can't be opened here (%v): the refusal is covered by OwnedBy only", err)
		return
	}
	defer g.Close()
	if err := OwnedByFile(g); !errors.Is(err, ErrNotOurs) {
		t.Errorf("OwnedByFile(%s, another user's): %v, want ErrNotOurs", other, err)
	}
}
