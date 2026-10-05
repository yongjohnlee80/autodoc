package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLeaseInfoRoundTripsUnderEverySpellingOfTheStore: the record written for a store is read back
// whole through a symlink to it, and is the store's identity's.
func TestLeaseInfoRoundTripsUnderEverySpellingOfTheStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "autodoc.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	id, err := Identity(path)
	if err != nil {
		t.Fatal(err)
	}
	if lid, err := Identity(link); err != nil || lid != id {
		t.Fatalf("Identity through a symlink = %q, %v; want %q", lid, err, id)
	}
	li := LeaseInfo{StoreID: id, StorePath: path, Addr: "/run/a.sock", PID: 7, Instance: "i1", Version: "v",
		Protocol: 13, MinProtocol: 12, Since: time.Unix(1, 0).UTC()}
	if err := WriteLeaseInfo(path, li); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLeaseInfo(link)
	if err != nil || got != li {
		t.Fatalf("ReadLeaseInfo through the symlink = %+v, %v; want %+v", got, err, li)
	}
	if fi, err := os.Stat(filepath.Join(dir, ".autodoc-lease-"+id+".json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the record is beside the store, 0600: %v, %v", fi, err)
	}
}

// TestLeaseInfoForAMissingStoreIsNotThere: a client's reads and the daemon's write never create a
// store, and say it is not there.
func TestLeaseInfoForAMissingStoreIsNotThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	if _, err := Identity(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Identity = %v, want ErrNotExist", err)
	}
	if _, err := ReadLeaseInfo(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadLeaseInfo = %v, want ErrNotExist", err)
	}
	if err := WriteLeaseInfo(path, LeaseInfo{}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("WriteLeaseInfo = %v, want ErrNotExist", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the store was created: %v", err)
	}
	// the lease's own path creates it, and refuses a directory that is not there
	if _, err := leaseLockPath(filepath.Join(t.TempDir(), "no-such-dir", "x.db")); err == nil {
		t.Error("leaseLockPath in a missing directory succeeded")
	}
}

// TestLeaseInfoThatIsNotJSONIsAnError: a damaged record is reported, naming it, never read as empty.
func TestLeaseInfoThatIsNotJSONIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "autodoc.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	id, _ := Identity(path)
	if err := os.WriteFile(filepath.Join(dir, ".autodoc-lease-"+id+".json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLeaseInfo(path); err == nil {
		t.Fatal("a damaged record was read")
	}
}

// TestRemoveLeaseInfoTakesOnlyItsOwnRecord: a daemon shutting down removes the record it wrote, and
// leaves a successor's; a record already gone is not an error.
func TestRemoveLeaseInfoTakesOnlyItsOwnRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "autodoc.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteLeaseInfo(path, LeaseInfo{Instance: "successor"}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLeaseInfo(path, "me"); err != nil {
		t.Fatal(err)
	}
	if li, err := ReadLeaseInfo(path); err != nil || li.Instance != "successor" {
		t.Fatalf("another instance's record was touched: %+v, %v", li, err)
	}
	if err := RemoveLeaseInfo(path, "successor"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLeaseInfo(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the record outlived its instance: %v", err)
	}
	if err := RemoveLeaseInfo(path, "successor"); err != nil {
		t.Fatalf("removing a record already gone: %v", err)
	}
}

// TestLeaseInfoInADirectoryItCannotWriteIsAnError: the write and the removal report a directory
// they cannot change, and the write leaves no temporary file behind.
func TestLeaseInfoInADirectoryItCannotWriteIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only directory")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "autodoc.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteLeaseInfo(path, LeaseInfo{Instance: "me"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := WriteLeaseInfo(path, LeaseInfo{Instance: "me"}); err == nil {
		t.Error("a write into a read-only directory succeeded")
	}
	if err := RemoveLeaseInfo(path, "me"); err == nil {
		t.Error("a removal from a read-only directory succeeded")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" && e.Name() != "autodoc.db" {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}
