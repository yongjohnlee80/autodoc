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
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// TestASocketInAFolderOthersCanWriteIsRefused: a server.socket in a folder its group or others can
// write (a sticky /tmp-like folder among them) is refused before anything binds, naming the folder,
// so nobody else can have bound the path first for a client to reach. The default-like 0700 folder
// is served by every other daemon test here.
func TestASocketInAFolderOthersCanWriteIsRefused(t *testing.T) {
	if ipc.Network != "unix" { // the seam: a named pipe's access control is its DACL, which ipc's cells check
		t.Skip("about a socket's folder: a named pipe lives in no folder")
	}
	for _, mode := range []os.FileMode{0o777 | os.ModeSticky, 0o770} {
		dir := short(t)
		shared := filepath.Join(dir, "shared")
		if err := os.Mkdir(shared, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(shared, mode); err != nil {
			t.Fatal(err)
		}
		sock := ipc.ScratchAddr(shared, "a.sock")
		// bounded: a daemon that binds there anyway serves until ctx ends, and fails the cell then
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := runServe(ctx, writeConfig(t, sock, filepath.Join(dir, "state"), ""), io.Discard, testBuild, nil, "")
		cancel()
		var unsafe *ipc.UnsafeDirError
		if !errors.As(err, &unsafe) || unsafe.Dir != shared {
			t.Fatalf("mode %v: %v, want an UnsafeDirError naming %s", mode, err, shared)
		}
		if _, err := os.Lstat(sock); !os.IsNotExist(err) {
			t.Errorf("mode %v: the socket was bound: %v", mode, err)
		}
	}
}

// TestTheDaemonServesOnlyItsUsersProcesses: the daemon closes, unanswered, a connection from
// another user's process (played: ipc.PlayUser), and logs the refusal; the control, this
// user's, is answered.
func TestTheDaemonServesOnlyItsUsersProcesses(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no peer credentials on " + runtime.GOOS)
	}
	dir := short(t)
	sock := ipc.ScratchAddr(dir, "a.sock")
	d := start(t, writeConfig(t, sock, filepath.Join(dir, "state"), ""), sock)

	restore := ipc.PlayUser(os.Getuid() + 1)
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
