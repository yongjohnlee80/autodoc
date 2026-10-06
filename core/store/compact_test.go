package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// filled is a store holding a table of n rows of 4 KiB, in the same file as everything else.
func filled(t *testing.T, n int) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.w.ExecContext(ctx, "CREATE TABLE ballast (id INTEGER PRIMARY KEY, b BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, "INSERT INTO ballast (b) SELECT randomblob(4096) FROM generate_series(1, ?)", n); err != nil {
		// generate_series is an extension: a recursive CTE does the same
		if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE k(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM k WHERE i < ?)
			INSERT INTO ballast (b) SELECT randomblob(4096) FROM k`, n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestCompactConvertsAndShrinksOnline (ADR 1791284787 §2.5): through the writer's own connection,
// outside a transaction, with a read held open across it, Compact puts the store in incremental
// mode and gives the space of a large delete back; the held read still answers from its snapshot.
// After it, an incremental vacuum returns what a later delete frees.
func TestCompactConvertsAndShrinksOnline(t *testing.T) {
	ctx := context.Background()
	s := filled(t, 5000)
	if mode, err := s.AutoVacuum(ctx); err != nil || mode != AutoVacuumNone {
		t.Fatalf("a new store's auto_vacuum: %d, %v; want none", mode, err)
	}
	if _, err := s.w.ExecContext(ctx, "DELETE FROM ballast WHERE id > 1000"); err != nil {
		t.Fatal(err)
	}
	full := s.FileSize()

	// a read transaction, opened and holding its snapshot, stays open until the compaction is done
	opened, release := make(chan struct{}), make(chan struct{})
	readDone := make(chan error, 1)
	var seen int
	go func() {
		readDone <- s.Read(ctx, func(tx *Tx) error {
			// a read through tx: it starts the transaction's snapshot, which then stays open
			if _, err := s.Workspace(1).Self(tx).Select(WorkspaceID); err != nil {
				return err
			}
			seen++
			close(opened)
			<-release
			return nil
		})
	}()
	<-opened
	c, err := s.Compact(ctx)
	close(release)
	if err != nil {
		t.Fatalf("Compact with a read open: %v", err)
	}
	if err := <-readDone; err != nil || seen != 1 {
		t.Fatalf("the read held across the compaction: %v (read %d)", err, seen)
	}
	if mode, _ := s.AutoVacuum(ctx); mode != AutoVacuumIncremental {
		t.Errorf("auto_vacuum after Compact: %d, want incremental", mode)
	}
	if c.Before != full {
		t.Errorf("Compact's Before is %d, the store was %d", c.Before, full)
	}
	// the read held the old snapshot, so the log could not be checkpointed back yet: it can now
	if _, err := s.w.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if after := s.FileSize(); after >= full/2 {
		t.Errorf("compacted %d bytes to %d once the read was done: want well under half (4,000 of 5,000 rows deleted)", full, after)
	}

	if _, err := s.w.ExecContext(ctx, "DELETE FROM ballast"); err != nil {
		t.Fatal(err)
	}
	if free, _ := s.FreePages(ctx); free < 900 {
		t.Fatalf("the delete freed %d pages: the cell expects about a thousand", free)
	}
	if err := s.IncrementalVacuum(ctx); err != nil {
		t.Fatal(err)
	}
	if free, _ := s.FreePages(ctx); free != 0 {
		t.Errorf("%d pages still free after the incremental vacuum", free)
	}
}

// TestCompactRefusesWithoutRoom: with less free space than twice the store, nothing runs: the store
// stays as it was, in its mode, and the refusal says how much is needed.
func TestCompactRefusesWithoutRoom(t *testing.T) {
	ctx := context.Background()
	s := filled(t, 500)
	was := freeSpace
	freeSpace = func(string) (int64, error) { return s.FileSize(), nil } // once the store, not twice
	t.Cleanup(func() { freeSpace = was })
	_, err := s.Compact(ctx)
	if !errors.Is(err, ErrNoRoom) || !strings.Contains(err.Error(), "MB free beside the store") {
		t.Fatalf("Compact with too little room: %v", err)
	}
	if mode, _ := s.AutoVacuum(ctx); mode != AutoVacuumNone {
		t.Errorf("a refused compaction changed the mode to %d", mode)
	}
}

// TestCompactKeepsTempFilesBesideTheStore: the temporary files of VACUUM go to the store's own
// directory, which the driver confirms.
func TestCompactKeepsTempFilesBesideTheStore(t *testing.T) {
	ctx := context.Background()
	s := filled(t, 10)
	if _, err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.w.QueryContext(ctx, "PRAGMA temp_store_directory")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var dir string
	if rows.Next() {
		_ = rows.Scan(&dir)
	}
	if dir != filepath.Dir(s.path) {
		t.Errorf("temp_store_directory = %q, want the store's %q", dir, filepath.Dir(s.path))
	}
}
