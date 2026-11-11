//go:build windows

package tui

import (
	"errors"

	"golang.org/x/sys/windows"
)

// dirHeld reports whether a handle of this process (or any) that reads, writes or deletes is open
// on dir itself (a root held on it, say): dir opened for listing with no sharing at all is refused
// while such a handle is open. A handle opened for its attributes alone doesn't conflict, so this is
// a sharing probe, not a count of every handle. A handle on a folder inside dir is on another object
// and doesn't count, as it doesn't on Linux (dirheld_other_test.go).
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
