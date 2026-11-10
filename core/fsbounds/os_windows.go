//go:build windows

package fsbounds

import (
	"io/fs"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// links is the hard-link count of the file at path (GetFileInformationByHandle's NumberOfLinks): a
// Windows FileInfo carries none. A file that can't be opened to ask counts as linked, so a second
// name is never let through unseen.
func links(path string, _ os.FileInfo) uint64 {
	h, err := open(path)
	if err != nil {
		return 2
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return 2
	}
	return uint64(info.NumberOfLinks)
}

// isLink reports whether fi, an Lstat, is a link the walk resolves: a symbolic link, or any other
// reparse point, a junction among them (Go reports those as irregular).
func isLink(fi fs.FileInfo) bool { return fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 }

// realPath is an existing path as the file system names it (GetFinalPathNameByHandle): every link
// and junction on it resolved, an 8.3 short name (RUNNER~1) given its long one, each name in the
// case the disk keeps, and the drive letter its own. Two spellings of one place are then one string.
func realPath(p string) (string, error) {
	h, err := open(p)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), volumeNameDOS)
	if err != nil {
		return "", err
	}
	final := windows.UTF16ToString(buf[:n])
	if rest, ok := strings.CutPrefix(final, `\\?\UNC\`); ok {
		return `\\` + rest, nil
	}
	return strings.TrimPrefix(final, `\\?\`), nil
}

// volumeNameDOS asks GetFinalPathNameByHandle for a drive letter's path (VOLUME_NAME_DOS, with
// FILE_NAME_NORMALIZED: both are 0, and x/sys names neither).
const volumeNameDOS = 0

// open opens p, file or folder, to ask about it, following links: read nothing, share everything.
func open(p string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

// resolve is the link or junction at p resolved, all the way: realPath of it, which a link that
// leads nowhere answers with a not-found error.
func resolve(p string) (string, error) { return realPath(p) }
