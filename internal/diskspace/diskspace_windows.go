//go:build windows

package diskspace

import (
	"os"

	"golang.org/x/sys/windows"
)

// free is GetDiskFreeSpaceEx's bytes available to this user (its quota counted) on dir's volume.
// dir must be there: a path the volume would answer for anyway is os.ErrNotExist, as on Unix.
func free(dir string) (uint64, error) {
	if _, err := os.Stat(dir); err != nil {
		return 0, err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, nil, nil); err != nil {
		return 0, err
	}
	return avail, nil
}
