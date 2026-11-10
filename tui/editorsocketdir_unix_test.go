//go:build unix

package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// The editor socket's folder is made private (0700), and a link is never taken for it. Unix's: on
// Windows the editor socket is a named pipe, with no folder to lock (editorsocket_other.go).
func TestTheEditorSocketsFolderIsPrivateAndNoLink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "editor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("a wide folder was left %v", fi.Mode().Perm())
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(link); err == nil {
		t.Fatal("a link was taken for the socket's folder")
	}
}
