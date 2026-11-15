//go:build server

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/core/pglease"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// pgLocation is a fresh PostgreSQL store's location: a scratch database, a schema in it, and a key
// file of the test's own.
func pgLocation(t *testing.T) Location {
	t.Helper()
	return Location{Engine: EngineOfPostgres, DSN: pgtest.ScratchURL(t), Schema: "autodoc_store",
		KeyFile: filepath.Join(t.TempDir(), "store.key")}
}

// A store on PostgreSQL opens, applies its scripts, writes and reads, and opens again with what it
// wrote: its workspaces, and a sealed connection's DSN, which its key file opens.
func TestAStoreOnPostgresKeepsWhatItWrites(t *testing.T) {
	ctx := context.Background()
	loc := pgLocation(t)
	s, err := OpenAt(ctx, loc)
	if err != nil {
		t.Fatal(err)
	}
	if st := s.Schema(); len(st.Pending) == 0 || st.Pending[0] != "000001_update_initialize_tables.sql" {
		t.Errorf("a new store's schema update = %+v, want the baseline applied", st)
	}
	w, err := s.AddWorkspace(ctx, "kb", "/kb", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openPG(t, loc)
	if st := s.Schema(); len(st.Pending) != 0 {
		t.Errorf("a second open's schema update = %+v, want nothing pending", st)
	}
	ws, err := s.Workspaces(ctx)
	if err != nil || len(ws) != 1 || ws[0].ID != w.ID || ws[0].Name != "kb" {
		t.Errorf("the workspaces after a reopen: %+v, %v; want kb", ws, err)
	}
	if got := s.Location(); got.Engine != EngineOfPostgres || got.Schema != "autodoc_store" {
		t.Errorf("Location() = %+v", got)
	}
}

// One process serves a store on PostgreSQL, as on SQLite: a second open is ErrBusy, naming the
// holder, until the first closes, which releases the lease so the next open takes it at once. The
// mutant, named here: Open not claiming the lease (two processes write one store).
func TestASecondOpenOfAStoreOnPostgresIsBusy(t *testing.T) {
	ctx := context.Background()
	loc := pgLocation(t)
	s, err := OpenAt(ctx, loc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = OpenAt(ctx, loc)
	if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "store=autodoc_store") {
		t.Errorf("a second open of a served store = %v, want ErrBusy naming its holder", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	openPG(t, loc)
}

// Every write of a store on PostgreSQL confirms its lease first: once the lease is taken over (the
// operations doc's statement, pointed at the store's row), a write fails with the lease lost and
// writes nothing, while reads go on. The mutant, named here: the store's writer unfenced (it
// writes on after another process took the store).
func TestAStoreOnPostgresWritesOnlyWhileItsLeaseIsItsOwn(t *testing.T) {
	ctx := context.Background()
	s := openPG(t, pgLocation(t))
	if _, err := s.AddWorkspace(ctx, "before", "/before", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.leaseConn().ExecContext(ctx, `UPDATE `+LeaseTable+` SET holder = '', epoch = epoch + 1,
		lease_until = clock_timestamp() WHERE scope = '`+LeaseScope+`'`); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddWorkspace(ctx, "after", "/after", nil, nil)
	if !errors.Is(err, pglease.ErrLost) {
		t.Fatalf("a write after the takeover: %v, want the lease lost", err)
	}
	ws, err := s.Workspaces(ctx)
	if err != nil || len(ws) != 1 || ws[0].Name != "before" {
		t.Errorf("the workspaces after the refused write: %+v, %v; want before alone", ws, err)
	}
}

// What only a SQLite file has is refused on PostgreSQL, ErrNotSQLite, and the damaged mark is never
// there.
func TestWhatOnlyASQLiteFileHasIsRefusedOnPostgres(t *testing.T) {
	ctx := context.Background()
	s := openPG(t, pgLocation(t))
	for name, err := range map[string]error{
		"QuickCheck":  func() error { _, err := s.QuickCheck(ctx); return err }(),
		"Check":       func() error { _, err := s.Check(ctx); return err }(),
		"Compact":     func() error { _, err := s.Compact(ctx); return err }(),
		"Checkpoint":  s.Checkpoint(ctx),
		"AutoVacuum":  func() error { _, err := s.AutoVacuum(ctx); return err }(),
		"MarkDamaged": s.MarkDamaged(CheckReport{}),
	} {
		if !errors.Is(err, ErrNotSQLite) {
			t.Errorf("%s on PostgreSQL: %v, want ErrNotSQLite", name, err)
		}
	}
	if s.Marked() || s.FileSize() != 0 || s.ClearDamaged() != nil {
		t.Error("a store on PostgreSQL has a file's mark or size")
	}
}

// A location a store cannot have is refused before any I/O.
func TestALocationIsCheckedBeforeAnyIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	key := filepath.Join(t.TempDir(), "k")
	for _, loc := range []Location{
		{Engine: "oracle"},
		{Engine: EngineOfPostgres, Schema: "s", KeyFile: key},
		{Engine: EngineOfPostgres, DSN: "postgres://x/y", Schema: "Not-Plain", KeyFile: key},
		{Engine: EngineOfPostgres, DSN: "postgres://x/y", Schema: "pg_catalog", KeyFile: key},
		{Engine: EngineOfPostgres, DSN: "postgres://x/y", Schema: "s"},
	} {
		if s, err := OpenAt(ctx, loc); err == nil {
			_ = s.Close()
			t.Errorf("OpenAt(%+v): no error", loc)
		}
	}
	if _, err := os.Stat(key); err == nil {
		t.Error("a refused location made its key file")
	}
}

// A read transaction of a store on PostgreSQL sees one commit, as a SQLite one does: a write that
// commits between two of its reads is not in the second. The mutant, named here: the readers'
// transactions at PostgreSQL's default, READ COMMITTED (the second read sees the write).
func TestAReadOfAStoreOnPostgresSeesOneCommit(t *testing.T) {
	ctx := context.Background()
	s := openPG(t, pgLocation(t))
	tx, err := s.r.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	count := func() int64 {
		t.Helper()
		rows, err := tx.QueryContext(ctx, `SELECT count(*) FROM workspace`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var n int64
		if rows.Next() {
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
		}
		return n
	}
	before := count()
	if _, err := s.AddWorkspace(ctx, "meanwhile", "/meanwhile", nil, nil); err != nil {
		t.Fatal(err)
	}
	if after := count(); after != before {
		t.Errorf("one read transaction saw %d workspaces, then %d: a commit between its reads", before, after)
	}
}

// A process that finds a store on PostgreSQL another's leaves it as it found it: the lease is
// claimed before the scripts, so a fresh database whose lease another holds is refused with no
// script applied (no ledger). The mutant, named here: the scripts applied before the claim (a newer
// build beside an older daemon would migrate the store under it).
func TestAStoreOnPostgresHeldByAnotherIsLeftAsItWas(t *testing.T) {
	ctx := context.Background()
	loc := pgLocation(t)
	admin, err := postgres.Open(ctx, loc.DSN, postgres.MaxOpenConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, q := range []string{`CREATE SCHEMA ` + loc.Schema, `SET search_path TO ` + loc.Schema, leaseTableDDL,
		`INSERT INTO ` + LeaseTable + ` VALUES ('` + LeaseScope + `', 'another', 1, clock_timestamp() + interval '1 hour', NULL, NULL, 'a newer one')`} {
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := OpenAt(ctx, loc); !errors.Is(err, ErrBusy) {
		t.Fatalf("an open of a store another holds: %v, want ErrBusy", err)
	}
	rows, err := admin.QueryContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name <> $2`, loc.Schema, LeaseTable)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int64
	if rows.Next() {
		_ = rows.Scan(&n)
	}
	if n != 0 {
		t.Errorf("%d tables were made in a store another holds, want none (no script applied)", n)
	}
}

// The lease's table made before the claim is 000036's own: the two statements are the same text.
func TestTheLeaseTableMadeBeforeTheClaimIs000036s(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "sql", "deployments", "postgres", "000036_update_writer_lease.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var stmt []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			stmt = append(stmt, line)
		}
	}
	norm := func(s string) string {
		return strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(s), ";")), " ")
	}
	if got, want := norm(leaseTableDDL), norm(strings.Join(stmt, "\n")); got != want {
		t.Errorf("leaseTableDDL:\n%s\n000036:\n%s", got, want)
	}
}

// Two stores in one database, one in public and one in a schema of its own, are apart: the second
// is made whole in its schema (as many tables as the first) and reads none of the first's rows; the
// first keeps its rows. (PostgreSQL creates an unqualified table in the
// first schema of the path, the store's own, so public beside it on the path changes none of this.)
func TestTwoStoresInOneDatabaseAreApart(t *testing.T) {
	ctx := context.Background()
	first := pgLocation(t)
	first.Schema = "public"
	a, err := OpenAt(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddWorkspace(ctx, "first", "/first", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Schema = "second_store"
	second.KeyFile = filepath.Join(t.TempDir(), "second.key")
	b := openPG(t, second)
	if ws, err := b.Workspaces(ctx); err != nil || len(ws) != 0 {
		t.Errorf("the second store's workspaces: %+v, %v; want none of the first's", ws, err)
	}
	tables := func(schema string) int64 {
		rows, err := b.leaseConn().QueryContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_type = 'BASE TABLE'`, schema)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var n int64
		if rows.Next() {
			_ = rows.Scan(&n)
		}
		return n
	}
	if p, s := tables("public"), tables("second_store"); s != p || s < 30 {
		t.Errorf("tables: %d in public, %d in second_store; want the second made whole in its own schema", p, s)
	}
	if _, err := b.AddWorkspace(ctx, "second", "/second", nil, nil); err != nil {
		t.Fatal(err)
	}
	a = openPG(t, first)
	if ws, err := a.Workspaces(ctx); err != nil || len(ws) != 1 || ws[0].Name != "first" {
		t.Errorf("the first store's workspaces after the second's write: %+v, %v; want first alone", ws, err)
	}
}

// shortLease makes the stores a cell opens lease for d, polled every d/4, until the cell ends; set before any store
// opens, so no renew reads it mid-change.
func shortLease(t *testing.T, d time.Duration) {
	t.Helper()
	ttl, poll := leaseTTL, busyPoll
	leaseTTL, busyPoll = d, d/4
	t.Cleanup(func() { leaseTTL, busyPoll = ttl, poll })
}

// A first start longer than the lease (its scripts building an index on a large store, say) keeps
// the lease: it is renewed from the claim on, so a second process opening the store meanwhile is
// refused, and the store writes once open. The mutant, named here: the renew started only after the
// scripts (the lease expires under them, and the second process takes the store).
func TestALongFirstStartKeepsItsLease(t *testing.T) {
	shortLease(t, 2*time.Second)
	ctx := context.Background()
	loc := pgLocation(t)
	standby := make(chan error, 1)
	beforeScripts = func() {
		beforeScripts = nil
		time.Sleep(3 * time.Second) // past the lease's length
		s, err := OpenAt(ctx, loc)
		if err == nil {
			_ = s.Close()
		}
		standby <- err
	}
	t.Cleanup(func() { beforeScripts = nil })
	s := openPG(t, loc)
	if err := <-standby; !errors.Is(err, ErrBusy) {
		t.Errorf("a second open during the first start's scripts: %v, want ErrBusy", err)
	}
	if _, err := s.AddWorkspace(ctx, "after", "/after", nil, nil); err != nil {
		t.Errorf("a write once the long first start opened: %v", err)
	}
}

// The first start's scripts are fenced as every write is: a lease taken over between the claim and
// the scripts fails them, the open with them, and nothing of the store's schema is made. The mutant,
// named here: the scripts run on the bare writer (they migrate a store another process now holds).
func TestTheFirstStartsScriptsAreFenced(t *testing.T) {
	ctx := context.Background()
	loc := pgLocation(t)
	admin, err := postgres.Open(ctx, loc.DSN, postgres.MaxOpenConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	beforeScripts = func() {
		beforeScripts = nil
		if _, err := admin.ExecContext(ctx, `UPDATE `+loc.Schema+`.`+LeaseTable+` SET holder = '', epoch = epoch + 1,
			lease_until = clock_timestamp() WHERE scope = '`+LeaseScope+`'`); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeScripts = nil })
	if s, err := OpenAt(ctx, loc); !errors.Is(err, pglease.ErrLost) {
		if err == nil {
			_ = s.Close()
		}
		t.Fatalf("an open whose lease was taken before its scripts: %v, want the lease lost", err)
	}
	rows, err := admin.QueryContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name <> $2`, loc.Schema, LeaseTable)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int64
	if rows.Next() {
		_ = rows.Scan(&n)
	}
	if n != 0 {
		t.Errorf("%d tables made by scripts whose lease was taken over, want none", n)
	}
}

// A store on PostgreSQL has one identity, written at its first open and read back at every open
// after; another store, in another schema, has another. The mutant, named here: an identity drawn
// at every open (the daemon would report a new store_id at each start).
func TestAStoreOnPostgresHasOneIdentity(t *testing.T) {
	ctx := context.Background()
	loc := pgLocation(t)
	s, err := OpenAt(ctx, loc)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ID()
	if err != nil || len(first) != 32 {
		t.Fatalf("the store's identity: %q, %v", first, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if again, err := openPG(t, loc).ID(); err != nil || again != first {
		t.Errorf("the identity after a reopen: %q, %v; want %q", again, err, first)
	}
	other := loc
	other.Schema = "another_store"
	if id, err := openPG(t, other).ID(); err != nil || id == first {
		t.Errorf("another schema's store has the identity %q, %v; want its own", id, err)
	}
}

// A daemon restarting waits for its predecessor's lease: released (a clean stop), it opens within a
// poll of the release, told once who held it. The mutant, named here: OpenAtWaiting not waiting
// (a restart beside a stopping daemon fails ErrBusy).
func TestOpenAtWaitingTakesAStoreItsHolderReleases(t *testing.T) {
	shortLease(t, 4*time.Second)
	ctx := context.Background()
	loc := pgLocation(t)
	held, err := OpenAt(ctx, loc)
	if err != nil {
		t.Fatal(err)
	}
	told := make(chan error, 4)
	got := make(chan error, 1)
	go func() {
		s, err := OpenAtWaiting(ctx, loc, func(err error) { told <- err })
		if err == nil {
			t.Cleanup(func() { _ = s.Close() })
		}
		got <- err
	}()
	select {
	case err := <-told:
		if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "store=autodoc_store") {
			t.Errorf("the wait was told %v, want ErrBusy naming the holder", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OpenAtWaiting never said it waits")
	}
	released := time.Now()
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("the waiting open: %v", err)
		}
		if waited := time.Since(released); waited > 2*busyPoll {
			t.Errorf("the waiting open took %s after the release, want within a poll (%s)", waited, busyPoll)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting open never opened the released store")
	}
	if len(told) != 0 {
		t.Errorf("the wait was told %d more times, want once", len(told))
	}
}

// A daemon restarting after its predecessor was killed (the lease left held, never renewed) takes
// the store once the lease's length has passed, and no sooner.
func TestOpenAtWaitingTakesAKilledHoldersStoreAtItsTTL(t *testing.T) {
	shortLease(t, 3*time.Second)
	ctx := context.Background()
	loc := pgLocation(t)
	s := openPG(t, loc)
	// the holder killed: its row another holder's, live for a lease, that nothing renews or releases
	if _, err := s.leaseConn().ExecContext(ctx, `UPDATE `+LeaseTable+` SET holder = 'killed', epoch = epoch + 1,
		lease_until = clock_timestamp() + interval '3 seconds' WHERE scope = '`+LeaseScope+`'`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, err := OpenAtWaiting(ctx, loc, nil)
	if err != nil {
		t.Fatalf("the open after the holder was killed: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() })
	if waited := time.Since(start); waited < 2*time.Second || waited > 3*time.Second+2*busyPoll {
		t.Errorf("the open took the killed holder's store after %s, want at its lease's end (3 s)", waited)
	}
}
