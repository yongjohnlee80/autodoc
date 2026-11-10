//go:build linux || darwin

package userpermtest

import (
	"os"
	"testing"
)

// Expose makes path readable by others: mode 0644.
func Expose(t testing.TB, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Unreadable makes path one this user may write but not read: mode 0200. A process that reads it
// all the same (root) has no such file, and the test is skipped (unreadable).
func Unreadable(t testing.TB, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o200); err != nil {
		t.Fatal(err)
	}
	unreadable(t, path)
}

// ExposeNew is a folder whose new files would be readable by others: on Unix a file's mode is its
// creator's, never the folder's, so there is nothing to set (a create that isn't private shows as a
// mode a cell reads instead).
func ExposeNew(testing.TB, string) {}
