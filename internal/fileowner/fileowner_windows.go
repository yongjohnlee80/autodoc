//go:build windows

package fileowner

import (
	"os"

	"golang.org/x/sys/windows"
)

// owner is the entry's owner SID, and whether it is this process's user's. The entry is opened
// itself (OPEN_REPARSE_POINT), a symlink not followed, for its owner alone (READ_CONTROL).
func owner(path string) (string, bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false, err
	}
	h, err := windows.CreateFile(p, windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", false, err
	}
	defer windows.CloseHandle(h)
	return handleOwner(h)
}

// ownerOfFile is the owner SID of the object f holds, and whether it is this process's user's: read
// through the handle itself, so what is checked is what f reads.
func ownerOfFile(f *os.File) (string, bool, error) { return handleOwner(windows.Handle(f.Fd())) }

// handleOwner is the owner SID of the object h holds, and whether it is this process's user's.
func handleOwner(h windows.Handle) (string, bool, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return "", false, err
	}
	sid, _, err := sd.Owner()
	if err != nil {
		return "", false, err
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", false, err
	}
	return "SID " + sid.String(), sid.Equals(u.User.Sid), nil
}
