package ingest

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// recycling makes a pool close every connection 100 ms after it opened, checking every 50 ms: the
// pool recycling a long-lived server sees over hours, in a second.
const recycling = "pool_max_conn_lifetime=100ms&pool_max_conn_idle_time=50ms&pool_health_check_period=50ms"

// lease is the tenant's writer lease row: its holder ("" when released or never claimed) and
// whether it is live by the database's clock.
func (f *fixture) lease() (holder string, live bool) {
	f.t.Helper()
	rows, err := f.store.QueryContext(f.ctx, `SELECT holder, lease_until > clock_timestamp() FROM rag_writer_lease WHERE scope = $1`, pgstore.LeaseScope(f.tenant))
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&holder, &live); err != nil {
			f.t.Fatal(err)
		}
	}
	return holder, live
}

// takeOver makes the tenant's lease another server's, as a standby's claim of an expired lease
// does: the next epoch, another holder, live for an hour.
func (f *fixture) takeOver() {
	f.t.Helper()
	if _, err := f.store.ExecContext(f.ctx, `UPDATE rag_writer_lease SET holder = 'another server', epoch = epoch + 1,
		lease_until = clock_timestamp() + interval '1 hour' WHERE scope = $1`, pgstore.LeaseScope(f.tenant)); err != nil {
		f.t.Fatal(err)
	}
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
// second never takes the lease while the first holds it and writes, however often the first's
// pool connections are replaced, and takes it once the first lets go. The mutant, named here: the
// claim without its liveness check (the second server takes the live lease while the first still
// writes).
func TestTwoServersOnOneTenantNeverWriteTogether(t *testing.T) {
	f := newFixture(t)
	src := f.source(nil)
	stores := map[*Ingester]dao.DataConn{}
	server := func(name string) *Ingester {
		store := f.leases("store-"+name, recycling)
		st, err := pgstore.New(store, f.tenant)
		if err != nil {
			t.Fatal(err)
		}
		in := New(st, f.tenant, src, f.business, store, Options{
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
	holder, _ := f.lease()
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
	if got, live := f.lease(); got != holder || !live {
		t.Fatalf("the lease moved from %s to %s (live %v)", holder, got, live)
	}
	if _, err := b.Pass(f.ctx); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("the second server wrote without the lease: %v", err)
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
// out a moment before the context said so: AcquireWriter answers context.DeadlineExceeded, not a
// failure to take the lease. The mutant, named here: ctx.Err() alone deciding (the i/o timeout
// comes back as the connection's failure).
func TestAWaitPastItsDeadlineEndsWithTheCallersError(t *testing.T) {
	f := newFixture(t)
	open, cancel := context.WithCancel(f.ctx)
	t.Cleanup(cancel)
	late := lateTimer{Context: open, deadline: time.Now().Add(-time.Millisecond)}
	leases := f.leases("leases", "")
	f.scalar(leases, "SELECT 1") // an idle connection, so the claim's write times out, not its connect
	if _, err := pgstore.AcquireWriter(late, leases, f.tenant, 0, 20*time.Millisecond, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("AcquireWriter past its deadline: %v, want context.DeadlineExceeded", err)
	}
}

// A lease taken over stops the writes: the next page's fence finds it another's, writes nothing
// and says ErrLockLost, and so does every write after it. The mutant, named here: the ingester
// writing through its Store unfenced (the page after the takeover is written).
func TestALeaseTakenOverStopsTheWrites(t *testing.T) {
	f := newFixture(t)
	in := f.ingester(f.source(nil), Options{PingEvery: time.Hour}, "")
	f.track(1, "A", "song", 1)
	if _, err := in.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.takeOver()
	f.track(2, "A", "song", 2)
	if _, err := in.Pass(f.ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("a pass after the lease was taken over: %v, want ErrLockLost", err)
	}
	if _, err := in.Refresh(f.ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("a refresh after it: %v, want ErrLockLost", err)
	}
	if got := f.paths(); !slices.Equal(got, trackPaths(1)) {
		t.Errorf("documents %v: a write landed after the lease was taken over", got)
	}
}

// The embedding worker, the tenant's other writer in the process, writes through the ingester's
// Store, fenced on its lease: its writes land while the lease is the ingester's, are refused
// (ErrLockLost) once it was taken over, and before the ingester holds one (ErrNotLocked). The
// mutant, named here: Store answering the plain store (the worker writes a tenant another server
// holds).
func TestTheEmbeddingWorkersWritesAreFenced(t *testing.T) {
	f := newFixture(t)
	waiting := New(f.st, f.tenant, f.source(nil), f.business, f.leases("leases-w", ""), Options{})
	if err := waiting.Store().SetModels(f.ctx, "before", ""); !errors.Is(err, ErrNotLocked) {
		t.Errorf("a write through an ingester holding no lease: %v, want ErrNotLocked", err)
	}
	in := f.ingester(f.source(nil), Options{PingEvery: time.Hour}, "")
	if err := in.Store().SetModels(f.ctx, "m", ""); err != nil {
		t.Fatal(err)
	}
	f.takeOver()
	if err := in.Store().SetModels(f.ctx, "other", ""); !errors.Is(err, ErrLockLost) {
		t.Errorf("the worker's write after the takeover: %v, want ErrLockLost", err)
	}
	if err := in.Store().Embed(f.ctx, "m", []pgstore.Embedding{{Vec: []float32{1, 0, 0, 0}}}); !errors.Is(err, ErrLockLost) {
		t.Errorf("the worker's Embed after the takeover: %v, want ErrLockLost", err)
	}
	if active, _, err := f.st.Models(f.ctx); err != nil || active != "m" {
		t.Errorf("the tenant's model after the refused writes: %q, %v; want m", active, err)
	}
}

// Run renews its lease every PingEvery: a lease taken over stops it with ErrLockLost while it
// sleeps between passes, the renew matching no row. The mutant, named here: Run without its
// renew (it sleeps on until its next pass, an hour away).
func TestRunStopsWhenItsLeaseIsTakenOver(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "song", 1)
	in := New(f.st, f.tenant, f.source(nil), f.business, f.leases("leases", ""), Options{
		Interval: time.Hour, PingEvery: 50 * time.Millisecond, Lag: -1})
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	waitFor(t, 5*time.Second, "the first refresh", func() bool { return len(f.paths()) == 1 })
	f.takeOver()
	select {
	case err := <-done:
		cancel()
		if !errors.Is(err, ErrLockLost) {
			t.Fatalf("Run: %v, want ErrLockLost", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("Run went on after its lease was taken over")
	}
}

// Run takes the lease, refreshes a tenant never refreshed, and when its context ends returns nil
// with the lease released: another server can take it at once. A second Run on the tenant waits
// meanwhile, and never writes. The mutant, named here: Run not releasing its lease when it stops.
func TestRunReleasesItsLeaseWhenItStops(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "song", 1)
	src := f.source(nil)
	ctx, cancel := context.WithCancel(f.ctx)
	in := New(f.st, f.tenant, src, f.business, f.leases("leases", ""), Options{Interval: 50 * time.Millisecond, Lag: -1})
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	// Run's first run is a refresh: its record commits after its documents
	waitFor(t, 5*time.Second, "the first refresh", func() bool {
		r := must[pgstore.Refresh](t)(f.st.Refreshed(f.ctx))
		return !r.Finished.IsZero() && len(f.paths()) == 1
	})
	// a second server waits for the lease and never writes
	waiting := New(f.st, f.tenant, src, f.business, f.leases("leases-2", ""), Options{LockPoll: 20 * time.Millisecond})
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
	if holder, live := f.lease(); holder != "" || live {
		t.Errorf("the lease after Run stopped: holder %q, live %v; want it released", holder, live)
	}
}

// A Run stopped while its renew is in flight still releases its lease: the release runs on a
// context of its own, on the store's pool, so the stop that cancelled the renew cannot cancel it,
// and no connection the cancel broke carries it. Stopped twenty times, a renew every millisecond.
// The mutant, named here: the release on the run's own context (cancelled as it stops, the lease
// is left to expire).
func TestAStopWithARenewInFlightStillReleasesTheLease(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "song", 1)
	leases := f.leases("leases", "")
	for i := 0; i < 20; i++ {
		in := New(f.st, f.tenant, f.source(nil), f.business, leases, Options{Interval: time.Hour, PingEvery: time.Millisecond, Lag: -1})
		ctx, cancel := context.WithCancel(f.ctx)
		done := make(chan error, 1)
		go func() { done <- in.Run(ctx) }()
		waitFor(t, 5*time.Second, "the lease taken", func() bool { _, live := f.lease(); return live })
		time.Sleep(time.Duration(i%5) * time.Millisecond)
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("stop %d: Run: %v", i, err)
		}
		if holder, live := f.lease(); holder != "" || live {
			t.Fatalf("stop %d: the lease after Run returned: holder %q, live %v; want it released", i, holder, live)
		}
	}
}

// Confirm says whether the tenant is this server's now, for the embedding worker beside the
// ingester: not before the lease is taken, yes while it is held, lost once a write finds it taken
// over, and not after it is let go. Locked is called once the lease is taken; Committed once per
// committed batch, and not for a pass that read nothing. The mutants, named here: Confirm
// answering nil with no lease held (the worker would write a tenant another server holds), the
// commit not calling Committed, and Acquire not calling Locked.
func TestConfirmSaysWhetherTheTenantIsThisServers(t *testing.T) {
	f := newFixture(t)
	commits, locked := 0, 0
	in := New(f.st, f.tenant, f.source(nil), f.business, f.leases("leases", ""), Options{
		LockPoll: 20 * time.Millisecond, Now: func(context.Context) (time.Time, error) { return at(1_000_000), nil }, Lag: -1,
		Committed: func() { commits++ }, Locked: func() { locked++ }})
	t.Cleanup(in.Release)
	if err := in.Confirm(f.ctx); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("Confirm before Acquire: %v, want ErrNotLocked", err)
	}
	if err := in.Acquire(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := in.Confirm(f.ctx); err != nil || locked != 1 {
		t.Fatalf("Confirm while the lease is held: %v; Locked called %d times, want 1", err, locked)
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
	f.takeOver()
	f.track(5, "A", "song", 5)
	if _, err := in.Pass(f.ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("a pass after the takeover: %v, want ErrLockLost", err)
	}
	if err := in.Confirm(f.ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("Confirm after a write found the lease taken over: %v, want ErrLockLost", err)
	}
	in.Release()
	if err := in.Confirm(f.ctx); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("Confirm after Release: %v, want ErrNotLocked", err)
	}
}

// A Run whose lease wait passes the caller's deadline stops cleanly, nil, though the driver's
// write timed out a moment before the context said it had ended. The mutant, named here: Run
// judging the stop by ctx.Err() alone (it returns the deadline as a failure).
func TestARunWhoseWaitPassesItsDeadlineStopsCleanly(t *testing.T) {
	f := newFixture(t)
	open, cancel := context.WithCancel(f.ctx)
	t.Cleanup(cancel)
	late := lateTimer{Context: open, deadline: time.Now().Add(-time.Millisecond)}
	leases := f.leases("leases", "")
	f.scalar(leases, "SELECT 1") // an idle connection, so the claim's write times out, not its connect
	in := New(f.st, f.tenant, f.source(nil), f.business, leases, Options{LockPoll: 20 * time.Millisecond})
	if err := in.Run(late); err != nil {
		t.Errorf("a Run whose wait passed its deadline: %v, want nil", err)
	}
}

// blackHole is a TCP address that accepts connections and never answers: a database host that
// hangs, so a connection to it ends at its own connect_timeout.
func blackHole(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range held {
			_ = c.Close()
		}
		mu.Unlock()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	return ln.Addr().String()
}

// A Run whose lease connection times out on its own, under a caller's context still live, fails
// with that error: the server stops and its supervisor restarts it, rather than going on without
// the tenant's ingester. The timeout wraps context.DeadlineExceeded, the connection's own deadline,
// not the caller's. The mutant, named here: Run treating any error that wraps a context error as a
// clean stop (it returns nil, and the tenant is silently not ingested).
func TestARunWhoseLeaseConnectionTimesOutFails(t *testing.T) {
	f := newFixture(t)
	u, err := url.Parse(f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = blackHole(t)
	q := u.Query()
	q.Set("connect_timeout", "1")
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	leases, err := postgres.OpenNamed(f.ctx, "leases-hung", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { leases.Close() })
	in := New(f.st, f.tenant, f.source(nil), f.business, leases, Options{})
	done := make(chan error, 1)
	go func() { done <- in.Run(f.ctx) }()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || f.ctx.Err() != nil {
			t.Errorf("a Run whose lease connection timed out under a live context: %v, want the connection's timeout", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run went on waiting for a connection that timed out")
	}
}
