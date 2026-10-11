package todotest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

var ctx = context.Background()

// The in-memory journal is the reference: core/store's SQLite one runs the same suite.
func TestTheInMemoryJournalConforms(t *testing.T) {
	todotest.RunJournalConformance(t, todotest.Harness{
		New:    func(*testing.T) todo.Journal { return todotest.NewJournal() },
		Reopen: func(_ *testing.T, j todo.Journal) todo.Journal { return j }, // it keeps what it holds for its life
		Break: func(_ *testing.T, j todo.Journal) func() {
			mem := j.(*todotest.Journal)
			mem.Fail = func(op string) error {
				switch op {
				case "Commit", "Record", "UpdateSettings":
					return todotest.ErrCrash
				}
				return nil
			}
			return func() { mem.Fail = nil }
		},
	})
}

// So does any journal behind Faulty, which fails only when asked.
func TestFaultyConformsAndFailsOnDemand(t *testing.T) {
	var f *todotest.Faulty
	todotest.RunJournalConformance(t, todotest.Harness{
		New:    func(*testing.T) todo.Journal { f = todotest.NewFaulty(todotest.NewJournal()); return f },
		Reopen: func(_ *testing.T, j todo.Journal) todo.Journal { return j },
		Break: func(_ *testing.T, j todo.Journal) func() {
			fj := j.(*todotest.Faulty)
			fj.Fail = func(op string) error {
				switch op {
				case "Commit", "Record", "UpdateSettings":
					return todotest.ErrCrash
				}
				return nil
			}
			return func() { fj.Fail = nil }
		},
	})
}

// A Fail hook fails every method, before it changes anything: on the in-memory journal, and on
// Faulty around any.
func TestFailStopsEveryMethod(t *testing.T) {
	mem := todotest.NewJournal()
	faulty := todotest.NewFaulty(todotest.NewJournal())
	for name, j := range map[string]todo.Journal{"in-memory": mem, "faulty": faulty} {
		var ops []string
		crash := func(op string) error { ops = append(ops, op); return todotest.ErrCrash }
		mem.Fail, faulty.Fail = crash, crash
		_, err1 := j.Begin(ctx, todo.Change{Store: "A"})
		_, err2 := j.Pending(ctx, "A")
		_, err3 := j.Record(ctx, todo.Change{Store: "A"}, todo.Edit{})
		_, err4 := j.Head(ctx, "A")
		_, err5 := j.After(ctx, "A", 0, 1)
		_, err6 := j.Prune(ctx, "A", time.Now(), 1)
		_, err7 := j.Snapshot(ctx, "A")
		_, err8 := j.Consumers(ctx, "A")
		_, err9 := j.Settings(ctx)
		errs := []error{err1, err2, err3, err4, err5, err6, err7, err8, err9,
			j.Stage(ctx, 1), j.Commit(ctx, 1, todo.Edit{}), j.Drop(ctx, 1), j.Baseline(ctx, "A", nil),
			j.PutConsumer(ctx, "A", todo.Consumer{Name: "c"}), j.UpdateSettings(ctx, func(*todo.Settings) error { return nil })}
		for i, err := range errs {
			if !errors.Is(err, todotest.ErrCrash) {
				t.Errorf("%s: call %d: %v", name, i, err)
			}
		}
		if len(ops) != len(errs) {
			t.Errorf("%s: Fail saw %v", name, ops)
		}
		mem.Fail, faulty.Fail = nil, nil
		if rows := todotest.NewFaulty(j).Rows("A"); len(rows) != 0 {
			t.Errorf("%s: a failed call left %+v", name, rows)
		}
	}
}
