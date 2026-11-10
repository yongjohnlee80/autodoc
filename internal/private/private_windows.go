//go:build windows

package private

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// check is what makes the file exposed, "" when nothing does: on Windows, an owner other than this
// user, no DACL (everyone, everything), or an ACE that grants access to a SID other than this user,
// SYSTEM and the Administrators. An ACE of any kind but a plain allow or deny is counted as exposure,
// so a kind this check does not read never passes.
func check(path string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	me, err := currentUser()
	if err != nil {
		return "", err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return "", err
	}
	if owner == nil || !owner.Equals(me) {
		return fmt.Sprintf("is owned by %v, not this user", owner), nil
	}
	dacl, _, err := sd.DACL()
	if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		return "", err
	}
	if dacl == nil {
		return "has no access list, so everyone may read and write it: restrict it to this user", nil
	}
	allowed, err := trusted(me)
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
			continue // takes access away: never exposure
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !anyEquals(allowed, sid) {
				return fmt.Sprintf("grants access to %v: restrict it to this user", sid), nil
			}
		default:
			return fmt.Sprintf("holds an access entry of type %d this check does not read: restrict it to this user", ace.Header.AceType), nil
		}
	}
	return "", nil
}

// create is a new file whose protected DACL grants this user alone full control, set as it is made.
func create(path string) (*os.File, error) {
	sa, err := ownOnly()
	if err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return nil, &os.PathError{Op: "create", Path: path, Err: os.ErrExist}
		}
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// restrict gives the file this user's protected, owner-only DACL, nothing inherited from its folder.
// A file another user owns is refused, never taken.
func restrict(path string) error {
	me, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	if owner, _, err := sd.Owner(); err != nil || owner == nil || !owner.Equals(me) {
		return fmt.Errorf("%w: %s is owned by %v, not this user", ErrExposed, path, owner)
	}
	own, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + me.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := own.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// ownOnly is the security attributes of a new owner-only file: a protected DACL granting this user
// full control, nothing inherited.
func ownOnly() (*windows.SecurityAttributes, error) {
	me, err := currentUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + me.String() + ")")
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
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
func trusted(me *windows.SID) ([]*windows.SID, error) {
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
