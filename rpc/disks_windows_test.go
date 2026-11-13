//go:build windows

package rpc

import "testing"

// On Windows a path's volume is its drive or share, and its name is the rest, slashed.
func TestADiskNameOnWindows(t *testing.T) {
	for _, c := range []struct{ abs, volume, name string }{
		{`C:\Users\me\a.md`, "C:", "Users/me/a.md"},
		{`d:\notes\a.md`, "d:", "notes/a.md"},
		{`\\server\share\notes\a.md`, `\\server\share`, "notes/a.md"},
	} {
		if v, n, err := diskName(c.abs); err != nil || v != c.volume || n != c.name {
			t.Errorf("diskName(%q) = %q, %q, %v; want %q, %q", c.abs, v, n, err, c.volume, c.name)
		}
	}
	// a drive's root, a path with no drive (relative to the current one), a relative one, one not clean
	for _, bad := range []string{`C:\`, `\Users\me`, `Users\me`, `C:Users`, `C:\Users\..\me`, `C:/Users/me`} {
		if _, _, err := diskName(bad); err == nil {
			t.Errorf("diskName(%q) was accepted", bad)
		}
	}
}
