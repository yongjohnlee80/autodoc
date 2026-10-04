//go:build unix

package tui

import (
	"errors"
	"os"
	"syscall"
)

// lockHandoff holds the handoff's lock file (path + ".lock") exclusively until the returned
// release: a write's rename and a removal's read, check and unlink each run whole under it. With
// wait false it refuses at once (errHandoffBusy) when another holds it. The lock file is left in
// place: removing it would let two holders lock two files.
func lockHandoff(path string, wait bool) (release func(), err error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	for {
		err = syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errHandoffBusy
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
