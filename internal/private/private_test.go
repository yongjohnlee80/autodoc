package private

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The contract, on every OS: a file Create made is private and stays so once written; Create refuses
// a file that is there; a file made readable by others is exposed, and Restrict makes it private; a
// missing file is not there, not exposed.
func TestACreatedFileIsPrivate(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	f, err := Create(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Check(key); err != nil {
		t.Errorf("a file Create made: %v, want private", err)
	}
	if _, err := Create(key); !errors.Is(err, os.ErrExist) {
		t.Errorf("Create over a file that is there: %v, want os.ErrExist", err)
	}
}

func TestAnExposedFileFailsAndRestrictMakesItPrivate(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	expose(t, key)
	if err := Check(key); !errors.Is(err, ErrExposed) {
		t.Fatalf("a file others may read: %v, want ErrExposed", err)
	}
	if err := Restrict(key); err != nil {
		t.Fatal(err)
	}
	if err := Check(key); err != nil {
		t.Errorf("after Restrict: %v, want private", err)
	}
}

func TestAMissingFileIsNotThere(t *testing.T) {
	err := Check(filepath.Join(t.TempDir(), "none"))
	if !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrExposed) {
		t.Errorf("a missing file: %v, want os.ErrNotExist and not ErrExposed", err)
	}
}
