//go:build server

package app

import (
	"context"
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
func TestTheLeaseRenewsWhileEveryStoreConnectionIsHeld(t *testing.T) {
	ttl := leaseTTL
	leaseTTL = 1500 * time.Millisecond // a renew every 500 ms; silent for most of 1.5 s, it is lost
	t.Cleanup(func() { leaseTTL = ttl })
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

	release, searched := f.holdStore(done, stderr, m[1])
	before := f.scalar(`SELECT lease_until::text FROM ` + f.schema + `.rag_writer_lease WHERE holder <> ''`)
	hold := time.NewTimer(4 * leaseTTL)
	select {
	case code := <-done:
		t.Fatalf("the server exited %d while every store connection was held: %s", code, stderr)
	case <-hold.C:
	}
	after := f.scalar(`SELECT lease_until::text FROM ` + f.schema + `.rag_writer_lease WHERE holder <> ''`)
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
