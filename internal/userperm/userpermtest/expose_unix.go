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

// ExposeNew is a folder whose new files would be readable by others: on Unix a file's mode is its
// creator's, never the folder's, so there is nothing to set (a create that isn't private shows as a
// mode a cell reads instead).
func ExposeNew(testing.TB, string) {}
