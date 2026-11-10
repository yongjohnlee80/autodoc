//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// open opens the lock's file, creating it. With noFollow the path is opened as itself, not as what a
// reparse point there leads to (FILE_FLAG_OPEN_REPARSE_POINT), and refused when it is one: a symbolic
// link, a junction or any other.
func open(path string, noFollow bool) (*os.File, error) {
	if !noFollow {
		return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: errIsALink}
	}
	return os.NewFile(uintptr(h), path), nil
}

// errIsALink is a lock path that is a reparse point, which NoFollow refuses.
var errIsALink = errors.New("is a link or another reparse point")

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
