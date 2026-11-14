package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/pglease"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// storeEngineEnv picks the engine openStore opens a store on: "postgres" for a scratch PostgreSQL
// database (AUTODOC_TEST_PGURL), SQLite otherwise.
const storeEngineEnv = "AUTODOC_TEST_STORE_ENGINE"

// pgLocation is a fresh PostgreSQL store's location: a scratch database, a schema in it, and a key
// file of the test's own.
func pgLocation(t *testing.T) Location {
	t.Helper()
	return Location{Engine: EngineOfPostgres, DSN: pgtest.ScratchURL(t), Schema: "autodoc_store",
		KeyFile: filepath.Join(t.TempDir(), "store.key")}
}

func openPG(t *testing.T, loc Location) *Store {
	t.Helper()
	s, err := OpenAt(context.Background(), loc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
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
