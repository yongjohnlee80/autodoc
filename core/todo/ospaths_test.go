package todo_test

import (
	"context"
	"testing"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// spelledDisk answers every canonical path in another spelling of the same directory, a trailing
// separator added: as a disk whose own form is not core/todo's does (Windows' answers
// `C:\Users\u\ws` where core/todo keeps "C:/Users/u/ws").
type spelledDisk struct{ *todotest.Disk }

func (d spelledDisk) Open(ctx context.Context, dir string) (vfs.FS, string, error) {
	fsys, canon, err := d.Disk.Open(ctx, dir)
	if err == nil {
		canon += "/"
	}
	return fsys, canon, err
}

// Whatever spelling the disk and the workspace list give a directory in, core/todo takes it in its
// own form: a workspace's existing .todo-list is its writable store, a registered root's is too,
// and each resolves to one store at one clean path.
func TestTheDisksSpellingOfAPathIsTakenInTheStoresForm(t *testing.T) {
	disk := todotest.NewDisk()
	disk.MkdirAll("/ws/.todo-list/open")
	disk.MkdirAll("/srv/p/.todo-list/open")
	m := todo.New(todotest.Roots{"ws": "/ws/"}, spelledDisk{disk}, todotest.NewJournal())
	ws := todo.At{Workspace: "ws"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatalf("claim the workspace's store: %v", err)
	}
	r, err := m.Resolve(ctx, ws)
	if err != nil || !r.Writable || r.Root != "/ws" || r.Store != "/ws/.todo-list" {
		t.Errorf("the workspace's store: %+v, %v; want writable, root /ws, store /ws/.todo-list", r, err)
	}
	// named by its root, the workspace's store is still the workspace's: one store, writable
	if byRoot, err := m.Resolve(ctx, todo.At{Root: "/ws"}); err != nil || byRoot.StoreID != r.StoreID || !byRoot.Writable {
		t.Errorf("the workspace's root named as a root: %+v, %v; want the store %s, writable", byRoot, err, r.StoreID)
	}
	if _, err := m.Register(ctx, "/srv/p/"); err != nil {
		t.Fatalf("register: %v", err)
	}
	p, err := m.Resolve(ctx, todo.At{Root: "/srv/p"})
	if err != nil || !p.Writable || p.Root != "/srv/p" || p.Store != "/srv/p/.todo-list" {
		t.Errorf("a registered root's store: %+v, %v; want writable, root /srv/p, store /srv/p/.todo-list", p, err)
	}
}
