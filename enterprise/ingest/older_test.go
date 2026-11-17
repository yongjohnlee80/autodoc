package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// v0315Key is the key a v0.3.15 server held schema's tenant by: that release's pgstore.WriterKey,
// written out here so the check's own copy is held to it.
func v0315Key(schema, tenant string) int64 {
	h := sha256.Sum256([]byte("autodoc-enterprise\x00" + schema + "\x00" + tenant))
	return int64(binary.BigEndian.Uint64(h[:8]) &^ (1 << 63))
}

// holdOlder holds the tenant's v0.3.15 writer lock on a connection of its own, as a v0.3.15 server
// holds it for its life; the func it returns lets it go, once however often it is called, and the
// test's cleanup calls it too, so a failing test never leaves the pool a connection short.
func (f *fixture) holdOlder() func() {
	f.t.Helper()
	key := v0315Key(f.schema, f.tenant)
	tx, err := f.store.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := tx.ExecContext(f.ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		_ = tx.Rollback()
		f.t.Fatal(err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_, _ = tx.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
			_ = tx.Rollback()
		})
	}
	f.t.Cleanup(release)
	return release
}

// advisoryLocks is how many advisory locks the fixture's database holds, every session's.
func (f *fixture) advisoryLocks() string {
	f.t.Helper()
	return f.scalar(f.store, `SELECT count(*)::text FROM pg_locks WHERE locktype = 'advisory'
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`)
}

// While a v0.3.15 server holds the tenant by its advisory lock, Acquire waits without claiming the
// lease, and the state says an older server is why; once that lock goes, it takes the lease. The
// check keeps no lock of its own. The mutants, named here: no check before the claim (the lease is
// taken beside the older server); the check by a session lock (it is kept after).
func TestAnOlderServersLockHoldsTheTenantUntilItGoes(t *testing.T) {
	f := newFixture(t)
	release := f.holdOlder()
	in := New(f.st, f.tenant, f.source(nil), f.business, f.leases("leases", ""), Options{LockPoll: 20 * time.Millisecond, OlderSchema: f.schema})
	t.Cleanup(in.Release)
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- in.Acquire(ctx) }()
	t.Cleanup(func() { cancel(); <-done }) // first of the cleanups: Acquire ends before its pools close
	waitFor(t, 5*time.Second, "the state saying an older server holds the tenant", func() bool {
		st := in.State()
		return st.Waiting && st.OlderServer
	})
	time.Sleep(100 * time.Millisecond) // several polls
	if holder, _ := f.lease(); holder != "" {
		t.Fatalf("the lease was taken (%s) while an older server holds the tenant", holder)
	}
	release()
	select {
	case err := <-done:
		done <- err // for the cleanup's wait
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire still waits 5 s after the older server let go")
	}
	if holder, live := f.lease(); holder == "" || !live {
		t.Errorf("the lease after the older server let go: %q, live %v; want it taken", holder, live)
	}
	if st := in.State(); st.Waiting || st.OlderServer {
		t.Errorf("the state after the lease was taken: %+v", st)
	}
	if n := f.advisoryLocks(); n != "0" {
		t.Errorf("%s advisory locks held after the checks; want none kept", n)
	}
}

// An older server that takes the tenant's lock while this one holds the lease (a v0.3.15 standby,
// running since before the upgrade) stops this one's run at the next renew, with ErrLockLost
// saying why. The mutant, named here: no check at the renew (the run goes on writing).
func TestAnOlderServerTakingTheTenantStopsTheRun(t *testing.T) {
	f := newFixture(t)
	in := f.ingester(f.source(nil), Options{PingEvery: 30 * time.Millisecond, LockPoll: 20 * time.Millisecond, OlderSchema: f.schema}, "")
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done }) // first of the cleanups: Run ends before its pools close
	time.Sleep(150 * time.Millisecond)     // several renews with no older server
	select {
	case err := <-done:
		done <- err
		t.Fatalf("the run stopped before any older server: %v", err)
	default:
	}
	f.holdOlder()
	select {
	case err := <-done:
		done <- err
		if !errors.Is(err, ErrLockLost) || !strings.Contains(err.Error(), "older server") {
			t.Errorf("the run stopped with %v; want ErrLockLost naming the older server", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run still goes 5 s after an older server took the tenant's lock")
	}
}
