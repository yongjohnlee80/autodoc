package app

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/peercred"
)

// TestASocketInAFolderOthersCanWriteIsRefused: a server.socket in a folder its group or others can
// write (a sticky /tmp-like folder among them) is refused before anything binds, naming the folder,
// so nobody else can have bound the path first for a client to reach. The default-like 0700 folder
// is served by every other daemon test here.
func TestASocketInAFolderOthersCanWriteIsRefused(t *testing.T) {
	for _, mode := range []os.FileMode{0o777 | os.ModeSticky, 0o770} {
		dir := short(t)
		shared := filepath.Join(dir, "shared")
		if err := os.Mkdir(shared, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(shared, mode); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(shared, "a.sock")
		// bounded: a daemon that binds there anyway serves until ctx ends, and fails the cell then
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := runServe(ctx, writeConfig(t, sock, filepath.Join(dir, "state"), ""), io.Discard, testBuild, nil)
		cancel()
		var unsafe *peercred.UnsafeDirError
		if !errors.As(err, &unsafe) || unsafe.Dir != shared {
			t.Fatalf("mode %v: %v, want an UnsafeDirError naming %s", mode, err, shared)
		}
		if _, err := os.Lstat(sock); !os.IsNotExist(err) {
			t.Errorf("mode %v: the socket was bound: %v", mode, err)
		}
	}
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

// TestTheDaemonServesOnlyItsUsersProcesses: the daemon closes, unanswered, a connection from
// another user's process (played: peercred.PlayUser), and logs the refusal; the control, this
// user's, is answered.
func TestTheDaemonServesOnlyItsUsersProcesses(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no peer credentials on " + runtime.GOOS)
	}
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	d := start(t, writeConfig(t, sock, filepath.Join(dir, "state"), ""), sock)

	restore := peercred.PlayUser(os.Getuid() + 1)
	defer restore()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("another user's connection read %d, %v; want it closed unanswered (EOF)", n, err)
	}
	eventually(t, "the refusal in the log", func() bool { return strings.Contains(d.out.String(), "refused a connection") })

	restore()
	call(t, dial(t, sock), "workspace.list") // the control: this user's connection is served
}
