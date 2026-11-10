package paths_test

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/internal/userdirs"
)

func TestUnder(t *testing.T) {
	for _, c := range []struct {
		p, dir string
		want   bool
	}{
		{"a/b", "a", true}, {"a", "a", true}, {"a/b/c", "a/b", true}, {"x", ".", true},
		{"a/bc", "a/b", false}, {"ab", "a", false}, {"a", "a/b", false},
		{"/home/u/kb/x.md", "/home/u/kb", true}, {"/home/u/kbx", "/home/u/kb", false},
	} {
		if got := paths.Under(c.p, c.dir); got != c.want {
			t.Errorf("Under(%q, %q) = %v, want %v", c.p, c.dir, got, c.want)
		}
	}
}

// ExpandHome makes a leading "~" the home directory and leaves every other path as it is; with no
// home directory to put there, it says so.
func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(userdirs.HomeVariable, home)
	for in, want := range map[string]string{"~": home, "~/notes": filepath.Join(home, "notes"), "/srv/kb": "/srv/kb", "notes/~": "notes/~", "~user/x": "~user/x"} {
		if got, err := paths.ExpandHome(in); err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	t.Setenv(userdirs.HomeVariable, "")
	if _, err := paths.ExpandHome("~/notes"); !errors.Is(err, paths.ErrNoHome) {
		t.Errorf("no home: %v; want ErrNoHome", err)
	}
}

// Slash is an OS path in the core's form, and IsAbs takes a path in either form: the directory the
// test is given, in the OS's own form and in Slash's, is absolute both ways, and stays the same
// directory.
func TestAnOSPathIsAbsoluteInEitherForm(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kb")
	slash := paths.Slash(dir)
	if strings.Contains(slash, `\`) || slash != path.Clean(slash) || filepath.FromSlash(slash) != dir {
		t.Errorf("Slash(%q) = %q: not the same directory, slash-separated and clean", dir, slash)
	}
	for _, p := range []string{dir, slash} {
		if !paths.IsAbs(p) {
			t.Errorf("IsAbs(%q) = false: the test's own directory", p)
		}
	}
	for in, want := range map[string]bool{"/srv/kb": true, "srv/kb": false, "": false, "~/kb": false, ".": false} {
		if got := paths.IsAbs(in); got != want {
			t.Errorf("IsAbs(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]string{"": "", "/srv/kb/": "/srv/kb", "/srv//kb/./x/..": "/srv/kb", "a/b": "a/b"} {
		if got := paths.Slash(in); got != want {
			t.Errorf("Slash(%q) = %q, want %q", in, got, want)
		}
	}
}
