//go:build windows

package fileinuse

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Is says whether err is a file held by another program: a sharing or a lock violation.
func Is(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// ReplaceRefused says whether err, a rename over an existing file, may be that file held open by
// another program: a file in use (Is), or an access refusal, which Windows gives a rename over a
// file opened without delete sharing. A folder that isn't writable is refused the same way, so a
// caller says both.
func ReplaceRefused(err error) bool {
	return Is(err) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
