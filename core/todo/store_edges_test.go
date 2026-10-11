package todo_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

var errDisk = errors.New("disk trouble")

// A disk that fails a read is an error for the verb that needs it; a task file that cannot be
// read is listed as malformed, with why.
func TestDiskFailuresSurface(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "t", ID: "t"})
	e.disk.WriteFile(kbStore+"/archived/2026/05/old.md", []byte(strings.Replace(taskFile("old", "archived"),
		"archived_at: \"2026-10-01T09:00:00-07:00\"", "archived_at: \"2026-05-01T09:00:00-07:00\"", 1)))
	fail := func(op, path string) {
		e.disk.SetFault(func(o, name string) error {
			if o == op && name == path {
				return errDisk
			}
			return nil
		})
	}
	defer e.disk.SetFault(nil)

	fail("read", kbStore+"/open/t.md")
	l, err := e.m.List(ctx, kb, todo.ListOpts{})
	if err != nil || len(l.Malformed) != 1 || !strings.HasPrefix(l.Malformed[0].Reason, "read: ") {
		t.Errorf("an unreadable task: %+v %v", l, err)
	}
	if _, err := e.m.Update(ctx, kb, "t", task.Rev, todo.Patch{"title": "x"}); !errors.Is(err, errDisk) {
		t.Errorf("updating it: %v", err)
	}
	fail("readdir", kbStore+"/open")
	if _, err := e.m.List(ctx, kb, todo.ListOpts{}); !errors.Is(err, errDisk) {
		t.Errorf("an unlistable bucket: %v", err)
	}
	fail("stat", kbStore+"/deferred")
	if _, err := e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"deferred"}}); !errors.Is(err, errDisk) {
		t.Errorf("a bucket that cannot be stat'ed: %v", err)
	}
	fail("readdir", kbStore+"/archived")
	if _, err := e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"archived"}}); !errors.Is(err, errDisk) {
		t.Errorf("an unlistable archive: %v", err)
	}
	if _, err := e.m.Get(ctx, kb, "nowhere"); !errors.Is(err, errDisk) {
		t.Errorf("finding an id past an unlistable archive: %v", err)
	}
	fail("readdir", kbStore+"/archived/2026")
	if _, err := e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"archived"}}); !errors.Is(err, errDisk) {
		t.Errorf("an unlistable year: %v", err)
	}
	fail("readdir", kbStore+"/archived/2026/05")
	if _, err := e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"archived/2026/05"}}); !errors.Is(err, errDisk) {
		t.Errorf("an unlistable month: %v", err)
	}
	e.disk.SetFault(nil)
	if _, err := e.m.Get(ctx, kb, "../t"); !errors.Is(err, todo.ErrInvalid) {
		t.Errorf("an unsafe id: %v", err)
	}
	e.disk.WriteFile(kbStore+"/open/t.md", []byte("---\nbroken\n"))
	if _, err := e.m.Update(ctx, kb, "t", revOf("---\nbroken\n"), todo.Patch{"title": "x"}); !errors.Is(err, todo.ErrInvalid) {
		t.Errorf("updating a malformed task: %v", err)
	}
}

// A journal that fails is an error where the verb needs it, and only logged where a listing only
// wanted to note hand edits.
func TestJournalFailuresSurface(t *testing.T) {
	e := claimed(t)
	e.add(todo.Spec{Title: "t", ID: "t"})
	e.j.Fail = func(op string) error {
		if op == "Snapshot" {
			return todotest.ErrCrash
		}
		return nil
	}
	if l, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil || len(l.Tasks) != 1 {
		t.Errorf("a listing whose diff fails: %+v %v", l, err)
	}
	e.j.Fail = func(op string) error {
		if op == "Pending" {
			return todotest.ErrCrash
		}
		return nil
	}
	e.restart()
	if _, err := e.m.List(ctx, kb, todo.ListOpts{}); !errors.Is(err, todotest.ErrCrash) {
		t.Errorf("recovery that cannot read the journal: %v", err)
	}
	e.j.Fail = nil
}

// The diff reads what other writers did across the archive boundary: a task archived by hand is a
// move, a task brought back from the archive by hand is a move, and a task file broken by hand is
// not a removal.
func TestHandMovesAcrossTheArchive(t *testing.T) {
	e := claimed(t)
	a := e.add(todo.Spec{Title: "a", ID: "a", Status: "completed"})
	b := e.add(todo.Spec{Title: "b", ID: "b", Status: "archived"}) // the snapshot holds its archived row
	e.add(todo.Spec{Title: "c", ID: "c"})
	pre := len(e.rows())

	// a: archived by another writer, as auto-core archives
	archived := strings.Replace(strings.Replace(e.read(kbStore+"/"+a.Path), "status: completed", "status: archived", 1),
		"---\n\n<!--", "archived_at: \"2026-10-11T10:00:00-07:00\"\n---\n\n<!--", 1)
	e.disk.WriteFile(kbStore+"/archived/2026/10/a.md", []byte(archived))
	e.must(e.disk.FS.Remove(ctx, "kb/.todo-list/"+a.Path))
	// b: brought back by hand
	back := strings.Replace(strings.Replace(e.read(kbStore+"/"+b.Path), "status: archived", "status: open", 1),
		"archived_at: \"2026-10-11T10:00:00-07:00\"\n", "", 1)
	e.disk.WriteFile(kbStore+"/open/b.md", []byte(back))
	e.must(e.disk.FS.Remove(ctx, "kb/.todo-list/"+b.Path))
	// c: broken by hand
	e.disk.WriteFile(kbStore+"/open/c.md", []byte("---\nbroken\n"))

	if _, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil {
		t.Fatal(err)
	}
	rows := e.rows()[pre:]
	got := map[string]todo.Change{}
	for _, c := range rows {
		if !c.Hand {
			t.Errorf("not a hand row: %+v", c)
		}
		got[c.ID] = c
	}
	if c := got["a"]; len(rows) != 2 || c.Kind != todo.KindUpdate || c.Op != todo.OpMove || c.To != "archived" || c.ToPath != "archived/2026/10/a.md" {
		t.Errorf("archived by hand: %+v", rows)
	}
	if c := got["b"]; c.Kind != todo.KindUpdate || c.Op != todo.OpMove || c.FromPath != b.Path || c.ToPath != "open/b.md" {
		t.Errorf("unarchived by hand: %+v", c)
	}
	if _, ok := got["c"]; ok {
		t.Error("a file broken by hand was logged as removed")
	}
}

// A store a second daemon moved is reopened at its new path; a directory with no buckets is not a
// store until Scaffold; claiming twice and releasing twice are no-ops.
func TestStoreOpenEdges(t *testing.T) {
	e := claimed(t)
	e.add(todo.Spec{Title: "t", ID: "t"})
	other := todo.New(e.roots, e.disk, e.j)
	e.disk.MkdirAll("/elsewhere")
	if _, err := other.MoveStore(ctx, kb, "/elsewhere/todos"); err != nil {
		t.Fatal(err)
	}
	if task, err := e.m.Get(ctx, kb, "t"); err != nil || task.ID != "t" {
		t.Errorf("after another daemon moved the store: %+v %v", task, err)
	}
	e.disk.MkdirAll("/plain")
	if _, err := e.m.SetDir(ctx, kb, "/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.List(ctx, kb, todo.ListOpts{}); !errors.Is(err, todo.ErrNotAStore) {
		t.Errorf("a directory with no buckets: %v", err)
	}
	e.must(e.m.Claim(ctx, kb)) // a directory that is no store yet gets no marker
	if e.exists("/plain/.autodoc-owner") {
		t.Error("a claim marked a directory that is no store")
	}
	if _, err := e.m.Scaffold(ctx, kb); err != nil {
		t.Fatal(err)
	}
	e.must(e.m.Claim(ctx, kb)) // marked already
	if _, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil || !e.exists("/plain/.autodoc-owner") {
		t.Errorf("scaffolded: %v", err)
	}
	e.must(e.m.Release(ctx, kb))
	e.must(e.m.Release(ctx, kb))
}
