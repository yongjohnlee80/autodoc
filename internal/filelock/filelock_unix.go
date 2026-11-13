//go:build linux || darwin

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// open opens the lock's file, creating it 0600; with noFollow a link at path is refused (ELOOP).
func open(path string, noFollow bool) (*os.File, error) {
	flags := os.O_CREATE | os.O_RDWR
	if noFollow {
		flags |= syscall.O_NOFOLLOW
	}
	return os.OpenFile(path, flags, 0o600)
}

// lock takes f's exclusive flock, waiting for it when wait is set; without wait it is held when
// another open file has it. A wait a signal interrupts is resumed.
func lock(f *os.File, wait bool) (held bool, err error) {
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
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return false, err
}

// unlock releases f's flock; the close after it would too.
func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
