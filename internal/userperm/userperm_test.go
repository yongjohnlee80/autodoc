package userperm

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// write makes path with data through CreatePrivate.
func write(t *testing.T, path, data string) {
	t.Helper()
	f, err := CreatePrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// read is OpenPrivate's handle read whole.
func read(t *testing.T, path string) (string, error) {
	t.Helper()
	f, err := OpenPrivate(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	return string(b), err
}

// The contract, on every OS: what CreatePrivate makes, OpenPrivate opens and reads; CreatePrivate
// refuses a file that is there, keeping os.ErrExist's identity; CreatePrivateTemp makes new private
// files; a missing file is not there, not exposed.
func TestACreatedFileOpensAndReads(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	write(t, key, "secret")
	if got, err := read(t, key); err != nil || got != "secret" {
		t.Errorf("OpenPrivate of what CreatePrivate made: %q %v, want secret", got, err)
	}
	if _, err := CreatePrivate(key); !errors.Is(err, os.ErrExist) {
		t.Errorf("CreatePrivate over a file that is there: %v, want os.ErrExist", err)
	}
	a, err := CreatePrivateTemp(dir, ".tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreatePrivateTemp(dir, ".tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	_ = b.Close()
	if a.Name() == b.Name() {
		t.Errorf("two temp files share %s", a.Name())
	}
	for _, f := range []string{a.Name(), b.Name()} {
		if _, err := read(t, f); err != nil {
			t.Errorf("a temp file %s: %v, want private", f, err)
		}
	}
	if _, err := read(t, filepath.Join(dir, "none")); !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrExposed) {
		t.Errorf("a missing file: %v, want os.ErrNotExist and not ErrExposed", err)
	}
}

// An exposed file, a folder and a link are refused with ErrExposed.
func TestExposedLinksAndFoldersAreRefused(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	write(t, key, "secret")
	exposed := filepath.Join(dir, "exposed")
	write(t, exposed, "secret")
	expose(t, exposed)
	if _, err := read(t, exposed); !errors.Is(err, ErrExposed) {
		t.Errorf("a file others may read: %v, want ErrExposed", err)
	}
	if _, err := read(t, dir); !errors.Is(err, ErrExposed) {
		t.Errorf("a folder: %v, want ErrExposed", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(key, link); err != nil {
		t.Logf("no symbolic link here (%v): the link case is not covered on this machine", err)
		return
	}
	if _, err := read(t, link); !errors.Is(err, ErrExposed) {
		t.Errorf("a symbolic link to a private file: %v, want ErrExposed (never followed)", err)
	}
}

// The handle OpenPrivate checked is the one read: a file put in the path's place afterwards is not
// what it reads.
func TestTheCheckedHandleIsTheOneRead(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	write(t, key, "checked")
	f, err := OpenPrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("swapped"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, key); err != nil {
		t.Logf("the open file can't be replaced here (%v): replacement after the check not covered", err)
		return
	}
	if b, err := io.ReadAll(f); err != nil || string(b) != "checked" {
		t.Errorf("read through the checked handle after the path was replaced: %q %v, want checked", b, err)
	}
}
