package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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

// TestOpenReportsAMissingRoot: a root that does not exist, and a root that is a regular file, are
// each ErrNotADirectory, with no workspace opened; a directory opens.
func TestOpenReportsAMissingRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.md")
	if err := os.WriteFile(file, []byte("# a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := workspace.Spec{Name: "kb", Include: workspace.DefaultInclude(), Exclude: workspace.DefaultExclude()}
	for _, root := range []string{filepath.Join(dir, "missing"), file} {
		c.Root = root
		if w, err := workspace.Open(c); !errors.Is(err, workspace.ErrNotADirectory) || w != nil {
			t.Errorf("Open(%s) = %v, %v; want no workspace and ErrNotADirectory", root, w, err)
		}
	}
	c.Root = dir
	w, err := workspace.Open(c)
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "kb" || w.Root != c.Root || !w.Matcher.Match("a.md") {
		t.Errorf("Open = %+v", w)
	}
	_ = w.Close()
}

// TestOpenReportsARootItCannotRead: a directory that cannot be opened is not a missing root; the
// error says it could not be opened, and why.
func TestOpenReportsARootItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	dir := filepath.Join(t.TempDir(), "sealed")
	if err := os.Mkdir(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := workspace.Open(workspace.Spec{Name: "kb", Root: dir})
	if err == nil || errors.Is(err, workspace.ErrNotADirectory) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Open of an unreadable root = %v; want the permission error, not ErrNotADirectory", err)
	}
}
