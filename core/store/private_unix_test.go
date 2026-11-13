//go:build !windows

package store

import (
	"fmt"
	"os"
	"testing"
)

// expose gives others read access to the file at path, as a file written 0644 has.
func expose(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

// makePrivate gives the file at path to this user alone: mode 0600.
func makePrivate(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// privateFile is nil when the file at path is this user's alone, as the store makes its secrets:
// mode 0600.
func privateFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s is mode %v, want 0600", path, fi.Mode().Perm())
	}
	return nil
}
