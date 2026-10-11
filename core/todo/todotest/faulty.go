package todotest

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// Faulty is any todo.Journal that fails on demand: what lets core/todo's crash suite stop a
// mutation at any step on the in-memory journal and on core/store's SQLite one alike.
type Faulty struct {
	todo.Journal

	// Fail, when set, is called with a method's name before the journal is; an error is that
	// method's result and the journal is not called.
	Fail func(op string) error
}

// NewFaulty is j, failing on demand.
func NewFaulty(j todo.Journal) *Faulty { return &Faulty{Journal: j} }

var _ todo.Journal = (*Faulty)(nil)

func (f *Faulty) fail(op string) error {
	if f.Fail == nil {
		return nil
	}
	return f.Fail(op)
}

// Rows is every row of a store, in seq order, read through the port: its pending and staged rows
// and its committed ones.
func (f *Faulty) Rows(store string) []todo.Change {
	ctx := context.Background()
	pending, _ := f.Journal.Pending(ctx, store)
	committed, _ := f.Journal.After(ctx, store, 0, math.MaxInt)
	out := append(pending, committed...)
	slices.SortFunc(out, func(a, b todo.Change) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}

func (f *Faulty) Begin(ctx context.Context, c todo.Change) (int64, error) {
	if err := f.fail("Begin"); err != nil {
		return 0, err
	}
	return f.Journal.Begin(ctx, c)
}

func (f *Faulty) Stage(ctx context.Context, seq int64) error {
	if err := f.fail("Stage"); err != nil {
		return err
	}
	return f.Journal.Stage(ctx, seq)
}

func (f *Faulty) Commit(ctx context.Context, seq int64, e todo.Edit) error {
	if err := f.fail("Commit"); err != nil {
		return err
	}
	return f.Journal.Commit(ctx, seq, e)
}

func (f *Faulty) Drop(ctx context.Context, seq int64) error {
	if err := f.fail("Drop"); err != nil {
		return err
	}
	return f.Journal.Drop(ctx, seq)
}

func (f *Faulty) Pending(ctx context.Context, store string) ([]todo.Change, error) {
	if err := f.fail("Pending"); err != nil {
		return nil, err
	}
	return f.Journal.Pending(ctx, store)
}

func (f *Faulty) Record(ctx context.Context, c todo.Change, e todo.Edit) (int64, error) {
	if err := f.fail("Record"); err != nil {
		return 0, err
	}
	return f.Journal.Record(ctx, c, e)
}

func (f *Faulty) Head(ctx context.Context, store string) (int64, error) {
	if err := f.fail("Head"); err != nil {
		return 0, err
	}
	return f.Journal.Head(ctx, store)
}

func (f *Faulty) After(ctx context.Context, store string, after int64, limit int) ([]todo.Change, error) {
	if err := f.fail("After"); err != nil {
		return nil, err
	}
	return f.Journal.After(ctx, store, after, limit)
}

func (f *Faulty) Prune(ctx context.Context, store string, before time.Time, upTo int64) (int, error) {
	if err := f.fail("Prune"); err != nil {
		return 0, err
	}
	return f.Journal.Prune(ctx, store, before, upTo)
}

func (f *Faulty) Snapshot(ctx context.Context, store string) ([]todo.SnapshotRow, error) {
	if err := f.fail("Snapshot"); err != nil {
		return nil, err
	}
	return f.Journal.Snapshot(ctx, store)
}

func (f *Faulty) Baseline(ctx context.Context, store string, rows []todo.SnapshotRow) error {
	if err := f.fail("Baseline"); err != nil {
		return err
	}
	return f.Journal.Baseline(ctx, store, rows)
}

func (f *Faulty) Consumers(ctx context.Context, store string) ([]todo.Consumer, error) {
	if err := f.fail("Consumers"); err != nil {
		return nil, err
	}
	return f.Journal.Consumers(ctx, store)
}

func (f *Faulty) PutConsumer(ctx context.Context, store string, c todo.Consumer) error {
	if err := f.fail("PutConsumer"); err != nil {
		return err
	}
	return f.Journal.PutConsumer(ctx, store, c)
}

func (f *Faulty) Settings(ctx context.Context) (todo.Settings, error) {
	if err := f.fail("Settings"); err != nil {
		return todo.Settings{}, err
	}
	return f.Journal.Settings(ctx)
}

func (f *Faulty) UpdateSettings(ctx context.Context, fn func(*todo.Settings) error) error {
	if err := f.fail("UpdateSettings"); err != nil {
		return err
	}
	return f.Journal.UpdateSettings(ctx, fn)
}
