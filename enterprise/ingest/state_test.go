package ingest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// An ingester publishes its state for an operator: not locked before it runs; locked once it holds
// its lock; each run's outcome, with what it did and its failure's code. A view whose query fails
// (its table gone) is "view", never the error's text, and clears once the view answers again. A
// second ingester of the tenant shows as waiting for the first's lock. The mutants, named here: the
// runs not recorded; the view's failure not told from the database's.
func TestAnIngesterPublishesItsState(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "good", 1)
	ctx, cancel := context.WithCancel(f.ctx)
	in := New(f.st, f.tenant, f.source(nil), f.business, f.open("locks", ""), Options{Schema: f.schema, Interval: 20 * time.Millisecond, Lag: -1})
	if st := in.State(); st.Locked || st.Waiting || !st.LastPass.At.IsZero() || !st.LastRefresh.At.IsZero() {
		t.Fatalf("before it runs: %+v", st)
	}
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, 5*time.Second, "a refresh and a pass recorded", func() bool {
		st := in.State()
		return st.Locked && !st.LastRefresh.At.IsZero() && !st.LastPass.At.IsZero()
	})
	if st := in.State(); st.LastRefresh.Stats.Written != 1 || st.LastRefresh.Error != opcode.None || st.LastPass.Error != opcode.None {
		t.Errorf("after a refresh and a pass: %+v", st)
	}

	f.exec(`ALTER TABLE track RENAME TO track_gone`)
	waitFor(t, 5*time.Second, "the view's failure", func() bool { return in.State().LastPass.Error == opcode.View })
	f.exec(`ALTER TABLE track_gone RENAME TO track`)
	waitFor(t, 5*time.Second, "the view answering again", func() bool { return in.State().LastPass.Error == opcode.None })

	second := New(f.st, f.tenant, f.source(nil), f.business, f.open("locks2", ""), Options{Schema: f.schema, Interval: 20 * time.Millisecond, Lag: -1, LockPoll: 20 * time.Millisecond})
	ctx2, cancel2 := context.WithCancel(f.ctx)
	done2 := make(chan error, 1)
	go func() { done2 <- second.Run(ctx2) }()
	t.Cleanup(func() { cancel2(); <-done2 })
	waitFor(t, 5*time.Second, "the second ingester waiting", func() bool { st := second.State(); return st.Waiting && !st.Locked })
}

// A run's failure is coded by its kind: the lock, the view's order, the view's query, else the
// index's database.
func TestARunsFailureIsCodedByItsKind(t *testing.T) {
	for _, c := range []struct {
		err  error
		want opcode.Code
	}{
		{nil, opcode.None},
		{fmt.Errorf("x: %w", ErrLockLost), opcode.LockLost},
		{fmt.Errorf("x: %w", ErrOrder), opcode.Order},
		{ErrStalled, opcode.Order},
		{fmt.Errorf("page: %w", viewError{errors.New("relation track does not exist")}), opcode.View},
		{errors.New("pgstore: put: connection reset"), opcode.Database},
	} {
		if got := codeOf(c.err); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
}
