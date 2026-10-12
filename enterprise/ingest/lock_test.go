package ingest

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// recycling makes a pool close every connection 100 ms after it opened, checking every 50 ms: the
// pool recycling a long-lived server sees over hours, in a second.
const recycling = "pool_max_conn_lifetime=100ms&pool_max_conn_idle_time=50ms&pool_health_check_period=50ms"

// holder is the backend holding the tenant's writer lock, or 0.
func (f *fixture) holder() int64 {
	f.t.Helper()
	s := f.scalar(f.business, `SELECT coalesce((SELECT pid `+pgstore.HeldAs+` LIMIT 1), 0)::text`, pgstore.WriterKey(f.schema, f.tenant))
	pid, _ := strconv.ParseInt(s, 10, 64)
	return pid
}

// waitFor polls cond for up to d.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %v", what, d)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Two servers on one tenant, each with its own pools, both recycling their connections: the
// second never takes the lock while the first holds it and writes, however often the first's pool
// connections are replaced, and takes it once the first lets go. The mutant, named here: the lock
// taken through the pool rather than on a pinned connection (a recycled connection takes the lock
// with it, and the second server takes it while the first still writes).
func TestTwoServersOnOneTenantNeverWriteTogether(t *testing.T) {
	f := newFixture(t)
	src := f.source(nil)
	stores := map[*Ingester]dao.DataConn{}
	server := func(name string) *Ingester {
		store := f.open("store-"+name, recycling, pgstore.WithSchema(f.schema))
		st, err := pgstore.New(store, f.tenant)
		if err != nil {
			t.Fatal(err)
		}
		in := New(st, f.tenant, src, f.business, f.open("locks-"+name, recycling), Options{Schema: f.schema,
			LockPoll: 20 * time.Millisecond, Now: func(context.Context) (time.Time, error) { return at(1_000_000), nil }, Lag: -1})
		t.Cleanup(in.Release)
		stores[in] = store
		return in
	}
	a, b := server("a"), server("b")
	if err := a.Acquire(f.ctx); err != nil {
		t.Fatal(err)
	}
	storePID := func(in *Ingester) string { return f.scalar(stores[in], "SELECT pg_backend_pid()::text") }
	before := storePID(a)
	lockPID := f.holder()
	for i := 1; i <= 8; i++ { // about a second of writing, with the pools recycling under it
		f.track(i, "A", "song", i)
		if _, err := a.Pass(f.ctx); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		ctx, cancel := context.WithTimeout(f.ctx, 100*time.Millisecond)
		err := b.Acquire(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("pass %d: the second server's Acquire: %v, want it still waiting", i, err)
		}
	}
	if storePID(a) == before {
		t.Fatalf("the first server's store pool kept backend %s for a second: nothing was recycled", before)
	}
	if got := f.holder(); got != lockPID {
		t.Fatalf("the lock moved from backend %d to %d", lockPID, got)
	}
	if _, err := b.Pass(f.ctx); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("the second server wrote without the lock: %v", err)
	}
	a.Release()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	if err := b.Acquire(ctx); err != nil {
		t.Fatalf("the second server's Acquire once the first let go: %v", err)
	}
	f.track(9, "A", "song", 9)
	if _, err := b.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.paths(); len(got) != 9 {
		t.Errorf("documents %v", got)
	}
}

// lateTimer is a context whose deadline has passed while its timer has not yet fired: Err is nil
// and Done still open. It is the window in which the driver, which bounds a write by the deadline,
// fails the write with an i/o timeout before the context says it ended.
type lateTimer struct {
	context.Context
	deadline time.Time
}

func (c lateTimer) Deadline() (time.Time, bool) { return c.deadline, true }

// A wait whose deadline has passed ends with the caller's error, though the driver's write timed
// out a moment before the context said so: AcquireWriter and Check answer
// context.DeadlineExceeded, not a failure to take the lock or the lock lost. The mutant, named
// here: ctx.Err() alone deciding (the i/o timeout comes back as the connection's failure).
func TestAWaitPastItsDeadlineEndsWithTheCallersError(t *testing.T) {
	f := newFixture(t)
	open, cancel := context.WithCancel(f.ctx)
	t.Cleanup(cancel)
	late := lateTimer{Context: open, deadline: time.Now().Add(-time.Millisecond)}

	locks := f.open("locks", "")
	f.scalar(locks, "SELECT 1") // an idle connection, so the pin succeeds and the write times out
	if _, err := pgstore.AcquireWriter(late, locks, f.schema, f.tenant, 20*time.Millisecond, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("AcquireWriter past its deadline: %v, want context.DeadlineExceeded", err)
	}
	l, err := pgstore.AcquireWriter(f.ctx, f.open("locks-2", ""), f.schema, f.tenant, 20*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Release)
	if err := l.Check(late); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, pgstore.ErrWriterLost) {
		t.Errorf("Check past its deadline: %v, want context.DeadlineExceeded", err)
	}
}

// Killing the lock's connection stops the writes: the next commit finds the lock gone, writes
// nothing and says ErrLockLost, and so does every write after it. The mutant, named here: a commit
// that does not confirm the lock (the page after the loss is written).
func TestLosingTheLockStopsTheWrites(t *testing.T) {
	f := newFixture(t)
	in := f.ingester(f.source(nil), Options{PingEvery: time.Hour}, "")
	f.track(1, "A", "song", 1)
	if _, err := in.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.exec("SELECT pg_terminate_backend($1)", f.holder())
	f.track(2, "A", "song", 2)
	if _, err := in.Pass(f.ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("a pass after the lock's connection was killed: %v, want ErrLockLost", err)
	}
	if _, err := in.Refresh(f.ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("a refresh after it: %v, want ErrLockLost", err)
	}
	if got := f.paths(); !slices.Equal(got, trackPaths(1)) {
		t.Errorf("documents %v: a write landed after the lock was lost", got)
	}
}

// Run confirms its lock every PingEvery: killing the lock's connection stops it with ErrLockLost
// while it sleeps between passes. The mutant, named here: Run without its ping (it sleeps on until
// its next pass, an hour away).
func TestRunStopsWhenItsLockIsLost(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "song", 1)
	in := New(f.st, f.tenant, f.source(nil), f.business, f.open("locks", ""), Options{Schema: f.schema,
		Interval: time.Hour, PingEvery: 50 * time.Millisecond, Lag: -1})
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	waitFor(t, 5*time.Second, "the first refresh", func() bool { return len(f.paths()) == 1 })
	f.exec("SELECT pg_terminate_backend($1)", f.holder())
	select {
	case err := <-done:
		cancel()
		if !errors.Is(err, ErrLockLost) {
			t.Fatalf("Run: %v, want ErrLockLost", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("Run went on after its lock was lost")
	}
}

// Run takes the lock, refreshes a tenant never refreshed, and when its context ends returns nil
// with the lock released: another server can take it at once. A second Run on the tenant waits
// meanwhile, and never writes. The mutant, named here: Run not releasing its lock when it stops.
func TestRunReleasesItsLockWhenItStops(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "song", 1)
	src := f.source(nil)
	ctx, cancel := context.WithCancel(f.ctx)
	in := New(f.st, f.tenant, src, f.business, f.open("locks", ""), Options{Schema: f.schema, Interval: 50 * time.Millisecond, Lag: -1})
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	// Run's first run is a refresh: its record commits after its documents
	waitFor(t, 5*time.Second, "the first refresh", func() bool {
		r := must[pgstore.Refresh](t)(f.st.Refreshed(f.ctx))
		return !r.Finished.IsZero() && len(f.paths()) == 1
	})
	// a second server waits for the lock and never writes
	waiting := New(f.st, f.tenant, src, f.business, f.open("locks-2", ""), Options{Schema: f.schema, LockPoll: 20 * time.Millisecond})
	wctx, wcancel := context.WithTimeout(f.ctx, 300*time.Millisecond)
	if err := waiting.Run(wctx); err != nil {
		t.Errorf("a waiting Run whose context ended: %v, want nil", err)
	}
	wcancel()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if pid := f.holder(); pid != 0 {
		t.Errorf("backend %d still holds the lock after Run stopped", pid)
	}
}

// Confirm says whether the tenant is this server's now, for the embedding worker beside the
// ingester: not before the lock is taken, yes while it is held, lost once its connection is
// killed, and not after it is let go. Committed is called once per committed batch, and not for a
// pass that read nothing. The mutants, named here: Confirm answering nil with no lock held (the
// worker would write a tenant another server holds), and the commit not calling Committed.
func TestConfirmSaysWhetherTheTenantIsThisServers(t *testing.T) {
	f := newFixture(t)
	commits := 0
	in := New(f.st, f.tenant, f.source(nil), f.business, f.open("locks", ""), Options{Schema: f.schema,
		LockPoll: 20 * time.Millisecond, Now: func(context.Context) (time.Time, error) { return at(1_000_000), nil }, Lag: -1,
		Committed: func() { commits++ }})
	t.Cleanup(in.Release)
	if err := in.Confirm(f.ctx); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("Confirm before Acquire: %v, want ErrNotLocked", err)
	}
	if err := in.Acquire(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := in.Confirm(f.ctx); err != nil {
		t.Fatalf("Confirm while the lock is held: %v", err)
	}
	for i := 1; i <= 4; i++ {
		f.track(i, "A", "song", i)
	}
	if st, err := in.Pass(f.ctx); err != nil || st.Pages != 2 || commits != 2 {
		t.Fatalf("a pass of two pages: %+v, %v; Committed called %d times, want 2", st, err, commits)
	}
	if _, err := in.Pass(f.ctx); err != nil || commits != 2 {
		t.Fatalf("a pass that read nothing: %v; Committed called %d times, want still 2", err, commits)
	}
	f.exec(`SELECT pg_terminate_backend($1)`, f.holder())
	waitFor(t, 5*time.Second, "the lock seen lost", func() bool { return errors.Is(in.Confirm(f.ctx), ErrLockLost) })
	in.Release()
	if err := in.Confirm(f.ctx); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("Confirm after Release: %v, want ErrNotLocked", err)
	}
}
