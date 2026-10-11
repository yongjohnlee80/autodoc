package todo_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// Every verb that changes a file writes its row, committed, with what it did.
func TestEveryVerbWritesItsRow(t *testing.T) {
	e := claimed(t)
	a := e.add(todo.Spec{Title: "a"})
	a, _ = e.m.Update(ctx, kb, a.ID, a.Rev, todo.Patch{"priority": "low"})
	a, _ = e.m.Assign(ctx, kb, a.ID, a.Rev, "agent:x", "why")
	a, _ = e.m.Status(ctx, kb, a.ID, a.Rev, "completed")
	e.must(e.m.Remove(ctx, kb, a.ID, a.Rev))
	e.disk.WriteFile(kbStore+"/open/bad.md", []byte("junk"))
	e.must(e.m.RemoveMalformed(ctx, kb, "open/bad.md"))
	var got []string
	for _, c := range e.rows() {
		if c.State != todo.StateCommitted || c.Hand {
			t.Errorf("%+v", c)
		}
		got = append(got, c.Kind+"/"+c.Op)
	}
	want := []string{"add/create", "update/in-place", "assign/move", "status/move", "remove/delete", "remove_malformed/delete"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows %v\nwant %v", got, want)
	}
}

// An edit made by hand is found by the snapshot diff and logged as an update naming its fields,
// never as an assign, even when it is the assignee that changed.
func TestHandEditsAreUpdatesNeverAssigns(t *testing.T) {
	e := claimed(t)
	a := e.add(todo.Spec{Title: "a"})
	p := kbStore + "/" + a.Path
	e.disk.WriteFile(p, []byte(strings.Replace(e.read(p), "title: a\n", "title: a\nassignee: \"agent:jarvis\"\n", 1)))
	e.disk.WriteFile(kbStore+"/deferred/byhand.md", []byte(taskFile("byhand", "deferred")))
	e.m.List(ctx, kb, todo.ListOpts{})
	rows := e.rows()
	if len(rows) != 3 {
		t.Fatalf("%+v", rows)
	}
	edit, made := rows[1], rows[2]
	if edit.Kind != todo.KindUpdate || !edit.Hand || edit.Reason != "" || !reflect.DeepEqual(edit.Fields, []string{"assignee"}) {
		t.Errorf("the edit: %+v", edit)
	}
	if made.Kind != todo.KindAdd || !made.Hand {
		t.Errorf("the new file: %+v", made)
	}
	for _, c := range rows {
		if c.Kind == todo.KindAssign {
			t.Error("a hand edit became an assign")
		}
	}
	// listing again logs nothing new
	e.m.List(ctx, kb, todo.ListOpts{})
	if len(e.rows()) != 3 {
		t.Error("the diff is not idempotent")
	}
	// a hand move between buckets is one update, and a deletion a remove
	b := e.get("byhand")
	e.disk.WriteFile(kbStore+"/open/byhand.md", []byte(strings.Replace(e.read(kbStore+"/"+b.Path), "status: deferred", "status: open", 1)))
	e.must(e.disk.FS.Remove(ctx, "kb/.todo-list/deferred/byhand.md"))
	e.must(e.disk.FS.Remove(ctx, "kb/.todo-list/"+strings.TrimPrefix(e.get(a.ID).Path, "")))
	e.m.List(ctx, kb, todo.ListOpts{})
	rows = e.rows()[3:]
	if len(rows) != 2 || rows[0].Kind != todo.KindUpdate || rows[0].Op != todo.OpMove || rows[0].To != "open" ||
		rows[1].Kind != todo.KindRemove || !rows[1].Hand {
		t.Errorf("%+v", rows)
	}
}

// The first sight of a store takes its snapshot silently: a store full of tasks is not 300 adds.
func TestFirstSightIsABaseline(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"a", "b", "c"} {
		e.disk.WriteFile(kbStore+"/open/"+id+".md", []byte(taskFile(id, "open")))
	}
	e.m.List(ctx, kb, todo.ListOpts{})
	if rows := e.rows(); len(rows) != 0 {
		t.Errorf("%d rows", len(rows))
	}
	if snap := mustSnapshot(t, e, e.storeID()); len(snap) != 3 {
		t.Errorf("snapshot %d rows", len(snap))
	}
}

// Consumers keep their cursor in the daemon. Delivery is at-least-once: without an ack the same
// changes come again. A cursor never moves back, nor past the head.
func TestConsumers(t *testing.T) {
	e := claimed(t)
	a := e.add(todo.Spec{Title: "a"})
	e.m.Assign(ctx, kb, a.ID, a.Rev, "agent:x", "do it")
	page, err := e.m.Changes(ctx, kb, "auto-core", 10)
	if err != nil || len(page.Changes) != 2 || page.Cursor != 2 || page.Resync {
		t.Fatalf("%+v %v", page, err)
	}
	if again, _ := e.m.Changes(ctx, kb, "auto-core", 10); !reflect.DeepEqual(again.Changes, page.Changes) {
		t.Error("an unacknowledged change was not delivered again")
	}
	e.must(e.m.Ack(ctx, kb, "auto-core", 2))
	if page, _ := e.m.Changes(ctx, kb, "auto-core", 10); len(page.Changes) != 0 {
		t.Errorf("after the ack: %+v", page)
	}
	e.must(e.m.Ack(ctx, kb, "auto-core", 1))
	if page, _ := e.m.Changes(ctx, kb, "auto-core", 10); len(page.Changes) != 0 {
		t.Error("the cursor moved back")
	}
	if err := e.m.Ack(ctx, kb, "auto-core", 99); !is(err, todo.ErrInvalid) {
		t.Errorf("past the head: %v", err)
	}
	if err := e.m.Ack(ctx, kb, "stranger", 1); !is(err, todo.ErrNotFound) {
		t.Errorf("an unknown consumer: %v", err)
	}
	if page, _ := e.m.Changes(ctx, kb, "auto-core", 1); page.Cursor != 2 {
		t.Error("the cursor is the head")
	}
	if _, err := e.m.Changes(ctx, kb, "", 1); !is(err, todo.ErrInvalid) {
		t.Errorf("no consumer: %v", err)
	}
}

// Pruning keeps every change a registered consumer has not acknowledged, however old; a consumer
// silent for 90 days is unregistered, and gets a resync when it returns.
func TestPruningAndResync(t *testing.T) {
	e := claimed(t)
	a := e.add(todo.Spec{Title: "a"})
	e.m.Changes(ctx, kb, "auto-core", 10) // registers at 0
	e.m.Changes(ctx, kb, "panel", 10)
	e.must(e.m.Ack(ctx, kb, "panel", 1))
	e.advance(31 * 24 * time.Hour)
	e.m.Changes(ctx, kb, "auto-core", 10) // seen, not acknowledged
	e.m.Changes(ctx, kb, "panel", 10)
	e.m.Refresh(ctx, kb)
	if len(e.rows()) != 1 {
		t.Fatal("an unacknowledged change was pruned")
	}
	e.must(e.m.Ack(ctx, kb, "auto-core", 1))
	e.m.Refresh(ctx, kb)
	if len(e.rows()) != 0 {
		t.Fatal("a change everyone acknowledged, past 30 days, was kept")
	}
	e.m.Assign(ctx, kb, a.ID, e.get(a.ID).Rev, "agent:x", "")
	e.advance(91 * 24 * time.Hour)
	e.m.Changes(ctx, kb, "panel", 10)
	e.m.Refresh(ctx, kb) // auto-core has been silent 91 days
	if kinds := e.eventKinds(); len(kinds) != 1 || kinds[0] != "todos.consumer_dropped" {
		t.Errorf("events %v", kinds)
	}
	page, err := e.m.Changes(ctx, kb, "auto-core", 10)
	if err != nil || !page.Resync || len(page.Snapshot) != 1 || page.Snapshot[0].Assignee != "agent:x" {
		t.Fatalf("the returning consumer: %+v %v", page, err)
	}
	if page, _ := e.m.Changes(ctx, kb, "auto-core", 10); page.Resync || len(page.Changes) != 0 {
		t.Errorf("after the resync: %+v", page)
	}
}
