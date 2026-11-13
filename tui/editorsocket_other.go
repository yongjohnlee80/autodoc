//go:build !unix

package tui

import (
	"errors"
	"net"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// listenEditor listens on a named pipe of the user's alone: ipc's address for dir and name, its
// access list and its guard, as the daemon's. A pipe lives in no folder and goes with its last
// handle, so there is no folder to make private and nothing a killed run leaves to sweep.
func listenEditor(dir, name string) (net.Listener, string, error) {
	addr := ipc.AddrIn(dir, name)
	ln, err := ipc.Listen(addr, nil)
	if err != nil {
		return nil, "", err
	}
	return ln, addr, nil
}

// unlistenEditor: a pipe leaves nothing behind once closed.
func unlistenEditor(string) {}

// privateDir: a folder made the user's alone is the unix socket's lock; a pipe has its access list.
func privateDir(string) error { return errors.New("the editor socket's folder lock is unix's") }

// sweepEditorSockets: a pipe goes with its last handle, so none is left to sweep.
func sweepEditorSockets(string) {}
