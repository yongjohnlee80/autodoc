//go:build linux || darwin

package filelock

import (
	"errors"
	"os"
	"syscall"
)

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
