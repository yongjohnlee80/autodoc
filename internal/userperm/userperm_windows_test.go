//go:build windows

package userperm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setDACL gives path the DACL sddl names (protected when it says P), its own entries only.
func setDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		t.Fatal(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if ctrl&windows.SE_DACL_PROTECTED != 0 {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func me(t *testing.T) string {
	t.Helper()
	u, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.String()
}

// expose grants Everyone read access beside this user's full control.
func expose(t *testing.T, path string) {
	t.Helper()
	setDACL(t, path, "D:P(A;;FA;;;"+me(t)+")(A;;FR;;;WD)")
}

// On Windows SYSTEM and the Administrators may hold access and the file stay private; Users,
// Authenticated Users or Everyone may not; a deny never cancels another's allow; a NULL DACL is
// exposed.
func TestTheDACLsVerdictsOnWindows(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	write(t, key, "secret")
	u := me(t)
	for sddl, exposed := range map[string]bool{
		"D:P(A;;FA;;;" + u + ")":                         false,
		"D:P(A;;FA;;;" + u + ")(A;;FA;;;SY)(A;;FA;;;BA)": false,
		"D:P(A;;FA;;;" + u + ")(A;;FR;;;BU)":             true,  // Users
		"D:P(A;;FA;;;" + u + ")(A;;FR;;;AU)":             true,  // Authenticated Users
		"D:P(A;;FA;;;" + u + ")(D;;FA;;;WD)":             false, // a deny alone exposes nothing
		"D:P(D;;FA;;;BU)(A;;FA;;;" + u + ")(A;;FR;;;BU)": true,  // a deny never cancels an allow
		"D:NO_ACCESS_CONTROL":                            true,  // no DACL: everyone, everything
	} {
		setDACL(t, key, sddl)
		_, err := read(t, key)
		if got := errors.Is(err, ErrExposed); got != exposed {
			t.Errorf("%s: %v, want exposed %v", sddl, err, exposed)
		}
	}
}

// A folder that grants Users read to what it holds exposes a file made the ordinary way in it
// (inherited), while CreatePrivate's file there is private from its creation: its DACL, read before
// anything is written or restricted, holds no entry but this user's.
func TestInheritedAccessAndAPrivateCreateUnderABroadFolderOnWindows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "broad")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	u := me(t)
	setDACL(t, dir, "D:P(A;OICI;FA;;;"+u+")(A;OICI;FR;;;BU)")
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := read(t, plain); !errors.Is(err, ErrExposed) {
		t.Errorf("a file inheriting Users read: %v, want ErrExposed", err)
	}
	key := filepath.Join(dir, "key")
	f, err := CreatePrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sd, err := windows.GetNamedSecurityInfo(key, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	mine, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !sid.Equals(mine) {
			t.Errorf("a file CreatePrivate made under a broad folder holds an entry for %v at its creation", sid)
		}
	}
}
