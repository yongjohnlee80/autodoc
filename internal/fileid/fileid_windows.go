//go:build windows

package fileid

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// id is the file's volume serial and file index, as "serial-index", from GetFileInformationByHandle:
// NTFS's identity of a file, the same through a hardlink and, the symlink followed, through a symlink.
func id(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	// no access but its attributes, sharing everything, and a folder too (BACKUP_SEMANTICS)
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%d", info.VolumeSerialNumber, uint64(info.FileIndexHigh)<<32|uint64(info.FileIndexLow)), nil
}
