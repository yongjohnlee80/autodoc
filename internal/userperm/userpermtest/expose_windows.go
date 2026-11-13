package userpermtest

import (
	"testing"

	"golang.org/x/sys/windows"
)

// Expose makes path readable by others: a protected DACL granting this user full control and
// Everyone read access.
func Expose(t testing.TB, path string) {
	t.Helper()
	setDACL(t, path, "D:P(A;;FA;;;"+me(t)+")(A;;FR;;;WD)")
}

// ExposeNew makes what is created in the folder dir from now on readable by others unless its
// creator says otherwise: a protected DACL whose entries files and folders inherit, this user's
// full control and Everyone's read. A file made the ordinary way there (os.Create, os.CreateTemp)
// is exposed, whatever the token's default owner; a private create (userperm.CreatePrivate) is not.
func ExposeNew(t testing.TB, dir string) {
	t.Helper()
	setDACL(t, dir, "D:P(A;OICI;FA;;;"+me(t)+")(A;OICI;FR;;;WD)")
}

// Unreadable makes path one this user may write but not read: a protected DACL granting this user
// write access alone (and so its security read, with no data). A process that reads it all the same
// (as a backup privilege may let userperm's open) has no such file, and the test is skipped
// (unreadable).
func Unreadable(t testing.TB, path string) {
	t.Helper()
	setDACL(t, path, "D:P(A;;FW;;;"+me(t)+")")
	unreadable(t, path)
}

func me(t testing.TB) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid.String()
}

func setDACL(t testing.TB, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}
