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

// ValidName is Normalize's name rule alone: for every name it accepts or refuses exactly what
// Normalize does with a root this OS calls absolute, with the same error, and it needs no root,
// so a rename's check answers the same on every OS.
func TestValidNameIsNormalizesNameRule(t *testing.T) {
	root := t.TempDir()
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"kb", true}, {"my notes", true}, {"a.b", true}, {"...", true}, {"C:", true}, {"ü", true},
		{"", false}, {".", false}, {"..", false},
		{"a/b", false}, {`a\b`, false}, {"/", false}, {`\`, false}, {"/kb", false}, {`C:\kb`, false},
		{" kb", false}, {"kb ", false}, {"\tkb", false}, {"kb\n", false},
	} {
		got := workspace.ValidName(c.name)
		if (got == nil) != c.ok {
			t.Errorf("ValidName(%q) = %v, want ok=%v", c.name, got, c.ok)
		}
		if got != nil && !errors.Is(got, workspace.ErrInvalidSpec) {
			t.Errorf("ValidName(%q) = %v, want ErrInvalidSpec", c.name, got)
		}
		_, norm := workspace.Normalize(workspace.Spec{Name: c.name, Root: root})
		if (got == nil) != (norm == nil) || got != nil && got.Error() != norm.Error() {
			t.Errorf("name %q: ValidName says %v, Normalize says %v", c.name, got, norm)
		}
	}
}
