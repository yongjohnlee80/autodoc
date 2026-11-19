//go:build server

package app

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// A tenant's writer lease is renewed on a pool of its own: with every connection of the store's
// pool held, here its one connection waiting on a table lock for four leases' lengths, the lease
// is renewed meanwhile and the server goes on, and once the lock is let go the row that waited is
// written. The mutant, named here: the ingesters given the store's pool for their leases (the
// renew waits for the held connection, the lease is lost, and the server exits 1).
func TestTheLeaseRenewsWhileEveryStoreConnectionIsHeld(t *testing.T) {
	ttl := leaseTTL
	leaseTTL = 1500 * time.Millisecond // a renew every 500 ms; silent for most of 1.5 s, it is lost
	t.Cleanup(func() { leaseTTL = ttl })
	f := newServerFixture(t, serverView)
	u, err := url.Parse(os.Getenv("AUTODOC_TEST_SERVER_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("pool_max_conns", "1") // the store's pool is one connection, which the test then holds
	u.RawQuery = q.Encode()
	t.Setenv("AUTODOC_TEST_SERVER_DSN", u.String())
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done, stderr := f.serve(ctx)
	f.waitServing(done, stderr, "the server serving", func() bool { return strings.Contains(stderr.String(), "serving enterprise search") })
	f.waitServing(done, stderr, "both rows written", func() bool { return f.documents() == "2" })

	// the store's one connection waits on a lock the test holds: the next pass writes a new row
	// into rag_document, which the lock keeps it from, its connection held while it waits
	tx, err := f.plain.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(f.ctx, `LOCK TABLE `+f.schema+`.rag_document IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO track VALUES (3, 'A', 'wren song', now())`)
	f.waitServing(done, stderr, "a store connection waiting on the lock", func() bool {
		return f.scalar(`SELECT count(*)::text FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query ILIKE '%rag_document%'`) != "0"
	})
	before := f.scalar(`SELECT lease_until::text FROM ` + f.schema + `.rag_writer_lease WHERE holder <> ''`)
	hold := time.NewTimer(4 * leaseTTL)
	select {
	case code := <-done:
		t.Fatalf("the server exited %d while its store's connection was held: %s", code, stderr)
	case <-hold.C:
	}
	after := f.scalar(`SELECT lease_until::text FROM ` + f.schema + `.rag_writer_lease WHERE holder <> ''`)
	if after == before {
		t.Errorf("the lease was not renewed in %v with the store's connection held (lease_until %s)", 4*leaseTTL, after)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	f.waitServing(done, stderr, "the row that waited written", func() bool { return f.documents() == "3" })
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Errorf("--server exited %d after its lease was held throughout: %s", code, stderr)
	}
}
