//go:build unix

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/userperm"
)

// The token file's checks are of the file opened, and the token is read from it: a file put in the
// path's place between the open and the read is never read, whichever way round. A FIFO is refused
// at once, never waited on.
func TestTheTokenFileReadIsTheFileChecked(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	swapAfterOpen := func(path, replacement string) {
		t.Helper()
		credentialOpened = func() {
			if err := os.Remove(path); err != nil {
				t.Error(err)
			}
			if err := os.Symlink(replacement, path); err != nil {
				t.Error(err)
			}
		}
		t.Cleanup(func() { credentialOpened = func() {} })
	}

	// checked private, swapped for a link to an open file: the private token is read
	open := write("open", "tok-open\n", 0o644)
	cred := write("cred", "tok-private\n", 0o600)
	swapAfterOpen(cred, open)
	if tok, err := readEditorCredential(cred); err != nil || tok != "tok-private" {
		t.Fatalf("swapped after the open: %q %v, want the opened file's tok-private", tok, err)
	}

	// opened open, swapped for a private file: still refused, as the file opened was
	open2 := write("open2", "tok-open\n", 0o644)
	private := write("private", "tok-private\n", 0o600)
	swapAfterOpen(open2, private)
	if tok, err := readEditorCredential(open2); !errors.Is(err, userperm.ErrExposed) {
		t.Fatalf("an open file swapped for a private one: %q %v, want refused (not private)", tok, err)
	}
	credentialOpened = func() {}

	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readEditorCredential(fifo); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, userperm.ErrExposed) || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("a FIFO: %v, want refused: not a regular file", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a FIFO in the token file's place blocked the read")
	}

}
