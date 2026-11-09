//go:build linux || darwin

package ipc

import (
	"context"
	"errors"
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

// Listen refuses a folder its group or others can write before anything binds, naming the folder:
// nobody else can have bound the address first for a client to reach.
func TestListenRefusesAFolderOthersCanWrite(t *testing.T) {
	for _, mode := range []os.FileMode{0o777 | os.ModeSticky, 0o770} {
		shared := filepath.Join(short(t), "shared")
		if err := os.Mkdir(shared, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(shared, mode); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(shared, "a.sock")
		ln, err := Listen(sock, nil)
		if err == nil {
			_ = ln.Close()
		}
		var unsafe *UnsafeDirError
		if !errors.As(err, &unsafe) || unsafe.Dir != shared {
			t.Fatalf("mode %v: %v, want an UnsafeDirError naming %s", mode, err, shared)
		}
		if _, err := os.Lstat(sock); !os.IsNotExist(err) {
			t.Errorf("mode %v: the socket was bound: %v", mode, err)
		}
	}
}

// A live listener's address is ErrInUse to Listen, and stays the live one's; a crashed daemon's
// socket, which nothing answers on, is taken over.
func TestListenTakesOverOnlyADeadAddress(t *testing.T) {
	dir := short(t)
	live := filepath.Join(dir, "live.sock")
	other, err := net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if ln, err := Listen(live, nil); !errors.Is(err, ErrInUse) {
		if err == nil {
			_ = ln.Close()
		}
		t.Fatalf("Listen on a live listener's socket: %v, want ErrInUse", err)
	}
	if c, err := net.Dial("unix", live); err != nil {
		t.Fatalf("the live listener lost its socket: %v", err)
	} else {
		_ = c.Close()
	}

	dead := filepath.Join(dir, "dead.sock")
	crashed, err := net.Listen("unix", dead)
	if err != nil {
		t.Fatal(err)
	}
	crashed.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = crashed.Close() // the file stays, and nothing listens
	ln, err := Listen(dead, nil)
	if err != nil {
		t.Fatalf("Listen on a crashed daemon's socket: %v, want it taken over", err)
	}
	defer ln.Close()
	if c, err := Dial(context.Background(), dead); err != nil {
		t.Fatalf("the taken-over socket does not answer: %v", err)
	} else {
		_ = c.Close()
	}
}

// Remove takes the address away while it is still the listener's, and leaves a successor's: a
// daemon shutting down never removes the socket another daemon bound since.
func TestRemoveLeavesASuccessorsAddress(t *testing.T) {
	sock := filepath.Join(short(t), "a.sock")
	ln, err := Listen(sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("Close removed the socket: %v (Remove is what takes it away)", err)
	}
	if err := os.Remove(sock); err != nil { // a successor took the path over
		t.Fatal(err)
	}
	successor, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer successor.Close()
	ln.Remove()
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("Remove took the successor's socket: %v", err)
	}

	own := filepath.Join(short(t), "b.sock")
	ln2, err := Listen(own, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ln2.Close()
	ln2.Remove()
	if _, err := os.Lstat(own); !os.IsNotExist(err) {
		t.Errorf("Remove left the listener's own socket: %v", err)
	}
}
