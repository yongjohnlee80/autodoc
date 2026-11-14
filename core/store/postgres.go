package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/core/pglease"
	"github.com/yongjohnlee80/autodoc/sql/deployments"
)

// THE STORE ON POSTGRESQL (docs/decisions/store.md#the-store-on-postgresql). A Location names a
// PostgreSQL database and schema in place of a SQLite file. The store is then:
//
//   - one writer connection, as on SQLite, so the store's writes keep SQLite's order;
//   - a pool of readers, read-only by session;
//   - a writer lease (core/pglease) of scope "store" in the schema's writer_lease table: Open
//     claims it, a live one is ErrBusy (SQLite's single instance), a ticker renews it, and every
//     write transaction confirms it as its first statement;
//   - the key in Location.KeyFile, since there is no file to keep it beside.
//
// What only a SQLite file has answers ErrNotSQLite: the quick and whole checks, the damaged mark,
// vacuuming and compaction, the write-ahead log's checkpoint.

// The engines a Location names.
const (
	EngineOfSQLite   = "sqlite"
	EngineOfPostgres = "postgres"
)

// ErrNotSQLite is an operation only a SQLite store's file has, asked of a store on PostgreSQL.
var ErrNotSQLite = errs.Sentinel(errs.ErrUnsupported, "store: only a SQLite store has this")

// Location is where a store is: a SQLite file (Engine "sqlite", or "": Path), or a PostgreSQL
// database (Engine "postgres": DSN, Schema and KeyFile).
type Location struct {
	Engine string
	// Path is a SQLite store's file.
	Path string
	// DSN is a PostgreSQL store's database; Schema the schema its tables live in, made when missing.
	DSN, Schema string
	// KeyFile is a PostgreSQL store's key, which seals its secrets (a SQLite store's is Path+".key").
	KeyFile string
}

// LeaseTable is the table a PostgreSQL store's writer lease is a row of (000036), and LeaseScope
// its row.
const (
	LeaseTable = "writer_lease"
	LeaseScope = "store"
)

// beforeScripts runs between the lease's claim and the scripts: nil in production, set by a cell
// that stands for a long first start or takes the lease over in that window.
var beforeScripts func()

// leaseTableDDL is 000036's statement, run before the lease is claimed (openPostgres): a store not
// yet at 000036 has its lease's table made, and one at it is left as it is. A cell holds the two
// texts equal.
const leaseTableDDL = `CREATE TABLE IF NOT EXISTS writer_lease (
    scope       text COLLATE "C" PRIMARY KEY,
    holder      text NOT NULL,
    epoch       bigint NOT NULL,
    lease_until timestamptz NOT NULL,
    claimed_at  timestamptz,
    renewed_at  timestamptz,
    holder_info text
)`

// leaseTTL is a PostgreSQL store's writer lease length: a variable, so a cell can stand for a first
// start longer than a shorter one.
var leaseTTL = pglease.DefaultTTL

var plainSchema = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// OpenAt opens the store at loc, as Open opens a SQLite file: it takes the store's lease (a live
// one is ErrBusy), opens it and brings its schema up to date.
func OpenAt(ctx context.Context, loc Location, opts ...Option) (*Store, error) {
	switch loc.Engine {
	case "", EngineOfSQLite:
		return Open(ctx, loc.Path, opts...)
	case EngineOfPostgres:
		return openPostgres(ctx, loc, opts...)
	}
	return nil, fmt.Errorf("store: no engine %q (sqlite or postgres)", loc.Engine)
}

// pgStore is what a store on PostgreSQL holds beside a Store's own: its pools and its lease.
type pgStore struct {
	loc    Location
	leases dao.DataConn
	lease  *pglease.Lease
	stop   context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func openPostgres(ctx context.Context, loc Location, opts ...Option) (*Store, error) {
	switch {
	case loc.DSN == "":
		return nil, errors.New("store: a PostgreSQL store needs its DSN")
	case !plainSchema.MatchString(loc.Schema) || strings.HasPrefix(loc.Schema, "pg_"):
		return nil, fmt.Errorf("store: the schema %q is not a plain name (lowercase letters, digits and _, not pg_)", loc.Schema)
	case loc.KeyFile == "":
		return nil, errors.New("store: a PostgreSQL store needs its key file")
	}
	inSchema := postgres.WithConnectHook(func(ctx context.Context, c dao.ConnectedConn) error {
		_, err := c.ExecContext(ctx, `SET search_path TO "`+loc.Schema+`", public`)
		return err
	})
	readOnly := postgres.WithConnectHook(func(ctx context.Context, c dao.ConnectedConn) error {
		_, err := c.ExecContext(ctx, "SET default_transaction_read_only = on")
		return err
	})
	var opened []dao.DataConn
	fail := func(err error) (*Store, error) {
		for _, c := range opened {
			_ = c.Close()
		}
		return nil, err
	}
	open := func(name string, o ...postgres.Option) (dao.DataConn, error) {
		c, err := postgres.OpenNamed(ctx, "store-"+name+":"+loc.Schema, loc.DSN, o...)
		if err != nil {
			return nil, fmt.Errorf("store: opening PostgreSQL (%s): %w", name, err)
		}
		opened = append(opened, c)
		return c, nil
	}
	// the schema first, on a connection of its own: the pools' search_path names it
	admin, err := open("admin", postgres.MaxOpenConns(1))
	if err != nil {
		return fail(err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS "`+loc.Schema+`"`); err != nil {
		return fail(fmt.Errorf("store: the schema %s: %w", loc.Schema, err))
	}
	// the lease before the scripts: a process that finds the store another's (a newer build beside an
	// older daemon) leaves its schema as it is. The lease's table is made first when missing, with
	// 000036's own statement, so a store not yet at 000036 can be claimed too.
	// NOTE: a pool of its own, of two: a renew must never wait for the writer's one connection,
	// which a write holds for its length
	leases, err := open("lease", postgres.MaxOpenConns(2), inSchema, pglease.WriterSession(leaseTTL))
	if err != nil {
		return fail(err)
	}
	if _, err := leases.ExecContext(ctx, leaseTableDDL); err != nil {
		return fail(fmt.Errorf("store: the writer lease's table: %w", err))
	}
	lease, err := pglease.Claim(ctx, leases, pglease.Options{Table: LeaseTable, Scope: LeaseScope, TTL: leaseTTL, Info: "store=" + loc.Schema})
	var held *pglease.HeldError
	if errors.As(err, &held) {
		return fail(fmt.Errorf("%w (%s, held by %s %s)", ErrBusy, loc.Schema, held.Holder.ID, held.Holder.Info))
	}
	if err != nil {
		return fail(err)
	}
	// the lease is the store's from here: renewed at once, so a long first start (scripts building
	// an index on a large store) never outlasts it, and closed with every failure after this
	runCtx, stop := context.WithCancel(context.Background())
	pg := &pgStore{loc: loc, leases: leases, lease: lease, stop: stop, done: make(chan struct{})}
	go pg.renew(runCtx)
	release := func() { _ = pg.close() }
	w, err := open("w", postgres.MaxOpenConns(1), inSchema, pglease.WriterSession(leaseTTL))
	if err != nil {
		release()
		return fail(err)
	}
	fenced := fencedConn{DataConn: w, fence: lease.Fence}
	if beforeScripts != nil {
		beforeScripts()
	}
	// the scripts through the fenced writer: their one transaction confirms the lease first and
	// holds it against a takeover until it commits
	st, err := deployments.Runner().Apply(ctx, fenced)
	if err != nil {
		release()
		return fail(fmt.Errorf("store: the schema in %s: %w", loc.Schema, err))
	}
	r, err := open("r", inSchema, readOnly)
	if err != nil {
		release()
		return fail(err)
	}
	_ = admin.Close()
	snap := snapshotConn{DataConn: r}
	s := &Store{w: fenced, r: snap, wt: newTables(fenced), rt: newTables(snap), status: st,
		keys: &keyslot{path: loc.KeyFile}, pg: pg}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// renew renews the store's lease every third of its length until the store closes.
func (pg *pgStore) renew(ctx context.Context) {
	defer close(pg.done)
	for {
		// NOTE: a timed wait: the lease lasts its TTL from each renew, so one every third of it keeps
		// the store's while two in a row fail
		if !clock.Sleep(ctx, leaseTTL/3) {
			return
		}
		if err := pg.lease.Renew(ctx); errors.Is(err, pglease.ErrLost) {
			return // every write fails its fence from here: the store is another's
		}
	}
}

// close stops the renew and releases the lease, on a context of its own: the caller's may have
// ended as it stopped.
func (pg *pgStore) close() error {
	var err error
	pg.once.Do(func() {
		pg.stop()
		<-pg.done
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = errors.Join(pg.lease.Release(ctx), pg.leases.Close())
	})
	return err
}

// snapshotConn is the readers of a store on PostgreSQL: each of their transactions is REPEATABLE
// READ and READ ONLY, so every read of one Store.Read sees one commit, as a SQLite read transaction
// (a WAL snapshot) does; PostgreSQL's default, READ COMMITTED, would read each statement afresh.
type snapshotConn struct{ dao.DataConn }

func (c snapshotConn) Begin(ctx context.Context) (dao.TxConn, error) {
	return dao.BeginConnTx(ctx, c.DataConn, dao.TxOptions{Access: dao.TxReadOnly, Isolation: dao.TxRepeatableRead})
}

// fencedConn is the writer of a store on PostgreSQL: each of its transactions begins with the
// store's lease's fence, which confirms the lease and holds it against a takeover until the
// transaction ends.
type fencedConn struct {
	dao.DataConn
	fence func(ctx context.Context, q dao.Querier) error
}

func (c fencedConn) Begin(ctx context.Context) (dao.TxConn, error) {
	tx, err := c.DataConn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.fence(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Location is where the store is.
func (s *Store) Location() Location {
	if s.pg != nil {
		return s.pg.loc
	}
	return Location{Engine: EngineOfSQLite, Path: s.path}
}

// sqliteOnly is ErrNotSQLite for a store on PostgreSQL, nil for a SQLite one.
func (s *Store) sqliteOnly() error {
	if s.pg != nil {
		return ErrNotSQLite
	}
	return nil
}

// leaseConn is a store on PostgreSQL's lease pool, for a cell that reaches the lease's row; nil for
// a SQLite store.
func (s *Store) leaseConn() dao.DataConn {
	if s.pg == nil {
		return nil
	}
	return s.pg.leases
}
