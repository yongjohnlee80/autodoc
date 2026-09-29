package workspace_test

import (
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

func TestMatcher(t *testing.T) {
	m := workspace.NewMatcher([]string{"**/*.md", "docs/*.txt"}, []string{".git/**", "drafts/**", "**/_*.md"})
	for _, c := range []struct {
		p    string
		want bool
	}{
		{"a.md", true}, {"x/y/z.md", true}, {"docs/a.txt", true}, {"docs/sub/a.txt", false},
		{"a.txt", false}, {".git/x.md", false}, {".git", false}, {"drafts/a.md", false},
		{"drafts/deep/a.md", false}, {"x/_private.md", false}, {"_top.md", false}, {"a.mdx", false},
	} {
		if got := m.Match(c.p); got != c.want {
			t.Errorf("Match(%q) = %v, want %v", c.p, got, c.want)
		}
	}
	if !m.Excluded(".git") || !m.Excluded("drafts/deep") || m.Excluded("docs") {
		t.Error("Excluded is wrong for a directory")
	}
}

func TestOpenReportsAMissingRoot(t *testing.T) {
	c := config.Workspace{Name: "kb", Root: filepath.Join(t.TempDir(), "missing"), Include: config.DefaultInclude, Exclude: config.DefaultExclude}
	if _, err := workspace.Open(c); err == nil {
		t.Fatal("opened a missing root")
	}
	c.Root = filepath.Dir(c.Root)
	w, err := workspace.Open(c)
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "kb" || w.Root != c.Root || !w.Matcher.Match("a.md") {
		t.Errorf("Open = %+v", w)
	}
	_ = w.Close()
}
