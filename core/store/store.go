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

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/sql/deployments"
)

// Store is the open store.
type Store struct {
	path   string
	w, r   dao.DataConn
	wt, rt *tables
	lease  *os.File
	status deploy.Status
	keys   *keyslot // the store's key, <store>.key (keyslot.go)
	now    clock.Clock
}

// Option configures a Store.
type Option func(*Store)

// WithClock is the clock the store's recorded times read (updated_at, an event's time); the
// default is time.Now.
func WithClock(c clock.Clock) Option { return func(s *Store) { s.now = c } }

// Open takes the store's lease, opens it and brings its schema up to date. A
// lease another process holds is ErrBusy.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
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
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func open(ctx context.Context, path string) (*Store, error) {
	// journal_size_limit: a checkpoint truncates the write-ahead log back to 64 MiB, so the room a
	// large write (a compaction, a reclaim) took in it is given back, not kept at its high-water mark
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_size_limit(67108864)"
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
	return &Store{path: path, w: w, r: r, wt: newTables(w), rt: newTables(r), status: st,
		keys: &keyslot{path: path + ".key"}}, nil
}

// Schema is what the schema update at Open found and did: the scripts it
// applied (Pending), and any warnings.
func (s *Store) Schema() deploy.Status { return s.status }

// Clock is the store's clock: the one the daemon hands every workspace's indexer, so the times the
// daemon records all read one clock.
func (s *Store) Clock() clock.Clock { return s.now }

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
	tx  *dao.Transaction
	t   *tables
	now clock.Clock
}

// Write runs fn in a write transaction, committed when fn returns nil.
func (s *Store) Write(ctx context.Context, fn func(tx *Tx) error) error {
	return dao.RunTx(ctx, func(tx *dao.Transaction) error { return fn(&Tx{tx: tx, t: s.wt, now: s.now}) })
}

// Read runs fn in a read transaction: every read in it sees one commit.
func (s *Store) Read(ctx context.Context, fn func(tx *Tx) error) error {
	return dao.RunTx(ctx, func(tx *dao.Transaction) error { return fn(&Tx{tx: tx, t: s.rt, now: s.now}) })
}
