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
