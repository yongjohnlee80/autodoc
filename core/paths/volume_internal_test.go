package paths

import "testing"

// Windows' rule, run on every OS: a drive's root stays "C:/" through every operation, a share keeps
// "//server/share", a verbatim path is its plain form, and a device path keeps its prefix.
func TestAVolumeSurvivesEveryOperationUnderWindowsRules(t *testing.T) {
	for in, want := range map[string]string{
		`C:\`: "C:/", `C:\x\`: "C:/x", `C:\a\..\b`: "C:/b", `c:\x`: "c:/x", `C:`: "C:", `C:x\y`: "C:x/y",
		`\\srv\sh\kb`: "//srv/sh/kb", `\\srv\sh`: "//srv/sh/", `\\srv\sh\`: "//srv/sh/", `\\srv\sh\kb\..`: "//srv/sh/",
		`\\?\C:\x`: "C:/x", `\\?\UNC\srv\sh\kb`: "//srv/sh/kb", `\\?\unc\srv\sh`: "//srv/sh/",
		`\\?\Volume{1}\x`: "//?/Volume{1}/x", `\\.\pipe\p`: "//./pipe/p",
		`/srv/kb`: "/srv/kb", `kb\x`: "kb/x", "": "",
	} {
		if got := slash(in, true); got != want {
			t.Errorf("slash(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range []struct {
		abs  string
		elem []string
		want string
	}{
		{"C:/", []string{".todo-list"}, "C:/.todo-list"},
		{"C:/kb", []string{"..", ".."}, "C:/"},
		{"//srv/sh/kb", []string{".todo-list"}, "//srv/sh/kb/.todo-list"},
		{"//srv/sh/", []string{".todo-list"}, "//srv/sh/.todo-list"},
		{"//srv/sh/kb", []string{".."}, "//srv/sh/"},
		{"/srv/kb", []string{"x"}, "/srv/kb/x"},
	} {
		if got := join(c.abs, true, c.elem...); got != c.want {
			t.Errorf("join(%q, %q) = %q, want %q", c.abs, c.elem, got, c.want)
		}
	}
	for in, want := range map[string]string{"C:/x": "C:/", "C:/": "C:/", "C:/a/b": "C:/a", "//srv/sh/kb": "//srv/sh/", "//srv/sh/": "//srv/sh/", "/srv": "/"} {
		if got := dir(in, true); got != want {
			t.Errorf("dir(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string][2]string{
		"C:/": {"C:/", ""}, "C:/a/b": {"C:/", "a/b"}, "//srv/sh/kb": {"//srv/sh/", "kb"}, "//srv/sh/": {"//srv/sh/", ""},
		"/srv/kb": {"/", "srv/kb"}, "C:x": {"", "C:x"}, "C:": {"", "C:"}, "kb": {"", "kb"},
	} {
		if root, rel := rootOf(in, true); root != want[0] || rel != want[1] {
			t.Errorf("rootOf(%q) = %q, %q; want %q, %q", in, root, rel, want[0], want[1])
		}
	}
	for _, c := range []struct {
		dir, p, want string
		ok           bool
	}{
		{"C:/", "C:/kb/x", "kb/x", true}, {"C:/kb", "C:/kb", ".", true}, {"C:/kb", "C:/kbx", "", false},
		{"C:/kb", "D:/kb/x", "", false}, {"//srv/sh/", "//srv/sh/kb", "kb", true}, {"//srv/sh/kb", "//srv/other/kb", "", false},
	} {
		if got, ok := relOf(c.dir, c.p, true); got != c.want || ok != c.ok {
			t.Errorf("relOf(%q, %q) = %q, %v; want %q, %v", c.dir, c.p, got, ok, c.want, c.ok)
		}
	}
}

// Unix' rule: no volumes; a backslash is a name's own character, never a separator.
func TestThereIsNoVolumeUnderUnixRules(t *testing.T) {
	for in, want := range map[string]string{`/srv/kb/`: "/srv/kb", `/srv/a\b`: `/srv/a\b`, `C:\x`: `C:\x`, "C:/": "C:"} {
		if got := slash(in, false); got != want {
			t.Errorf("slash(%q) = %q, want %q", in, got, want)
		}
	}
	if got := join("/", false, ".todo-list"); got != "/.todo-list" {
		t.Errorf("join(/, .todo-list) = %q", got)
	}
	if root, rel := rootOf("C:/x", false); root != "" || rel != "C:/x" {
		t.Errorf("rootOf(C:/x) = %q, %q: no drive outside Windows", root, rel)
	}
}

// Every path on a volume is under its root, and only its own volume's; a relative dir ending in
// "/" is no root.
func TestEveryPathOnAVolumeIsUnderItsRoot(t *testing.T) {
	for _, c := range []struct {
		p, dir string
		want   bool
	}{
		{"/x", "/", true}, {"/", "/", true}, {"docs/a", "docs/", false}, {"docs/", "docs/", true},
	} {
		if got := Under(c.p, c.dir); got != c.want {
			t.Errorf("Under(%q, %q) = %v, want %v", c.p, c.dir, got, c.want)
		}
	}
	if win {
		for _, c := range []struct {
			p, dir string
			want   bool
		}{{"C:/kb/x", "C:/", true}, {"//srv/sh/kb", "//srv/sh/", true}, {"C:/kb", "D:/", false}} {
			if got := Under(c.p, c.dir); got != c.want {
				t.Errorf("Under(%q, %q) = %v, want %v", c.p, c.dir, got, c.want)
			}
		}
	}
}
