package crew

import (
	"path/filepath"
	"testing"
)

// A name that is a path isn't looked for.
func TestPathsAndNames(t *testing.T) {
	for _, c := range []struct {
		name string
		path bool
	}{{"claude", false}, {"claude_tui.sh", false}, {filepath.Join("bin", "claude"), true}, {"." + string(filepath.Separator) + "claude", true}} {
		if got := isPath(c.name); got != c.path {
			t.Errorf("isPath(%q) = %v, want %v", c.name, got, c.path)
		}
	}
	if got := programsOn(filepath.Join("bin", "claude"), t.TempDir(), t.TempDir()); got != nil {
		t.Errorf("programsOn of a path: %v, want none", got)
	}
}
