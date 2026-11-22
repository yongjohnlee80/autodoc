//go:build server

package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A tenant's writer lease is renewed on a pool of its own: with every connection of the store's
// pool held for four leases' lengths (its two waiting on a table lock, the ingester's write on one
// and a search on the other), the lease is renewed meanwhile and the server goes on, and once the
// lock is let go the row that waited is written and the search answered. The mutant, named here:
// the ingesters given the store's pool for their leases (the renew waits for a held connection,
// the lease is lost, and the server exits 1).
//
// The lease is 3 s: a renew every second, each bounded by half a second, and silent for 2.5 s the
// lease is lost. At 1.5 s a renew had a quarter second, which two renews in a row overran under
// -race on a loaded runner: the lease was lost and given up, and the server exited.
func TestTheLeaseRenewsWhileEveryStoreConnectionIsHeld(t *testing.T) {
	ttl := leaseTTL
	leaseTTL = 3 * time.Second
	t.Cleanup(func() { leaseTTL = ttl })
	quickBuilder(t)
	f := newServerFixture(t, serverView)
	f.storePool(minIndexConns) // the fewest the server takes, both of which the test then holds
	t.Setenv("AUTODOC_TEST_HTTP_TOKEN", httpToken)
	f.extra = httpBlock
	f.configure("fake-model", f.url)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done, stderr := f.serve(ctx)
	f.waitServing(done, stderr, "the server serving", func() bool { return strings.Contains(stderr.String(), "serving enterprise search") })
	f.waitServing(done, stderr, "both rows written", func() bool { return f.documents() == "2" })
	m := listening.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("the server does not say where it listens: %s", stderr)
	}

	// leaseUntil is the lease's lease_until while a server holds it. A lease with no holder was given
	// up: the server lost it, its ingester said it is stopping, and the server exits once its
	// requests in flight end (shutdownGrace; the search this cell holds on the lock keeps it that
	// long). That is reported with what the server said. A lease with no holder, the ingester not
	// stopping and the server not exiting past that grace, would be the fault itself.
	leaseUntil := func(when string) string {
		t.Helper()
		if v := f.scalar(`SELECT coalesce((SELECT lease_until::text FROM ` + f.schema + `.rag_writer_lease WHERE holder <> ''), '')`); v != "" {
			return v
		}
		grace := time.After(shutdownGrace + 5*time.Second)
		for {
			if strings.Contains(stderr.String(), "tenant music: stopping") {
				t.Fatalf("%s: the lease has no holder: the server lost it and is stopping: %s", when, stderr)
			}
			select {
			case code := <-done:
				t.Fatalf("%s: the lease has no holder: the server lost it and exited %d: %s", when, code, stderr)
			case <-grace:
				t.Fatalf("%s: the lease has no holder, and the server goes on: %s", when, stderr)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	release, searched := f.holdStore(done, stderr, m[1])
	before := leaseUntil("every store connection held")
	hold := time.NewTimer(4 * leaseTTL)
	select {
	case code := <-done:
		t.Fatalf("the server exited %d while every store connection was held: %s", code, stderr)
	case <-hold.C:
	}
	after := leaseUntil(fmt.Sprintf("after %v held", 4*leaseTTL))
	if after == before {
		t.Errorf("the lease was not renewed in %v with every store connection held (lease_until %s)", 4*leaseTTL, after)
	}
	release()
	f.waitServing(done, stderr, "the row that waited written", func() bool { return f.documents() == "3" })
	if code := <-searched; code != http.StatusOK {
		t.Errorf("the search that waited on the lock answered %d, want 200", code)
	}
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Errorf("--server exited %d after its lease was held throughout: %s", code, stderr)
	}
}
