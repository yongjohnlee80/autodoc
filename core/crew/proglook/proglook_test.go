package proglook

import (
	"path/filepath"
	"testing"
)

// A name that is a path isn't looked for; a program's name drops the extension that makes it one.
func TestPathsAndNames(t *testing.T) {
	for _, c := range []struct {
		name string
		path bool
	}{{"claude", false}, {"claude_tui.sh", false}, {filepath.Join("bin", "claude"), true}, {filepath.Join(".", "claude"), true}} {
		if got := IsPath(c.name); got != c.path {
			t.Errorf("IsPath(%q) = %v, want %v", c.name, got, c.path)
		}
	}
	if got := All(filepath.Join("bin", "claude"), t.TempDir(), t.TempDir()); got != nil {
		t.Errorf("All of a path: %v, want none", got)
	}
	for in, want := range map[string]string{filepath.Join("x", "mise"): "mise", filepath.Join("x", "mise.exe"): "mise", "claude.cmd": "claude"} {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}
