package paths_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/paths"
)

// On Windows a drive's path is absolute in either form, and its slash form keeps the drive; a
// drive's current directory ("C:x") and a bare drive ("C:") are not absolute.
func TestADrivesPathIsAbsoluteInEitherForm(t *testing.T) {
	for in, want := range map[string]bool{`C:\Users\u`: true, "C:/Users/u": true, `\\server\share\kb`: true, "C:x": false, "C:": false, `Users\u`: false} {
		if got := paths.IsAbs(in); got != want {
			t.Errorf("IsAbs(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]string{`C:\Users\u\kb`: "C:/Users/u/kb", `C:\Users\u\kb\`: "C:/Users/u/kb", `C:\Users\.\u\..\v`: "C:/Users/v"} {
		if got := paths.Slash(in); got != want {
			t.Errorf("Slash(%q) = %q, want %q", in, got, want)
		}
	}
}

// A drive's root, through the functions with this OS's rule: it stays "C:/" (never "C:", the
// drive's current directory) through Slash, Join, Dir, Clean and Root.
func TestADrivesRootStaysItsRoot(t *testing.T) {
	if got := paths.Slash(`C:\`); got != "C:/" {
		t.Errorf(`Slash(C:\) = %q, want C:/`, got)
	}
	if got := paths.Join("C:/", ".todo-list"); got != "C:/.todo-list" {
		t.Errorf("Join(C:/, .todo-list) = %q", got)
	}
	if got := paths.Dir("C:/.todo-list"); got != "C:/" {
		t.Errorf("Dir(C:/.todo-list) = %q", got)
	}
	if got := paths.Clean("C:/kb/.."); got != "C:/" {
		t.Errorf("Clean(C:/kb/..) = %q", got)
	}
	if !paths.IsRoot("C:/") || paths.IsRoot("C:") || !paths.IsAbs("C:/") {
		t.Error("C:/ is a root and absolute; C: is neither")
	}
	if got := paths.Slash(`\\srv\sh\kb`); got != "//srv/sh/kb" || paths.Dir(got) != "//srv/sh/" {
		t.Errorf(`Slash(\\srv\sh\kb) = %q, its Dir %q`, got, paths.Dir(got))
	}
}
