//go:build windows

package private

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// setDACL gives path the DACL sddl names, protected or not as it says.
func setDACL(t *testing.T, path, sddl string, protected bool) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if protected {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// expose grants Everyone read access beside this user's full control.
func expose(t *testing.T, path string) {
	t.Helper()
	me, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	setDACL(t, path, "D:P(A;;FA;;;"+me.String()+")(A;;FR;;;WD)", true)
}

// On Windows SYSTEM and the Administrators may hold access and the file stay private (no file
// keeps them out); Users, Authenticated Users or Everyone may not.
func TestOnlyThisUserSystemAndAdministratorsKeepItPrivateOnWindows(t *testing.T) {
	me, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for sddl, exposed := range map[string]bool{
		"D:P(A;;FA;;;" + me.String() + ")":                         false,
		"D:P(A;;FA;;;" + me.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)": false,
		"D:P(A;;FA;;;" + me.String() + ")(A;;FR;;;BU)":             true,  // Users
		"D:P(A;;FA;;;" + me.String() + ")(A;;FR;;;AU)":             true,  // Authenticated Users
		"D:P(A;;FA;;;" + me.String() + ")(D;;FA;;;WD)":             false, // a deny takes access away
	} {
		setDACL(t, key, sddl, true)
		err := Check(key)
		if got := err != nil; got != exposed {
			t.Errorf("%s: %v, want exposed %v", sddl, err, exposed)
		}
	}
}
