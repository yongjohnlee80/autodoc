package daemon

import (
	"errors"

	"golang.org/x/sys/windows"
)

// heldOpen reports whether err is Windows refusing a change to a file or folder another handle
// holds open without FILE_SHARE_DELETE: a served root, which the daemon holds while it serves it.
func heldOpen(err error) bool { return errors.Is(err, windows.ERROR_SHARING_VIOLATION) }
