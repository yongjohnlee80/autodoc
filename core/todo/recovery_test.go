package todo_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// crasher kills the daemon at its nth step: the nth journal write or disk mutation fails, and so
// does everything after it, as nothing runs after a crash.
type crasher struct {
	mu        sync.Mutex
	n, at     int
	dead      bool
	mutations map[string]bool
}

func (c *crasher) step(op string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return todotest.ErrCrash
	}
	if !c.mutations[op] {
		return nil
	}
	c.n++
	if c.n == c.at {
		c.dead = true
		return todotest.ErrCrash
	}
	return nil
}

func (e *env) crashAt(at int) *crasher {
	c := &crasher{at: at, mutations: map[string]bool{"Begin": true, "Stage": true, "Commit": true, "Drop": true, "Record": true,
		"Baseline": true, "PutConsumer": true, "UpdateSettings": true, "write": true, "create": true, "remove": true, "rename": true, "mkdir": true}}
	e.j.Fail = c.step
	e.disk.SetFault(func(op, _ string) error { return c.step(op) })
	return c
}

func (e *env) revive() {
	e.j.Fail = nil
	e.disk.SetFault(nil)
	e.restart()
}

func revOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// recoveryCase is one kind of change: how to set it up, make it, and tell the disk before it from
// the disk after it.
type recoveryCase struct {
	name, kind string
	setup      func(e *env) func() error
	before     func(e *env) bool
	after      func(e *env) bool
}

func recoveryCases() []recoveryCase {
	var seeded todo.Task
	seed := func(e *env, spec todo.Spec) {
		seeded = e.add(spec)
	}
	open := func(id string) string { return kbStore + "/open/" + id + ".md" }
	return []recoveryCase{
		{name: "create", kind: todo.KindAdd,
			setup: func(e *env) func() error {
				return func() error { _, err := e.m.Add(ctx, kb, todo.Spec{Title: "new one"}); return err }
			},
			before: func(e *env) bool { return !e.exists(open("2026-10-11-new-one")) },
			after:  func(e *env) bool { return e.exists(open("2026-10-11-new-one")) }},
		{name: "create by assign", kind: todo.KindAssign,
			setup: func(e *env) func() error {
				return func() error { _, err := e.m.Add(ctx, kb, todo.Spec{Title: "theirs", Assignee: "agent:x"}); return err }
			},
			before: func(e *env) bool { return !e.exists(kbStore + "/in-progress/2026-10-11-theirs.md") },
			after:  func(e *env) bool { return e.exists(kbStore + "/in-progress/2026-10-11-theirs.md") }},
		{name: "in-place", kind: todo.KindUpdate,
			setup: func(e *env) func() error {
				seed(e, todo.Spec{Title: "t", ID: "t"})
				return func() error {
					_, err := e.m.Update(ctx, kb, "t", seeded.Rev, todo.Patch{"title": "changed"})
					return err
				}
			},
			before: func(e *env) bool { return strings.Contains(e.read(open("t")), "title: t\n") },
			after:  func(e *env) bool { return strings.Contains(e.read(open("t")), "title: changed\n") }},
		{name: "move", kind: todo.KindStatus,
			setup: func(e *env) func() error {
				seed(e, todo.Spec{Title: "t", ID: "t"})
				return func() error { _, err := e.m.Status(ctx, kb, "t", seeded.Rev, "completed"); return err }
			},
			before: func(e *env) bool { return e.exists(open("t")) && !e.exists(kbStore+"/completed/t.md") },
			after:  func(e *env) bool { return !e.exists(open("t")) && e.exists(kbStore+"/completed/t.md") }},
		{name: "move by assign", kind: todo.KindAssign,
			setup: func(e *env) func() error {
				seed(e, todo.Spec{Title: "t", ID: "t"})
				return func() error { _, err := e.m.Assign(ctx, kb, "t", seeded.Rev, "agent:x", "go"); return err }
			},
			before: func(e *env) bool { return e.exists(open("t")) && !e.exists(kbStore+"/in-progress/t.md") },
			after:  func(e *env) bool { return !e.exists(open("t")) && e.exists(kbStore+"/in-progress/t.md") }},
		{name: "archive by refresh", kind: todo.KindArchive,
			setup: func(e *env) func() error {
				e.disk.WriteFile(kbStore+"/completed/old.md", []byte(strings.Replace(taskFile("old", "completed"),
					"completed_at: \"2026-10-01T09:00:00-07:00\"", "completed_at: \"2026-08-01T09:00:00-07:00\"", 1)))
				return func() error { _, err := e.m.Refresh(ctx, kb); return err }
			},
			before: func(e *env) bool { return e.exists(kbStore + "/completed/old.md") },
			after: func(e *env) bool {
				return !e.exists(kbStore+"/completed/old.md") && e.exists(kbStore+"/archived/2026/10/old.md")
			}},
		{name: "delete", kind: todo.KindRemove,
			setup: func(e *env) func() error {
				seed(e, todo.Spec{Title: "t", ID: "t"})
				return func() error { return e.m.Remove(ctx, kb, "t", seeded.Rev) }
			},
			before: func(e *env) bool { return e.exists(open("t")) },
			after:  func(e *env) bool { return !e.exists(open("t")) }},
		{name: "delete malformed", kind: todo.KindRemoveMalformed,
			setup: func(e *env) func() error {
				e.disk.WriteFile(open("bad"), []byte("---\nnot: a task\n---\n"))
				return func() error { return e.m.RemoveMalformed(ctx, kb, "open/bad.md") }
			},
			before: func(e *env) bool { return e.exists(open("bad")) },
			after:  func(e *env) bool { return !e.exists(open("bad")) }},
		{name: "store move", kind: todo.KindMoveStore,
			setup: func(e *env) func() error {
				seed(e, todo.Spec{Title: "t", ID: "t"})
				e.disk.MkdirAll("/elsewhere")
				return func() error { _, err := e.m.MoveStore(ctx, kb, "/elsewhere/todos"); return err }
			},
			before: func(e *env) bool { return e.exists(kbStore+"/open/t.md") && !e.exists("/elsewhere/todos/open/t.md") },
			after:  func(e *env) bool { return !e.exists(kbStore+"/open/t.md") && e.exists("/elsewhere/todos/open/t.md") }},
	}
}

// For every kind of change, a crash is injected at every step it takes: after the pending row,
// after each file step (both halves of a move, each step of a delete), before and inside the
// commit. After a restart, recovery leaves no row pending, and a change row exists if and only if
// the file change happened, with a snapshot that matches the disk (so no hand edit is logged).
func TestRecoveryAfterACrashAtEveryStep(t *testing.T) {
	for _, rc := range recoveryCases() {
		t.Run(rc.name, func(t *testing.T) {
			for at := 1; ; at++ {
				e := claimed(t)
				e.must(e.m.Claim(ctx, kb))
				op := rc.setup(e)
				if _, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil { // the snapshot sees the setup
					t.Fatal(err)
				}
				e.j.PutConsumer(ctx, e.storeID(), todo.Consumer{Name: "auto-core", Acked: 1})
				id := e.storeID()
				pre := len(e.j.Rows(id))
				if !rc.before(e) {
					t.Fatal("the setup is not the before state")
				}
				c := e.crashAt(at)
				opErr := op()
				crashed := c.dead
				e.revive()
				if _, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil {
					t.Fatalf("step %d: list after the crash: %v", at, err)
				}
				if r, _ := e.m.Resolve(ctx, kb); r.StoreID != id || !r.Claimed {
					t.Fatalf("step %d: the store lost its id or claim: %+v", at, r)
				}
				var made, hand int
				for _, row := range e.j.Rows(id)[min(pre, len(e.j.Rows(id))):] {
					switch {
					case row.State != todo.StateCommitted:
						t.Errorf("step %d: row left %s: %+v", at, row.State, row)
					case row.Hand:
						hand++
					case row.Kind == rc.kind:
						made++
					}
				}
				switch {
				case made > 1:
					t.Errorf("step %d: %d rows for one change", at, made)
				case made == 1 && !rc.after(e):
					t.Errorf("step %d: a committed row, but the change did not happen", at)
				case made == 0 && !rc.before(e):
					t.Errorf("step %d: the change happened (or half did) with no row", at)
				}
				if hand != 0 {
					t.Errorf("step %d: %d hand edits logged: the snapshot does not match the disk", at, hand)
				}
				if cs, _ := e.j.Consumers(ctx, id); len(cs) != 1 || cs[0].Acked != 1 {
					t.Errorf("step %d: the consumer's cursor moved: %+v", at, cs)
				}
				for _, r := range mustSnapshot(t, e, id) {
					if got := revOf(e.read(storePath(e) + "/" + r.Path)); got != r.Rev {
						t.Errorf("step %d: snapshot row %s does not match its file", at, r.Path)
					}
				}
				if trash, _ := e.disk.FS.ReadDir(ctx, strings.TrimPrefix(storePath(e), "/")+"/.trash"); len(trash) != 0 {
					t.Errorf("step %d: %d files left in the trash", at, len(trash))
				}
				if !crashed {
					if opErr != nil {
						t.Fatalf("no crash, and still: %v", opErr)
					}
					if made != 1 {
						t.Errorf("the change without a crash wrote %d rows", made)
					}
					return // every step has been crashed at
				}
			}
		})
	}
}

func mustSnapshot(t *testing.T, e *env, id string) []todo.SnapshotRow {
	rows, err := e.j.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func storePath(e *env) string {
	r, _ := e.m.Resolve(ctx, kb)
	return r.Store
}

// A file changed outside the daemon while its change was pending is ambiguous: the row goes, the
// ambiguity is reported, and the snapshot diff logs the disk as a hand edit.
func TestRecoveryCallsAForeignChangeAmbiguous(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "t", ID: "t"})
	// crash after the destination is written, before the source is removed
	e.disk.SetFault(func(op, name string) error {
		if op == "remove" && strings.HasSuffix(name, "/open/t.md") {
			e.j.Fail = func(string) error { return todotest.ErrCrash }
			return todotest.ErrCrash
		}
		return nil
	})
	if _, err := e.m.Status(ctx, kb, "t", task.Rev, "deferred"); err == nil {
		t.Fatal("no crash")
	}
	e.revive()
	edited := strings.Replace(e.read(kbStore+"/open/t.md"), "Body", "Hand-edited body", 1)
	e.disk.WriteFile(kbStore+"/open/t.md", []byte(strings.Replace(e.read(kbStore+"/open/t.md"), "title: t", "title: hand", 1)+edited[:0]))
	if _, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(e.pending()) != 0 {
		t.Error("the row was left pending")
	}
	var status, hand int
	for _, c := range e.rows() {
		switch {
		case c.Kind == todo.KindStatus:
			status++
		case c.Hand:
			hand++
		}
	}
	if status != 0 || hand == 0 {
		t.Errorf("status rows %d (want 0), hand rows %d (want some)", status, hand)
	}
	if kinds := e.eventKinds(); len(kinds) != 1 || kinds[0] != "todos.ambiguous" {
		t.Errorf("events %v", kinds)
	}
	if !e.exists(kbStore+"/open/t.md") || !e.exists(kbStore+"/deferred/t.md") {
		t.Error("recovery removed a file it could not account for")
	}
}

// A pending delete whose file is gone, and is not in the trash, was removed by someone else.
func TestRecoveryOfAForeignRemoval(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "t", ID: "t"})
	e.disk.SetFault(func(op, name string) error {
		if op == "mkdir" && strings.HasSuffix(name, "/.trash") {
			e.j.Fail = func(string) error { return todotest.ErrCrash }
			return todotest.ErrCrash
		}
		return nil
	})
	e.m.Remove(ctx, kb, "t", task.Rev)
	e.revive()
	e.must(e.disk.FS.Remove(ctx, "kb/.todo-list/open/t.md"))
	e.m.List(ctx, kb, todo.ListOpts{})
	for _, c := range e.rows() {
		if c.Kind == todo.KindRemove && !c.Hand {
			t.Errorf("the daemon took a removal it did not make: %+v", c)
		}
	}
	if kinds := e.eventKinds(); len(kinds) != 1 || kinds[0] != "todos.ambiguous" {
		t.Errorf("events %v", kinds)
	}
}

// A store move that left both directories, or neither, makes the store read-only until a person
// decides.
func TestAnAmbiguousStoreMoveBlocksWrites(t *testing.T) {
	e := claimed(t)
	e.add(todo.Spec{Title: "t", ID: "t"})
	e.disk.MkdirAll("/elsewhere")
	e.disk.SetFault(func(op, _ string) error {
		if op == "rename" {
			e.j.Fail = func(string) error { return todotest.ErrCrash }
			return todotest.ErrCrash
		}
		return nil
	})
	if _, err := e.m.MoveStore(ctx, kb, "/elsewhere/todos"); err == nil {
		t.Fatal("no crash")
	}
	e.revive()
	e.disk.MkdirAll("/elsewhere/todos") // someone made the target meanwhile
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "x"}); !is(err, todo.ErrNotWritable) {
		t.Errorf("a write to a store with an ambiguous move: %v", err)
	}
	if _, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil {
		t.Errorf("reads go on: %v", err)
	}
	if kinds := e.eventKinds(); len(kinds) == 0 || kinds[0] != "todos.store_move_blocked" {
		t.Errorf("events %v", kinds)
	}
}
