package workspace_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// NormalizeWorkspace is what workspace.add checks: the rules [[workspace]] had.
func TestNormalize(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	w, err := workspace.Normalize(workspace.Spec{Name: "kb", Root: "~/notes"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Root != filepath.Join(home, "notes") {
		t.Errorf("root = %q, want ~ expanded", w.Root)
	}
	if !reflect.DeepEqual(w.Include, workspace.DefaultInclude()) || !reflect.DeepEqual(w.Exclude, workspace.DefaultExclude()) {
		t.Errorf("patterns = %v / %v, want the defaults", w.Include, w.Exclude)
	}
	w, err = workspace.Normalize(workspace.Spec{Name: "kb", Root: "/srv/kb", Include: []string{"docs/**/*.md", "*.markdown"}, Exclude: []string{}})
	if err != nil || len(w.Include) != 2 || len(w.Exclude) != 0 {
		t.Errorf("an explicit empty exclude must stay empty: %+v, %v", w, err)
	}
	for _, c := range []struct {
		name string
		w    workspace.Spec
		want string
	}{
		{"relative root", workspace.Spec{Name: "a", Root: "notes"}, "absolute"},
		{"no name", workspace.Spec{Root: "/x"}, "name is required"},
		{"a name with a separator", workspace.Spec{Name: "a/b", Root: "/x"}, "path separator"},
		{"a name with surrounding space", workspace.Spec{Name: " a", Root: "/x"}, "surrounding space"},
		{"bad pattern", workspace.Spec{Name: "a", Root: "/x", Include: []string{"["}}, "pattern"},
		{"absolute pattern", workspace.Spec{Name: "a", Root: "/x", Exclude: []string{"/etc/**"}}, "pattern"},
	} {
		if _, err := workspace.Normalize(c.w); !errors.Is(err, workspace.ErrInvalidSpec) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrInvalidSpec mentioning %q", c.name, err, c.want)
		}
	}
}
