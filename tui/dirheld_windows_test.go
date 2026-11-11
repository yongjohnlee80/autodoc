//go:build windows

package tui

import (
	"errors"

	"golang.org/x/sys/windows"
)

// dirHeld reports whether a handle of this process (or any) is open on dir itself: dir opened with
// no sharing at all is refused while any other handle on it is open. A handle on a folder inside dir
// is on another object and doesn't count, as it doesn't on Linux (dirheld_other_test.go).
func dirHeld(dir string) (bool, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return false, err
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, windows.CloseHandle(h)
}
