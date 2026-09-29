// Package store is AutoDoc's one store: every workspace, and every workspace's
// index, in one SQLite file. The schema is sql/deployments, applied by golib
// dao/deploy; every row is read and written through golib dao, one declaration
// per table (tables.go); and a workspace's tables are reached only through its
// Scope, which filters every read and stamps every write with its workspace.
//
// One connection writes (a pool of one, BEGIN IMMEDIATE); any number read, each
// in its own read transaction on the WAL, so a reader sees one commit.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/dao/sqlite"

	"github.com/yongjohnlee80/autodoc/sql/deployments"
)

// Store is the open store.
type Store struct {
	path   string
	w, r   dao.DataConn
	wt, rt *tables
	lease  *os.File
	status deploy.Status
}

// Open takes the store's lease, opens it and brings its schema up to date. A
// lease another process holds is ErrBusy.
func Open(ctx context.Context, path string) (*Store, error) {
	lease, err := acquireLease(path)
	if err != nil {
		return nil, err
	}
	s, err := open(ctx, path)
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	s.lease = lease
	return s, nil
}

func open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	w, err := sqlite.OpenNamed(ctx, "store-w:"+path, dsn+"&_txlock=immediate", sqlite.MaxOpenConns(1))
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}
	st, err := deployments.Runner().Apply(ctx, w)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: %s: %w", path, err)
	}
	readOnly := func(ctx context.Context, c dao.ConnectedConn) error {
		_, err := c.ExecContext(ctx, "PRAGMA query_only=1")
		return err
	}
	r, err := sqlite.OpenHooked(ctx, "store-r:"+path, dsn, readOnly, sqlite.MaxOpenConns(runtime.NumCPU()))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: opening %s for reading: %w", path, err)
	}
	return &Store{path: path, w: w, r: r, wt: newTables(w), rt: newTables(r), status: st}, nil
}

// Schema is what the schema update at Open found and did: the scripts it
// applied (Pending), and any warnings.
func (s *Store) Schema() deploy.Status { return s.status }

// Path is the store's file.
func (s *Store) Path() string { return s.path }

// Close closes the store and releases its lease.
func (s *Store) Close() error {
	err := errors.Join(s.r.Close(), s.w.Close())
	if s.lease != nil {
		err = errors.Join(err, s.lease.Close())
		s.lease = nil
	}
	return err
}

// Tx is one transaction: a write transaction on the writer, or a read
// transaction (a snapshot) on a reader. A Scope's accessors take it.
type Tx struct {
	tx *dao.Transaction
	t  *tables
}

// Write runs fn in a write transaction, committed when fn returns nil.
func (s *Store) Write(ctx context.Context, fn func(tx *Tx) error) error {
	return dao.RunTx(ctx, func(tx *dao.Transaction) error { return fn(&Tx{tx: tx, t: s.wt}) })
}

// Read runs fn in a read transaction: every read in it sees one commit.
func (s *Store) Read(ctx context.Context, fn func(tx *Tx) error) error {
	return dao.RunTx(ctx, func(tx *dao.Transaction) error { return fn(&Tx{tx: tx, t: s.rt}) })
}
