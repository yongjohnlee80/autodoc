package todo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// A workspace on a share, on the real disk: the store is found, claimed and written under its UNC
// path, and the store's path keeps the share as its volume ("//localhost/C$/…"), never a rooted
// "/localhost/…" a drive would resolve. The share is the test's own directory through the drive's
// administrative share.
func TestAStoreOnAShareKeepsTheShareAsItsVolume(t *testing.T) {
	dir := t.TempDir()
	vol := filepath.VolumeName(dir)
	if len(vol) != 2 || vol[1] != ':' {
		t.Skipf("the temporary directory %s is not on a drive", dir)
	}
	root := `\\localhost\` + vol[:1] + `$` + dir[len(vol):] + `\ws`
	if err := os.MkdirAll(filepath.Join(root, ".todo-list", "open"), 0o755); err != nil {
		t.Skipf("the administrative share is not reachable here: %v", err)
	}
	m := todo.New(todotest.Roots{"ws": root}, localDisk{}, todotest.NewJournal())
	t.Cleanup(func() { m.Close() }) // before the directory is removed: Windows removes no directory held open
	ws := todo.At{Workspace: "ws"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatalf("claim the store on the share: %v", err)
	}
	r, err := m.Resolve(ctx, ws)
	if err != nil || !r.Writable {
		t.Fatalf("the store on the share: %+v, %v; want writable", r, err)
	}
	if !strings.HasPrefix(strings.ToLower(r.Store), "//localhost/") || r.Store != paths.Join(r.Root, ".todo-list") {
		t.Errorf("the store's path %q under root %q: want the share kept, //localhost/…/.todo-list", r.Store, r.Root)
	}
	if _, err := m.Add(ctx, ws, todo.Spec{Title: "t"}, ""); err != nil {
		t.Fatalf("add on the share: %v", err)
	}
	if l, err := m.List(ctx, ws, todo.ListOpts{}); err != nil || len(l.Tasks) != 1 {
		t.Errorf("list on the share: %+v, %v", l, err)
	}
}
