package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// COMPACTING (ADR 1791284787 §2.5) — SQLite puts the pages a delete frees on its freelist: the file
// does not shrink, and the rows kept stay where they were, among the gaps. In incremental
// auto-vacuum mode, PRAGMA incremental_vacuum returns the freelist to the file system; switching a
// store into that mode takes one VACUUM, which also rewrites every table contiguously.
//
// Both run outside any transaction (SQLite refuses VACUUM inside one, and Write always opens one),
// on the writer's one connection: writes wait for them, and reads, the store being in WAL mode, go
// on from their snapshots.

// The store's auto_vacuum modes.
const (
	AutoVacuumNone        = 0
	AutoVacuumFull        = 1
	AutoVacuumIncremental = 2
)

// ErrNoRoom is a compaction refused before it starts: the store's file system has less free space
// than VACUUM may need.
var ErrNoRoom = errors.New("store: not enough free space to compact the store")

// AutoVacuum is the store's auto_vacuum mode.
func (s *Store) AutoVacuum(ctx context.Context) (int, error) {
	mode, err := s.pragma(ctx, "auto_vacuum")
	return int(mode), err
}

// FreePages is how many of the store's pages are on its freelist.
func (s *Store) FreePages(ctx context.Context) (int64, error) {
	return s.pragma(ctx, "freelist_count")
}

// pragma reads a pragma's one number, on the writer's connection.
func (s *Store) pragma(ctx context.Context, name string) (int64, error) {
	rows, err := s.w.QueryContext(ctx, "PRAGMA "+name)
	if err != nil {
		return 0, fmt.Errorf("store: reading %s: %w", name, err)
	}
	defer rows.Close()
	var n int64
	if !rows.Next() {
		return 0, fmt.Errorf("store: reading %s: no row", name)
	}
	if err := rows.Scan(&n); err != nil {
		return 0, fmt.Errorf("store: reading %s: %w", name, err)
	}
	return n, rows.Err()
}

// IncrementalVacuum returns the freelist's pages to the file system. A store not yet in
// incremental mode keeps them, to reuse: it does nothing.
func (s *Store) IncrementalVacuum(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return fmt.Errorf("store: incremental_vacuum: %w", err)
	}
	return nil
}

// Compaction is what a compaction did: the store's file's size before and after, in bytes. The
// write-ahead log is not counted: it is transient, and empties at a checkpoint.
type Compaction struct {
	Before, After int64
}

// freeSpace is the free space on the file system holding dir, in bytes, as an unprivileged writer
// sees it (a variable, so a test can say there is none).
var freeSpace = func(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// FileSize is the store's file's size in bytes, its write-ahead log aside.
func (s *Store) FileSize() int64 {
	fi, err := os.Stat(s.path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// Checkpoint copies the write-ahead log into the store's file and empties it, waiting for the reads
// that hold older snapshots (up to the busy timeout): for a store no daemon serves, whose file then
// shows its size (--compact).
func (s *Store) Checkpoint(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("store: checkpoint: %w", err)
	}
	return nil
}

// Compact puts the store in incremental auto-vacuum mode and rewrites it with VACUUM. VACUUM may need
// free space of twice the store's size, a temporary copy and its journal (sqlite.org/lang_vacuum),
// so less than that is ErrNoRoom, before anything runs. Its temporary files go beside the store,
// never to the system's temp directory, which may be memory.
//
// The rewrite goes through the write-ahead log. A read holding the old snapshot keeps the log from
// being checkpointed back into a smaller file: the store reaches its new size at the first checkpoint
// after such reads are done. After reports the size once Compact's own checkpoint has run.
func (s *Store) Compact(ctx context.Context) (Compaction, error) {
	before := s.FileSize()
	dir := filepath.Dir(s.path)
	free, err := freeSpace(dir)
	if err != nil {
		return Compaction{}, fmt.Errorf("store: reading the free space beside %s: %w", s.path, err)
	}
	if free < 2*before {
		return Compaction{}, fmt.Errorf("%w: compacting needs %d MB free beside the store, %d MB are",
			ErrNoRoom, 2*before>>20, free>>20)
	}
	for _, stmt := range []string{
		fmt.Sprintf("PRAGMA temp_store_directory = '%s'", escapeLiteral(dir)),
		"PRAGMA auto_vacuum = INCREMENTAL",
		"VACUUM",
		"PRAGMA wal_checkpoint(PASSIVE)", // a TRUNCATE would hold the writer while reads finish
	} {
		if _, err := s.w.ExecContext(ctx, stmt); err != nil {
			return Compaction{}, fmt.Errorf("store: compacting (%s): %w", stmt, err)
		}
	}
	return Compaction{Before: before, After: s.FileSize()}, nil
}

// escapeLiteral doubles a string's quotes for an SQL literal.
func escapeLiteral(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(out)
}
