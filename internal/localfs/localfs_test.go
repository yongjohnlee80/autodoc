package localfs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/localfs"
)

// A write replaces the file whole, and leaves nothing else beside it; a write with nowhere to go
// says so.
func TestWriteFileReplacesItWhole(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "page.html")
	for _, content := range []string{"<p>first</p>", "<p>second</p>"} {
		if err := localfs.WriteFile(ctx, p, []byte(content)); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(p); string(got) != content {
			t.Errorf("the file holds %q, want %q", got, content)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want the file alone", len(entries))
	}
	if err := localfs.WriteFile(ctx, "", []byte("x")); err == nil {
		t.Error("a write to no path succeeded")
	}
	if err := localfs.WriteFile(ctx, filepath.Join(dir, "missing", "page.html"), []byte("x")); err == nil {
		t.Error("a write into a directory that is not there succeeded")
	}
}

// Resolve is where a path really is: through a symlink, and for a path not there yet, its names
// under the resolved folder that is, the root at the least.
func TestResolveFollowsLinks(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	// nothing of it there but the root
	absent := filepath.Join(string(filepath.Separator), "autodoc-resolve-absent-"+filepath.Base(dir), "x.html")
	for in, want := range map[string]string{
		filepath.Join(dir, "alias"):                  real,
		filepath.Join(dir, "alias", "new", "x.html"): filepath.Join(real, "new", "x.html"),
		filepath.Join(dir, "gone", "y"):              filepath.Join(dir, "gone", "y"),
		absent:                                       absent,
		string(filepath.Separator):                   string(filepath.Separator),
	} {
		if got := localfs.Resolve(in); got != want {
			t.Errorf("Resolve(%s) = %s, want %s", in, got, want)
		}
	}
}
