package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// TestTheSocketIsBornPrivate: the socket file is created 0600 by the bind itself, whatever the
// process's umask, not opened wider and narrowed by the chmod after it.
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
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the socket as bound is %#o, want 0600", perm)
	}
	if now := syscall.Umask(0); now != 0 {
		t.Errorf("the bind left the umask %#o, want the process's own (0) back", now)
	}
}
