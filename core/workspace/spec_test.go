package workspace_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/workspace"
	"github.com/yongjohnlee80/autodoc/internal/userdirs"
)

// NormalizeWorkspace is what workspace.add checks: the rules [[workspace]] had.
func TestNormalize(t *testing.T) {
	home := t.TempDir()
	t.Setenv(userdirs.HomeVariable, home)
	abs := filepath.Join(t.TempDir(), "kb") // an absolute root on any OS
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
	w, err = workspace.Normalize(workspace.Spec{Name: "kb", Root: abs, Include: []string{"docs/**/*.md", "*.markdown"}, Exclude: []string{}})
	if err != nil || len(w.Include) != 2 || len(w.Exclude) != 0 {
		t.Errorf("an explicit empty exclude must stay empty: %+v, %v", w, err)
	}
	for _, c := range []struct {
		name string
		w    workspace.Spec
		want string
	}{
		{"relative root", workspace.Spec{Name: "a", Root: "notes"}, "absolute"},
		{"no name", workspace.Spec{Root: abs}, "name is required"},
		{"a name with a separator", workspace.Spec{Name: "a/b", Root: abs}, "path separator"},
		{"a name with surrounding space", workspace.Spec{Name: " a", Root: abs}, "surrounding space"},
		{"bad pattern", workspace.Spec{Name: "a", Root: abs, Include: []string{"["}}, "pattern"},
		{"absolute pattern", workspace.Spec{Name: "a", Root: abs, Exclude: []string{"/etc/**"}}, "pattern"},
	} {
		if _, err := workspace.Normalize(c.w); !errors.Is(err, workspace.ErrInvalidSpec) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrInvalidSpec mentioning %q", c.name, err, c.want)
		}
	}
}
