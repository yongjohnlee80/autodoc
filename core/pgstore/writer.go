package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/errs"

	adclock "github.com/yongjohnlee80/autodoc/core/clock"
)

// One writer per tenant, for a server that indexes a source into it: the writer holds the
// tenant's lock (AcquireWriter) and confirms it (Check) before each commit, so a writer that lost it
// writes nothing more.

// ErrWriterLost is a tenant's writer lock gone from under its writer: its connection closed or was
// killed. The writer must stop writing; taking the lock again is a new AcquireWriter.
var ErrWriterLost = errs.Sentinel(errs.ErrPrecondition, "pgstore: the tenant's writer lock was lost")

// WriterKey is the advisory lock key of a tenant in a schema: the first 8 bytes of a SHA-256 over
// both, with the sign bit cleared, so pg_locks shows it as a non-negative (classid << 32 | objid)
// and two servers configured alike agree on it whatever PostgreSQL's hash functions do.
func WriterKey(schema, tenant string) int64 {
	h := sha256.Sum256([]byte("autodoc-enterprise\x00" + schema + "\x00" + tenant))
	return int64(binary.BigEndian.Uint64(h[:8]) &^ (1 << 63))
}

// HeldAs selects the grants of the advisory lock keyed $1 in this database: an advisory lock is
// the database's, and pg_locks shows the whole server's.
const HeldAs = `FROM pg_locks WHERE locktype = 'advisory' AND granted AND objsubid = 1
	AND ((classid::bigint << 32) | objid::bigint) = $1
	AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`

// WriterLock is a tenant's writer lock: a session advisory lock held on a connection pinned for
// the writer's whole life, so the tenant has one writer at a time.
//
// A pool hands each statement whichever physical connection is free and recycles idle ones, so a
// session lock taken through it would stay on a connection that goes back to the pool, or is
// closed, while another server takes the same lock and writes beside this one. The lock's
// connection is therefore pinned (acquired and never returned) from a pool of its own, and every
// statement of the lock runs on it.
//
// Its methods are safe for concurrent use: a writer checks the lock before each commit while a
// ticker of its own checks it as well.
type WriterLock struct {
	mu   sync.Mutex
	pin  postgres.PinnedConn
	key  int64
	pid  int64 // the pinned connection's backend
	lost bool
}

// AcquireWriter pins a connection of conn, a pool of its own, and takes the writer lock of tenant
// in schema on it, trying every poll until it is free or ctx ends (ctx's error then, whatever the
// driver reported as the deadline passed). waiting is
// called once, with the backend that holds it, when the lock is first found held by another
// session.
func AcquireWriter(ctx context.Context, conn dao.DataConn, schema, tenant string, poll time.Duration, waiting func(holder int64)) (*WriterLock, error) {
	key := WriterKey(schema, tenant)
	pin, err := postgres.PinSessionConn(ctx, conn)
	if err != nil {
		if err := ended(ctx); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("pgstore: pin the writer lock's connection: %w", err)
	}
	l := &WriterLock{pin: pin, key: key}
	told := false
	for {
		var ok bool
		var holder int64
		err := l.query(ctx, func(tx dao.ContextTxConn) error {
			return scanOne(ctx, tx, `SELECT pg_try_advisory_lock($1), pg_backend_pid(),
				coalesce((SELECT pid `+HeldAs+` AND pid <> pg_backend_pid() LIMIT 1), 0)`,
				[]any{key}, &ok, &l.pid, &holder)
		})
		if err != nil {
			pin.Discard()
			if err := ended(ctx); err != nil {
				// the caller stopped waiting: the driver's error (an i/o timeout at the deadline)
				// says nothing more than that
				return nil, err
			}
			return nil, fmt.Errorf("pgstore: take the writer lock: %w", err)
		}
		if ok {
			return l, nil
		}
		if !told && waiting != nil {
			waiting(holder)
			told = true
		}
		// NOTE: polled, not waited on. The lock is held by another server's session, and nothing
		// tells this one when that session lets go or dies; a blocking pg_advisory_lock could not
		// be bounded by ctx on a pinned connection. A try every poll keeps a standby
		// server's takeover prompt.
		if !adclock.Sleep(ctx, poll) {
			pin.Discard() // nothing is held: the connection only goes
			return nil, ctx.Err()
		}
	}
}

// Check confirms the lock is still this session's: the connection answers and pg_locks shows the
// lock granted to it. Any failure is ErrWriterLost, and stays so; a ctx that ended is its error.
func (l *WriterLock) Check(ctx context.Context) error {
	l.mu.Lock()
	lost := l.lost
	l.mu.Unlock()
	if lost {
		return ErrWriterLost
	}
	var held bool
	err := l.query(ctx, func(tx dao.ContextTxConn) error {
		return scanOne(ctx, tx, `SELECT EXISTS (SELECT 1 `+HeldAs+` AND pid = pg_backend_pid())`,
			[]any{l.key}, &held)
	})
	switch {
	case err != nil && ended(ctx) != nil:
		return ended(ctx) // the caller stopped: that says nothing of the lock
	case err != nil:
		l.markLost()
		return fmt.Errorf("%w: %v", ErrWriterLost, err)
	case !held:
		l.markLost()
		return ErrWriterLost
	}
	return nil
}

// ended is ctx's error once it has ended, counting a deadline that has passed though ctx's own
// timer has not yet fired. The driver bounds a write by the socket's deadline, set to ctx's, and the
// runtime can fail that write with an i/o timeout a moment before the timer marks ctx done: in that
// window ctx.Err() is still nil, and the caller's deadline would read as a broken connection.
func ended(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) {
		return context.DeadlineExceeded
	}
	return nil
}

func (l *WriterLock) markLost() {
	l.mu.Lock()
	l.lost = true
	l.mu.Unlock()
}

// Release lets go of the lock and gives its connection back, or closes the connection when the
// lock was lost or the unlock fails: a session that ends releases its locks.
func (l *WriterLock) Release() {
	l.mu.Lock()
	lost := l.lost
	l.mu.Unlock()
	if !lost {
		// NOTE: a bound on the unlock, which runs as the ingester stops, whatever ended its context;
		// a connection that does not answer within it is closed, which releases the lock as well.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		err := l.query(ctx, func(tx dao.ContextTxConn) error {
			return scanOne(ctx, tx, "SELECT pg_advisory_unlock($1)", []any{l.key}, &unlocked)
		})
		if err == nil && unlocked && l.pin.Release(ctx) == nil {
			return
		}
	}
	l.pin.Discard()
}

// Backend is the process id of the lock's connection on the server.
func (l *WriterLock) Backend() int64 { return l.pid }

// query runs fn in a transaction on the pinned connection, one at a time. A session advisory lock
// taken inside a transaction outlives it.
func (l *WriterLock) query(ctx context.Context, fn func(tx dao.ContextTxConn) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	tx, err := l.pin.BeginSessionTx(ctx, dao.TxOptions{})
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.RollbackContext(context.Background())
		return err
	}
	return tx.CommitContext(ctx)
}

// scanOne runs q and scans its one row into dest.
func scanOne(ctx context.Context, q dao.Querier, sql string, args []any, dest ...any) error {
	rows, err := q.QueryContext(ctx, sql, args...)
	if err != nil {
		return err
	}
	return scanRows(rows, dest...)
}

// scanRows scans the one row of rows into dest and closes them.
func scanRows(rows dao.Rows, dest ...any) error {
	if !rows.Next() {
		err := rows.Err()
		_ = rows.Close()
		if err == nil {
			err = errors.New("no row")
		}
		return err
	}
	if err := rows.Scan(dest...); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

// Now is conn's database's clock (now()): the clock a business database's updated_at values are
// written with, which a reader bounding rows by them reads too, whatever this host's says.
func Now(ctx context.Context, conn dao.DataConn) (time.Time, error) {
	rows, err := conn.QueryContext(ctx, "SELECT now()")
	if err != nil {
		return time.Time{}, fmt.Errorf("pgstore: the database's time: %w", err)
	}
	var t time.Time
	if err := scanRows(rows, &t); err != nil {
		return time.Time{}, fmt.Errorf("pgstore: the database's time: %w", err)
	}
	return t, nil
}
