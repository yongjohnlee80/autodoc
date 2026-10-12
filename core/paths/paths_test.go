package paths_test

import (
	"errors"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/paths"
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
	t.Setenv("HOME", "/home/u")
	for in, want := range map[string]string{"~": "/home/u", "~/notes": "/home/u/notes", "/srv/kb": "/srv/kb", "notes/~": "notes/~", "~user/x": "~user/x"} {
		if got, err := paths.ExpandHome(in); err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	t.Setenv("HOME", "")
	if _, err := paths.ExpandHome("~/notes"); !errors.Is(err, paths.ErrNoHome) {
		t.Errorf("no home: %v; want ErrNoHome", err)
	}
}
