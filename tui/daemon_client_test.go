package tui

import (
	"os"
	"testing"
)

// shortDir is a directory whose socket path fits a unix socket's limit on every platform.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ado")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
