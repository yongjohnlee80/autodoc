//go:build windows

package userperm

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// openNoFollow opens path for reading and for its security (READ_CONTROL), the entry itself: a
// reparse point (a symbolic link, a junction) is not followed, and regular then refuses it. A folder
// opens too (FILE_FLAG_BACKUP_SEMANTICS, as os.Open opens one), so regular refuses it as exposed
// rather than the open failing with access denied. A backup privilege the flag may honour lets
// nothing pass: exposure reads the DACL itself, through the handle.
func openNoFollow(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL,
		windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
		}
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// regular is nil when f holds a regular file: no reparse point, no folder, no device or pipe.
func regular(f *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	switch a := info.FileAttributes; {
	case a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return fmt.Errorf("%w: it is a reparse point (a link or a junction), which a private file never is", ErrExposed)
	case a&windows.FILE_ATTRIBUTE_DIRECTORY != 0:
		return fmt.Errorf("%w: it is a folder, not a regular file", ErrExposed)
	}
	t, err := windows.GetFileType(windows.Handle(f.Fd()))
	if err != nil {
		return err
	}
	if t != windows.FILE_TYPE_DISK {
		return fmt.Errorf("%w: it is no regular file (type %d)", ErrExposed, t)
	}
	return nil
}

// exposure is what exposes the file f holds, "" when nothing does, read from its DACL through the
// handle: no DACL at all (everyone, everything); an allow, inherited or not, for a SID other than
// this user, SYSTEM and the Administrators; or an entry of a form not read here (an object or
// callback entry), counted as exposure so it never passes. A deny entry never cancels a forbidden
// allow; on its own it exposes nothing.
func exposure(f *os.File) (string, error) {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	dacl, _, err := sd.DACL()
	if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		return "", err
	}
	if dacl == nil {
		return "has no access list, so everyone may read and write it: restrict it to this user", nil
	}
	allowed, err := trusted()
	if err != nil {
		return "", err
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return "", err
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !anyEquals(allowed, sid) {
				return fmt.Sprintf("grants access to %v: restrict it to this user", sid), nil
			}
		default:
			return fmt.Sprintf("holds an access entry of type %d, which this check does not read: restrict it to this user", ace.Header.AceType), nil
		}
	}
	return "", nil
}

// create is a new file made with this user as its owner and a protected DACL granting this user
// alone full control, set in the call that makes it (CREATE_NEW): nothing is inherited from the
// folder at any moment, and the token's default owner is not taken. The handle
// is not inheritable.
func create(path string) (*os.File, error) {
	me, err := currentUser()
	if err != nil {
		return nil, err
	}
	// the owner is named too: a new object's default owner is the token's TokenOwner, which can be
	// a group (an elevated administrator's is the Administrators), and OpenPrivate wants this user
	sd, err := windows.SecurityDescriptorFromString("O:" + me.String() + "D:P(A;;FA;;;" + me.String() + ")")
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, &os.PathError{Op: "create", Path: path, Err: os.ErrExist}
		}
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// currentUser is this process's user's SID.
func currentUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}

// trusted are the SIDs an access entry may grant to and the file stay private: this user, SYSTEM
// and the Administrators.
func trusted() ([]*windows.SID, error) {
	me, err := currentUser()
	if err != nil {
		return nil, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	return []*windows.SID{me, system, admins}, nil
}

func anyEquals(sids []*windows.SID, s *windows.SID) bool {
	for _, t := range sids {
		if t.Equals(s) {
			return true
		}
	}
	return false
}
