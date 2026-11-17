package ingest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// running is an ingester of the deletes fixture's source run on a clock the test moves (seconds
// past t0, the database's now), with a lag of 10 s and a pass every 20 ms.
func running(t *testing.T, f *fixture, src *Source, now *atomic.Int64) *Ingester {
	t.Helper()
	in := f.ingester(src, Options{Lag: 10 * time.Second, Interval: 20 * time.Millisecond, Refresh: time.Hour,
		Now: func(context.Context) (time.Time, error) { return at(int(now.Load())), nil }}, "")
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return in
}

// caughtUpTo waits for the ingester's rows and deletes to be caught up to sec.
func caughtUpTo(t *testing.T, in *Ingester, sec int) {
	t.Helper()
	waitFor(t, 10*time.Second, "caught up to "+at(sec).String(), func() bool {
		st := in.State()
		return st.CaughtUp.Equal(at(sec)) && st.DeletesCaughtUp.Equal(at(sec))
	})
}

// How far the index is caught up (State.CaughtUp, DeletesCaughtUp) is the bound the last complete
// run read to, not the cursor: on a quiet source it follows now − lag while the cursor stays put; a
// row newer than that bound is past it until a pass reads it; a pass that fails holds it where it
// was, so the lag grows, and the next that completes moves it on. The deletes view's is its own: a
// failing deletes view holds it while the rows' moves on. The mutants, named here: CaughtUp moved
// by a failed pass; DeletesCaughtUp moved by a failed deletes pass.
func TestTheIndexIsCaughtUpToTheLastCompleteRunsBound(t *testing.T) {
	f, src := deletesFixture(t, "")
	f.track(1, "A", "One", 1)
	var now atomic.Int64
	now.Store(100)
	in := running(t, f, src, &now)
	caughtUpTo(t, in, 90) // the first run, a refresh, read to now − lag

	// quiet: no row moves, the cursor stays at track 1's updated_at, and the bound follows the clock
	for _, sec := range []int{200, 300} {
		now.Store(int64(sec))
		caughtUpTo(t, in, sec-10)
	}
	if c, err := f.st.Cursor(f.ctx); err != nil || !c.UpdatedAt.Equal(at(1)) {
		t.Fatalf("the cursor on a quiet source: %+v, %v; want it at track 1's updated_at", c, err)
	}

	// a row inside the lag is past the bound: the index is not caught up to it, until a pass reads it
	f.track(2, "A", "Two", 295)
	time.Sleep(100 * time.Millisecond) // several passes at now 300
	if st := in.State(); !st.CaughtUp.Before(at(295)) {
		t.Fatalf("caught up to %v with a row at %v unread", st.CaughtUp, at(295))
	}
	now.Store(400)
	caughtUpTo(t, in, 390)

	// a failing pass holds the bound where it was, and the lag grows; a pass that completes moves it
	f.exec(`ALTER TABLE track RENAME TO track_away`)
	now.Store(500)
	waitFor(t, 10*time.Second, "a failed pass", func() bool { return in.State().LastPass.Error == opcode.View })
	time.Sleep(100 * time.Millisecond)
	if st := in.State(); !st.CaughtUp.Equal(at(390)) {
		t.Errorf("caught up to %v after failing passes; want it held at %v", st.CaughtUp, at(390))
	}
	f.exec(`ALTER TABLE track_away RENAME TO track`)
	caughtUpTo(t, in, 490)

	// a failing deletes view holds its own bound; the rows' moves on
	f.exec(`ALTER TABLE track_deleted RENAME TO track_deleted_away`)
	now.Store(600)
	waitFor(t, 10*time.Second, "the rows caught up to 590", func() bool { return in.State().CaughtUp.Equal(at(590)) })
	time.Sleep(100 * time.Millisecond)
	if st := in.State(); !st.DeletesCaughtUp.Equal(at(490)) || st.DeletesError == opcode.None {
		t.Errorf("the deletes view failing: caught up to %v, error %q; want it held at %v", st.DeletesCaughtUp, st.DeletesError, at(490))
	}
	f.exec(`ALTER TABLE track_deleted_away RENAME TO track_deleted`)
	caughtUpTo(t, in, 590)
}
