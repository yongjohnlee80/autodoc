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
