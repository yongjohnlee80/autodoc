package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/clock"
)

// ErrLockLost is the tenant's writer lock gone from under its ingester: its connection closed or
// was killed. The ingester stops writing at once (a page it was writing is rolled back) and Run
// returns this, so the process exits non-zero and its supervisor restarts it to take the lock
// again; it never goes on writing unlocked.
var ErrLockLost = errs.Sentinel(errs.ErrPrecondition, "ingest: the tenant's writer lock was lost")

// ErrNotLocked is a write asked of an ingester that does not hold its tenant's lock.
var ErrNotLocked = errs.Sentinel(errs.ErrPrecondition, "ingest: the tenant's writer lock is not held")

// lockKey is the advisory lock key of a tenant in a schema: the first 8 bytes of a SHA-256 over
// both, with the sign bit cleared, so pg_locks shows it as a non-negative (classid << 32 | objid)
// and two servers configured alike agree on it whatever PostgreSQL's hash functions do.
func lockKey(schema, tenant string) int64 {
	h := sha256.Sum256([]byte("autodoc-enterprise\x00" + schema + "\x00" + tenant))
	return int64(binary.BigEndian.Uint64(h[:8]) &^ (1 << 63))
}

// heldAs selects the grants of the advisory lock keyed $1 in this database: an advisory lock is
// the database's, and pg_locks shows the whole server's.
const heldAs = `FROM pg_locks WHERE locktype = 'advisory' AND granted AND objsubid = 1
	AND ((classid::bigint << 32) | objid::bigint) = $1
	AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`

// tenantLock is a session advisory lock held on a connection pinned for the ingester's whole life.
//
// A pool hands each statement whichever physical connection is free and recycles idle ones, so a
// session lock taken through it would stay on a connection that goes back to the pool, or is
// closed, while another server takes the same lock and writes beside this one. The lock's
// connection is therefore pinned (acquired and never returned) from a pool of its own, and every
// statement of the lock runs on it.
//
// Its methods are safe for concurrent use: the ingester checks the lock before each commit while
// Run's ticker checks it every ping.
type tenantLock struct {
	mu   sync.Mutex
	pin  postgres.PinnedConn
	key  int64
	pid  int64 // the pinned connection's backend, for a test that kills it
	lost bool
}

// acquire pins a connection of conn and takes the lock on it, trying every poll until it is free
// or ctx ends. waiting is called once, when the lock is first found held by another session.
func acquire(ctx context.Context, conn dao.DataConn, key int64, poll time.Duration, waiting func(holder int64)) (*tenantLock, error) {
	pin, err := postgres.PinSessionConn(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ingest: pin the lock's connection: %w", err)
	}
	l := &tenantLock{pin: pin, key: key}
	told := false
	for {
		var ok bool
		var holder int64
		err := l.query(ctx, func(tx dao.ContextTxConn) error {
			return scanOne(ctx, tx, `SELECT pg_try_advisory_lock($1), pg_backend_pid(),
				coalesce((SELECT pid `+heldAs+` AND pid <> pg_backend_pid() LIMIT 1), 0)`,
				[]any{key}, &ok, &l.pid, &holder)
		})
		if err != nil {
			pin.Discard()
			if ctx.Err() != nil {
				// the caller stopped waiting: the driver's error (an i/o timeout at the deadline)
				// says nothing more than that
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("ingest: take the tenant's lock: %w", err)
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
		// be bounded by ctx on a pinned connection. A try every poll (LockPoll) keeps a standby
		// server's takeover prompt.
		if !clock.Sleep(ctx, poll) {
			pin.Discard() // nothing is held: the connection only goes
			return nil, ctx.Err()
		}
	}
}

// check confirms the lock is still this session's: the connection answers and pg_locks shows the
// lock granted to it. Any failure is ErrLockLost, and stays so.
func (l *tenantLock) check(ctx context.Context) error {
	l.mu.Lock()
	lost := l.lost
	l.mu.Unlock()
	if lost {
		return ErrLockLost
	}
	var held bool
	err := l.query(ctx, func(tx dao.ContextTxConn) error {
		return scanOne(ctx, tx, `SELECT EXISTS (SELECT 1 `+heldAs+` AND pid = pg_backend_pid())`,
			[]any{l.key}, &held)
	})
	switch {
	case err != nil && ctx.Err() != nil:
		return ctx.Err() // the caller stopped: that says nothing of the lock
	case err != nil:
		l.markLost()
		return fmt.Errorf("%w: %v", ErrLockLost, err)
	case !held:
		l.markLost()
		return ErrLockLost
	}
	return nil
}

func (l *tenantLock) markLost() {
	l.mu.Lock()
	l.lost = true
	l.mu.Unlock()
}

// release lets go of the lock and gives its connection back, or closes the connection when the
// lock was lost or the unlock fails: a session that ends releases its locks.
func (l *tenantLock) release() {
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

// query runs fn in a transaction on the pinned connection, one at a time. A session advisory lock
// taken inside a transaction outlives it.
func (l *tenantLock) query(ctx context.Context, fn func(tx dao.ContextTxConn) error) error {
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
