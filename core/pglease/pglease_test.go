package pglease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// the lease table as pgstore's 000007 makes it (pgstore's own cells hold that script to it)
const table = "test_lease"

const ddl = `CREATE TABLE test_lease (
    scope       text COLLATE "C" PRIMARY KEY,
    holder      text NOT NULL,
    epoch       bigint NOT NULL,
    lease_until timestamptz NOT NULL,
    claimed_at  timestamptz,
    renewed_at  timestamptz,
    holder_info text
)`

// ttl is the cells' lease: long enough that a renew (bounded by a sixth of it) answers under
// -race on two CPUs, short enough that a cell outlasting it twice is a few seconds.
const ttl = 1800 * time.Millisecond

func scratch(t *testing.T) dao.DataConn {
	t.Helper()
	return pgtest.Scratch(t, func(ctx context.Context, c dao.DataConn) error {
		_, err := c.ExecContext(ctx, ddl)
		return err
	})
}

func opts(scope string) Options { return Options{Table: table, Scope: scope, TTL: ttl, Info: "cell"} }

func claim(t *testing.T, conn dao.DataConn, scope string) *Lease {
	t.Helper()
	l, err := Claim(context.Background(), conn, opts(scope))
	if err != nil {
		t.Fatalf("claim %s: %v", scope, err)
	}
	return l
}

// row is the lease row's holder and epoch, and whether it is live by the database's clock.
func row(t *testing.T, conn dao.DataConn, scope string) (holder string, epoch int64, live bool) {
	t.Helper()
	if err := scanOne(context.Background(), conn, `SELECT holder, epoch, lease_until > clock_timestamp() FROM `+table+` WHERE scope = $1`,
		[]any{scope}, &holder, &epoch, &live); err != nil {
		t.Fatal(err)
	}
	return holder, epoch, live
}

// openWrite is a write transaction fenced on l: its first statement l's fence, then a marker row
// written, open until the returned commit.
func openWrite(t *testing.T, conn dao.DataConn, l *Lease, marker string) (commit func() error) {
	t.Helper()
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS marker (name text PRIMARY KEY, at timestamptz DEFAULT clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	tx, err := dao.BeginConnTx(ctx, conn, dao.TxOptions{Isolation: dao.TxReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	// ended whatever the cell does: a cell that fails with the write open would leave its connection
	// held, and the pool's close, at the scratch database's cleanup, waiting on it for ever
	t.Cleanup(func() { _ = tx.Rollback() })
	if err := l.Fence(ctx, tx); err != nil {
		t.Fatalf("the fence of an open write: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO marker (name) VALUES ($1)`, marker); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	return tx.Commit
}

// held is whether err is a claim refused because the lease is another's.
func held(err error) bool { return errors.Is(err, ErrHeld) }

// A lease taken over once it expired: the standby is refused while it is live, naming the
// holder; once its TTL passes unrenewed the standby takes it at the next epoch, and the old
// holder's renew and fence then answer ErrLost. The mutant, named here: the claim without its
// liveness check (the standby takes a live lease).
func TestALeaseIsTakenOverOnlyOnceItExpired(t *testing.T) {
	conn := scratch(t)
	a := claim(t, conn, "t")
	_, err := Claim(context.Background(), conn, opts("t"))
	var he *HeldError
	if !errors.As(err, &he) || he.Holder.ID != a.ID() || !strings.Contains(he.Holder.Info, "cell") {
		t.Fatalf("a claim of a live lease: %v, want it held by %s", err, a.ID())
	}
	time.Sleep(ttl + 200*time.Millisecond)
	b := claim(t, conn, "t")
	if b.Epoch() != a.Epoch()+1 {
		t.Errorf("the takeover's epoch %d, want %d", b.Epoch(), a.Epoch()+1)
	}
	if err := a.Renew(context.Background()); !errors.Is(err, ErrLost) {
		t.Errorf("the old holder's renew after the takeover: %v, want ErrLost", err)
	}
	if h, _, _ := row(t, conn, "t"); h != b.ID() {
		t.Errorf("the row's holder %s after the old holder's renew, want %s", h, b.ID())
	}
}

// A fenced write longer than the lease, renewing meanwhile: every renew lands at once (the
// fence's FOR KEY SHARE does not hold up the holder's UPDATE), the holder keeps the lease
// throughout and after, and a standby's claims are refused throughout and after. The mutant,
// named here: the fence FOR SHARE (the renews wait for the write, the holder goes silent and
// counts its lease lost).
func TestALongFencedWriteKeepsItsLease(t *testing.T) {
	conn := scratch(t)
	a := claim(t, conn, "t")
	commit := openWrite(t, conn, a, "long")
	end := time.Now().Add(2 * ttl)
	for time.Now().Before(end) {
		start := time.Now()
		if err := a.Renew(context.Background()); err != nil {
			t.Fatalf("a renew during the holder's own open write: %v (after %s)", err, time.Since(start))
		}
		ctx, cancel := context.WithTimeout(context.Background(), ttl)
		_, err := Claim(ctx, conn, opts("t"))
		cancel()
		if !held(err) {
			t.Fatalf("a standby's claim during the write: %v, want it held", err)
		}
		time.Sleep(ttl / 3)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	if err := a.Check(); err != nil {
		t.Errorf("the holder after its write: %v, want the lease still its own", err)
	}
	if _, err := Claim(context.Background(), conn, opts("t")); !held(err) {
		t.Errorf("a standby's claim after the write: %v, want it held", err)
	}
}

// A fenced write longer than the lease, not renewing: past its TTL a standby still cannot claim
// while the write is open (the claim's FOR UPDATE meets the fence), and is refused at once, never
// queued; it claims as the write ends, and the write committed before it did. The mutants, named
// here: the claim as an INSERT … ON CONFLICT DO UPDATE without FOR UPDATE (it takes the lease
// over mid-write), and the claim without NOWAIT (it waits on the row for the write's length).
func TestATakeoverWaitsForAnOpenFencedWrite(t *testing.T) {
	conn := scratch(t)
	a := claim(t, conn, "t")
	commit := openWrite(t, conn, a, "before the takeover")
	time.Sleep(ttl + 200*time.Millisecond) // expired by the database's clock, the write still open
	ctx, cancel := context.WithTimeout(context.Background(), ttl/2)
	start := time.Now()
	_, err := Claim(ctx, conn, opts("t"))
	cancel()
	var he *HeldError
	if !errors.As(err, &he) || !he.Busy {
		t.Fatalf("a claim while the expired holder's write is open: %v after %s, want it refused as busy", err, time.Since(start))
	}
	if waited := time.Since(start); waited > ttl/4 {
		t.Errorf("the refused claim took %s: it waited on the row", waited)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	b := claim(t, conn, "t")
	var committed, claimed time.Time
	if err := scanOne(context.Background(), conn, `SELECT (SELECT at FROM marker WHERE name = 'before the takeover'),
		(SELECT claimed_at FROM `+table+` WHERE scope = 't')`, nil, &committed, &claimed); err != nil {
		t.Fatalf("the write before the takeover: %v", err)
	}
	if !committed.Before(claimed) {
		t.Errorf("the write's row at %s, the takeover at %s", committed, claimed)
	}
	if b.Epoch() != a.Epoch()+1 {
		t.Errorf("the takeover's epoch %d, want %d", b.Epoch(), a.Epoch()+1)
	}
}

// The fence reads the database's clock now, not its transaction's start: a lease that expires
// after a write transaction began fails that transaction's fence. The mutant, named here: the
// fence on now() (the transaction's start is before the expiry, so it passes).
func TestTheFenceReadsTheClockNotTheTransactionsStart(t *testing.T) {
	conn := scratch(t)
	a := claim(t, conn, "t")
	ctx := context.Background()
	tx, err := dao.BeginConnTx(ctx, conn, dao.TxOptions{Isolation: dao.TxReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1`); err != nil { // the transaction under way
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	// expired by the database's clock, after the transaction began; the holder's own clock still
	// says it renewed lately, so only the database can tell
	if _, err := conn.ExecContext(ctx, `UPDATE `+table+` SET lease_until = clock_timestamp() - interval '10 ms' WHERE scope = 't'`); err != nil {
		t.Fatal(err)
	}
	if err := a.Fence(ctx, tx); !errors.Is(err, ErrLost) {
		t.Errorf("the fence of a lease expired after its transaction began: %v, want ErrLost", err)
	}
}

// A release lets a standby claim at once, at the next epoch, and leaves nothing a renew of the
// old holder's can revive: one sent after the release, with its holder and epoch, matches no row.
// The mutants, named here: the renew without its holder and epoch (it renews the standby's
// lease), and the release only expiring the lease (the old holder's renew revives it).
func TestAReleaseLetsAStandbyClaimAndNothingRevivesIt(t *testing.T) {
	conn := scratch(t)
	a := claim(t, conn, "t")
	epoch := a.Epoch()
	if err := a.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h, _, live := row(t, conn, "t"); h != "" || live {
		t.Errorf("the row after the release: holder %q, live %v; want released", h, live)
	}
	// a renew the old holder had in flight as it released: its own statement, its holder and epoch
	res, err := conn.ExecContext(context.Background(), a.sql.renew, "t", a.ID(), epoch, ttl.Seconds())
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Errorf("the old holder's renew after its release matched %d rows, want none", n)
	}
	b := claim(t, conn, "t")
	if b.Epoch() <= epoch {
		t.Errorf("the standby's epoch %d, want past %d", b.Epoch(), epoch)
	}
	if err := a.Renew(context.Background()); !errors.Is(err, ErrLost) {
		t.Errorf("the released holder's renew: %v, want ErrLost", err)
	}
	res, err = conn.ExecContext(context.Background(), a.sql.renew, "t", a.ID(), epoch, ttl.Seconds())
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Errorf("the old holder's renew after the standby's claim matched %d rows, want none", n)
	}
	if h, _, live := row(t, conn, "t"); h != b.ID() || !live {
		t.Errorf("the standby's lease: holder %s, live %v", h, live)
	}
}

// A holder that goes a TTL less a sixth without a good renew counts its lease lost by its own
// clock, whatever the database says, and stays so: its fence fails before any I/O. The mutant,
// named here: Check without the silence rule (a holder cut off from the database writes on while
// a standby takes over).
func TestASilentHolderCountsItsLeaseLost(t *testing.T) {
	conn := scratch(t)
	a := claim(t, conn, "t")
	now := time.Now()
	a.now = func() time.Time { return now.Add(ttl - ttl/6 + time.Millisecond) }
	if err := a.Check(); !errors.Is(err, ErrLost) {
		t.Fatalf("a holder silent for most of its TTL: %v, want ErrLost", err)
	}
	a.now = time.Now
	if err := a.Check(); !errors.Is(err, ErrLost) {
		t.Errorf("the lease once counted lost, after: %v, want it lost still", err)
	}
	if err := a.Fence(context.Background(), conn); !errors.Is(err, ErrLost) {
		t.Errorf("its fence: %v, want ErrLost", err)
	}
}

// Acquire waits while the lease is another's, telling who holds it once, and takes it within a
// poll of its release.
func TestAcquireWaitsAndTakesTheLeaseOnRelease(t *testing.T) {
	conn := scratch(t)
	a := claim(t, conn, "t")
	told := make(chan Holder, 4)
	got := make(chan *Lease, 1)
	go func() {
		b, err := Acquire(context.Background(), conn, opts("t"), 20*time.Millisecond, func(h Holder) { told <- h })
		if err != nil {
			t.Error(err)
		}
		got <- b
	}()
	select {
	case h := <-told:
		if h.ID != a.ID() {
			t.Errorf("waiting told holder %s, want %s", h.ID, a.ID())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire never said it waits")
	}
	time.Sleep(100 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("Acquire took a live lease")
	default:
	}
	released := time.Now()
	if err := a.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if waited := time.Since(released); waited > time.Second {
			t.Errorf("Acquire took the released lease %s after the release", waited)
		}
		if b != nil && b.Epoch() <= a.Epoch() {
			t.Errorf("the new epoch %d, want past %d", b.Epoch(), a.Epoch())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not take the released lease")
	}
	if len(told) != 0 {
		t.Errorf("waiting was told %d more times, want once", len(told))
	}
}

// Two scopes are two leases: one held does not hold the other.
func TestScopesAreSeparateLeases(t *testing.T) {
	conn := scratch(t)
	claim(t, conn, "tenant:a")
	claim(t, conn, "tenant:b")
}

// Options a lease cannot have are refused before any I/O.
func TestOptionsAreChecked(t *testing.T) {
	for _, o := range []Options{{Table: "x; DROP TABLE y", Scope: "s"}, {Table: "t"}, {Table: "t", Scope: "s", TTL: -time.Second}} {
		if _, err := Claim(context.Background(), nil, o); err == nil {
			t.Errorf("Claim with %+v: no error", o)
		}
	}
}

// manualTakeover is the statement docs/enterprise/operations.md gives an operator for taking a
// tenant over by hand, read from the doc itself and pointed at the cell's table and scope.
func manualTakeover(t *testing.T, scope string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "enterprise", "operations.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	i := strings.Index(doc, "**Taking a tenant over by hand,**")
	if i < 0 {
		t.Fatal("operations.md has no manual takeover")
	}
	block := doc[i:]
	start := strings.Index(block, "```sql")
	end := strings.Index(block[start+len("```sql"):], "```")
	if start < 0 || end < 0 {
		t.Fatal("operations.md's manual takeover has no sql block")
	}
	sql := strings.Join(strings.Fields(block[start+len("```sql"):start+len("```sql")+end]), " ")
	if !strings.Contains(sql, "<schema>.rag_writer_lease") || !strings.Contains(sql, "'tenant:<name>'") {
		t.Fatalf("the manual takeover no longer names <schema>.rag_writer_lease and 'tenant:<name>': %s", sql)
	}
	sql = strings.ReplaceAll(sql, "<schema>.rag_writer_lease", table)
	return strings.ReplaceAll(sql, "'tenant:<name>'", "'"+scope+"'")
}

// The operations doc's manual takeover, run as it is written, stops a live holder: a write it has
// open finishes (the statement doesn't wait for it), its next renew and its next fence answer
// ErrLost, and a standby claims at its next poll. The mutant, named here: a takeover that only
// expires the lease (the holder's renew revives it, and its fences pass again).
func TestTheDocumentedManualTakeoverStopsALiveHolder(t *testing.T) {
	conn := scratch(t)
	a := claim(t, conn, "t")
	commit := openWrite(t, conn, a, "open at the takeover")
	ctx, cancel := context.WithTimeout(context.Background(), ttl)
	_, err := conn.ExecContext(ctx, manualTakeover(t, "t"))
	cancel()
	if err != nil {
		t.Fatalf("the documented takeover, with a write open: %v", err)
	}
	if err := commit(); err != nil {
		t.Errorf("the write open at the takeover: %v, want it to finish", err)
	}
	if err := a.Renew(context.Background()); !errors.Is(err, ErrLost) {
		t.Errorf("the holder's renew after the takeover: %v, want ErrLost", err)
	}
	tx, err := dao.BeginConnTx(context.Background(), conn, dao.TxOptions{Isolation: dao.TxReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if err := a.Fence(context.Background(), tx); !errors.Is(err, ErrLost) {
		t.Errorf("the holder's next write's fence: %v, want ErrLost", err)
	}
	b := claim(t, conn, "t")
	if b.Epoch() <= a.Epoch() {
		t.Errorf("the standby's epoch %d, want past %d", b.Epoch(), a.Epoch())
	}
}
