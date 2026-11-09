//go:build linux || darwin

package ipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// The Unix fixture of the contract suite (peer_test.go): a unix socket, dialled as a client does.

// socketIn listens on a unix socket in a fresh short directory.
func socketIn(t *testing.T) (net.Listener, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	return ln, path
}

// rawDial connects to the socket at path without Dial's check, as a client of any kind can.
func rawDial(path string) (net.Conn, error) { return net.Dial("unix", path) }

// TestCheckDir: a socket's folder that its group or others can write, sticky or not, is refused,
// as is one of another owner and a file; a folder only its owner writes is taken.
func TestCheckDir(t *testing.T) {
	dir := t.TempDir()
	for _, mode := range []os.FileMode{0o700, 0o755, 0o750} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := CheckDir(dir); err != nil {
			t.Errorf("mode %#o: %v", mode, err)
		}
	}
	var unsafe *UnsafeDirError
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777, 0o777 | os.ModeSticky} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := CheckDir(dir); !errors.As(err, &unsafe) {
			t.Errorf("mode %v: %v, want an UnsafeDirError", mode, err)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDir(file); !errors.As(err, &unsafe) {
		t.Errorf("a file: %v, want an UnsafeDirError", err)
	}
	if os.Getuid() == 0 {
		// run as root (a container), the folder is root's, which CheckDir accepts by design: make it
		// another, unprivileged user's
		if err := os.Chown(dir, 54321, -1); err != nil {
			t.Fatal(err)
		}
	}
	playAnotherUser(t)
	if err := CheckDir(dir); !errors.As(err, &unsafe) {
		t.Errorf("another user's folder: %v, want an UnsafeDirError", err)
	}
}
