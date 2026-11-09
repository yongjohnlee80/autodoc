//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lock takes f's exclusive lock (LockFileEx on its first byte), waiting for it when wait is set;
// without wait it is held when another handle has it.
func lock(f *os.File, wait bool) (held bool, err error) {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !wait {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}

// unlock releases f's lock at once (UnlockFileEx). Closing the handle releases it as well, but
// Windows may do that lazily, as it does for a process that ends holding one.
func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
