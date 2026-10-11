package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/rpc"
)

func TestTodoRootsAreTheServedWorkspaces(t *testing.T) {
	ws := rpc.Fixed(&rpc.Workspace{Name: "kb", Root: "/kb"}, &rpc.Workspace{Name: "gone", Root: "/gone", Err: errors.New("no root")})
	r := TodoRoots{Workspaces: ws}
	if root, ok := r.Root("kb"); !ok || root != "/kb" {
		t.Errorf("kb's root %q %v", root, ok)
	}
	if _, ok := r.Root("gone"); ok {
		t.Error("a workspace that could not open has a root")
	}
	if _, ok := r.Root("nope"); ok {
		t.Error("an unknown workspace has a root")
	}
	if got := r.Names(); !reflect.DeepEqual(got, []string{"kb"}) {
		t.Errorf("names %v", got)
	}
}

func TestTodoDiskOpensACanonicalDirectory(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	fsys, canon, err := TodoDisk{}.Open(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	want, _ := filepath.EvalSymlinks(real)
	if canon != want {
		t.Errorf("canonical %q, want %q", canon, want)
	}
	if _, err := fsys.Stat(context.Background(), "a.md"); err != nil {
		t.Errorf("a.md through the opened fs: %v", err)
	}
	if _, _, err := (TodoDisk{}).Open(context.Background(), filepath.Join(dir, "missing")); !errors.Is(err, vfs.ErrNotExist) {
		t.Errorf("a missing dir: %v", err)
	}
	if _, _, err := (TodoDisk{}).Open(context.Background(), "relative"); err == nil {
		t.Error("a relative dir was opened")
	}
}
