//go:build unix

package app

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The token file's checks are of the file opened, and the token is read from it: a file put in the
// path's place between the open and the read is never read, whichever way round. A FIFO is refused
// at once, never waited on, and a file longer than a token is refused.
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
	if tok, err := readEditorCredential(open2); err == nil || !strings.Contains(err.Error(), "must be 0600") {
		t.Fatalf("an open file swapped for a private one: %q %v, want refused (must be 0600)", tok, err)
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
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("a FIFO: %v, want refused: not a regular file", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a FIFO in the token file's place blocked the read")
	}

	if _, err := readEditorCredential(write("long", strings.Repeat("x", 257), 0o600)); err == nil || !strings.Contains(err.Error(), "not a token") {
		t.Fatalf("257 bytes: %v, want refused", err)
	}
	if tok, err := readEditorCredential(write("full", strings.Repeat("y", 256), 0o600)); err != nil || len(tok) != 256 {
		t.Fatalf("256 bytes: %d %v, want the token", len(tok), err)
	}
}
