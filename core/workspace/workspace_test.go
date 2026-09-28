package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func cfg(t *testing.T, name string) (config.Workspace, string) {
	t.Helper()
	return config.Workspace{Name: name, Root: t.TempDir(), Include: config.DefaultInclude, Exclude: config.DefaultExclude}, t.TempDir()
}

// TestLeaseIsSingleInstance: a second Open of a workspace another holder serves is ErrWorkspaceBusy
// until the first closes; the index lives in the state directory, never in the root.
func TestLeaseIsSingleInstance(t *testing.T) {
	c, state := cfg(t, "kb")
	w, err := workspace.Open(c, state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(w.IndexPath(), c.Root) {
		t.Errorf("the index %s is inside the root %s", w.IndexPath(), c.Root)
	}
	if fi, err := os.Stat(w.IndexPath()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the store: %v, %v", fi, err)
	}
	if _, err := workspace.Open(c, state); !errors.Is(err, workspace.ErrWorkspaceBusy) {
		t.Fatalf("a second Open: %v, want ErrWorkspaceBusy", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w2, err := workspace.Open(c, state)
	if err != nil {
		t.Fatalf("reopening after Close: %v", err)
	}
	_ = w2.Close()
}

// TestLeaseNamesTheStoreNotThePath: a state directory reached through a symlink is the same store,
// so its lease is the same lease.
func TestLeaseNamesTheStoreNotThePath(t *testing.T) {
	c, state := cfg(t, "kb")
	w, err := workspace.Open(c, state)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	alias := filepath.Join(t.TempDir(), "state-alias")
	if err := os.Symlink(state, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Open(c, alias); !errors.Is(err, workspace.ErrWorkspaceBusy) {
		t.Fatalf("Open through a symlinked state dir: %v, want ErrWorkspaceBusy", err)
	}
}

func TestOpenReportsAMissingRoot(t *testing.T) {
	c, state := cfg(t, "kb")
	c.Root = filepath.Join(c.Root, "missing")
	if _, err := workspace.Open(c, state); err == nil {
		t.Fatal("opened a missing root")
	}
	// the lease it took was released: the next Open is not busy
	c.Root = filepath.Dir(c.Root)
	w, err := workspace.Open(c, state)
	if err != nil {
		t.Fatalf("after a failed Open: %v", err)
	}
	_ = w.Close()
}
