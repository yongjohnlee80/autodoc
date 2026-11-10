//go:build unix

package rpc

import "testing"

// On Unix a path's volume is "" and its name in the one tree is the path below "/".
func TestADiskNameOnUnix(t *testing.T) {
	for _, c := range []struct{ abs, volume, name string }{
		{"/home/me/a.md", "", "home/me/a.md"},
		{`/home/me/a\b.md`, "", `home/me/a\b.md`}, // a backslash is part of a Unix name
	} {
		if v, n, err := diskName(c.abs); err != nil || v != c.volume || n != c.name {
			t.Errorf("diskName(%q) = %q, %q, %v; want %q, %q", c.abs, v, n, err, c.volume, c.name)
		}
	}
	for _, bad := range []string{"/", "home/me", "/home/../me", "/home//me"} {
		if _, _, err := diskName(bad); err == nil {
			t.Errorf("diskName(%q) was accepted", bad)
		}
	}
}
