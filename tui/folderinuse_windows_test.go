//go:build windows

package tui

import (
	"errors"

	"golang.org/x/sys/windows"
)

// folderInUse reports whether err is a removal the OS refused because a process holds the folder
// (a running process's working folder, a handle open in it): Windows refuses it, a sharing
// violation.
func folderInUse(err error) bool { return errors.Is(err, windows.ERROR_SHARING_VIOLATION) }
