//go:build linux || darwin

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// short is a temporary directory with a path short enough for a unix socket.
func short(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "ad")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// TestTheSocketIsBornPrivate: the socket file is created with no group or other bits by the bind
// itself, whatever the process's umask, not opened wider and narrowed by the chmod after it.
func TestTheSocketIsBornPrivate(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	sock := filepath.Join(short(t), "a.sock")
	ln, err := listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fi, err := os.Lstat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("the socket as bound is %#o, want its owner's alone", perm)
	}
	if now := syscall.Umask(0); now != 0 {
		t.Errorf("the bind left the umask %#o, want the process's own (0) back", now)
	}
}

// TestLiveSocketIsNotReplaced: a socket something listens on is never taken over, even when the
// dial fails for another reason than "no listener" (here, permission denied), nor is a path that is
// not a socket.
func TestLiveSocketIsNotReplaced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root connects through a mode-000 socket")
	}
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	live, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := os.Chmod(sock, 0); err != nil {
		t.Fatal(err)
	}
	ln, err := listen(sock)
	if err == nil {
		_ = ln.Close()
		t.Fatal("listen took over a live socket")
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		t.Fatal(err)
	}
	c, derr := net.Dial("unix", sock)
	if derr != nil {
		t.Fatalf("the live listener lost its socket: %v", derr)
	}
	_ = c.Close()

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ln, err := listen(plain); err == nil {
		_ = ln.Close()
		t.Error("listen replaced a file that is not a socket")
	}
	if b, err := os.ReadFile(plain); err != nil || string(b) != "not a socket" {
		t.Errorf("the file was touched: %q, %v", b, err)
	}
}
