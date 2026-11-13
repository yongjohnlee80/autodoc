package store

import (
	"testing"

	"golang.org/x/sys/windows"

	"github.com/yongjohnlee80/autodoc/internal/userperm"
)

// setDACL gives the file at path the DACL sddl names, protected: its own entries only.
func setDACL(t *testing.T, path, sddl string) {
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

// me is this process's user's SID, as SDDL names it.
func me(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid.String()
}

// expose gives Everyone read access to the file at path beside this user's full control.
func expose(t *testing.T, path string) {
	t.Helper()
	setDACL(t, path, "D:P(A;;FA;;;"+me(t)+")(A;;FR;;;WD)")
}

// makePrivate gives the file at path to this user alone: a protected DACL granting this user full
// control and no one else anything.
func makePrivate(t *testing.T, path string) {
	t.Helper()
	setDACL(t, path, "D:P(A;;FA;;;"+me(t)+")")
}

// privateFile is nil when the file at path is this user's alone, as the store makes its secrets: its
// DACL grants no one else access (userperm's reading of it).
func privateFile(path string) error {
	f, err := userperm.OpenPrivate(path)
	if err != nil {
		return err
	}
	return f.Close()
}
