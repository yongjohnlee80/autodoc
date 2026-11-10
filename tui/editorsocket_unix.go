//go:build unix

package tui

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// listenEditor listens at dir/name, guarded: the folder is the user's alone before anything listens
// in it (privateDir), a socket left by an earlier run with this pid and those of the runs that were
// killed are removed first, and the socket's own 0600 after Listen is a second lock, not the first.
func listenEditor(dir, name string) (net.Listener, string, error) {
	path := ipc.AddrIn(dir, name)
	if err := privateDir(dir); err != nil {
		return nil, "", err
	}
	_ = os.Remove(path)
	sweepEditorSockets(dir)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	// another user's process is closed unanswered; the folder already keeps it out
	return ipc.Guard(ln, nil), path, nil
}

// unlistenEditor removes the socket's file once it is closed.
func unlistenEditor(path string) { _ = os.Remove(path) }

// privateDir makes dir the user's alone before the editor socket goes in it: a real folder, not a
// link, owned by this user, mode 0700. A folder that was there with a wider mode is narrowed; one of
// another owner, or a link, is refused, and the socket is not served.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not this user's own", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// pidSocket is an editor socket's name, <pid>.sock: the only files the sweep looks at.
var pidSocket = regexp.MustCompile(`^[0-9]+\.sock$`)

// sweepEditorSockets removes the sockets in dir that nothing listens on: a TUI that was killed never
// removed its own, and the folder keeps them. A socket is removed only when a connection to it is
// refused; one that answers, or fails any other way (a full backlog, a timeout), is left, and so is
// a file that is not a socket (connect refuses one too) and a name that is not <pid>.sock.
func sweepEditorSockets(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.Type() != os.ModeSocket || !pidSocket.MatchString(e.Name()) {
			continue
		}
		c, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			_ = c.Close()
			continue
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			_ = os.Remove(path)
		}
	}
}
