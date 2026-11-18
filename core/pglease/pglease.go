// Package pglease is a writer's lease on PostgreSQL: one holder of a scope at a time, claimed and
// renewed in transactions, with every write fenced in its own transaction. It holds no session
// state: no advisory lock, no pinned connection, nothing that a dropped connection loses.
//
// A lease is a row of a lease table (Table), keyed by its scope:
//
//	scope text COLLATE "C" PRIMARY KEY, holder text NOT NULL, epoch bigint NOT NULL,
//	lease_until timestamptz NOT NULL, claimed_at timestamptz, renewed_at timestamptz, holder_info text
//
// Every time is the database's own clock_timestamp(), never now() (a transaction's start) and
// never a host's clock. The one local clock is the holder's monotonic one, for losing a lease by
// silence (Check).
//
// How the row locks fit (measured on PostgreSQL 18): a fence holds the row FOR KEY SHARE until its
// write transaction ends. That conflicts only with FOR UPDATE, so the holder's renew, a plain
// UPDATE of columns no unique index holds (FOR NO KEY UPDATE), is never held up by its own writes,
// however long, while a claim, which takes the row FOR UPDATE, cannot land while a fenced write is
// open. A claim takes it NOWAIT, so a claimer never queues on the row in front of the holder's
// renew. An INSERT … ON CONFLICT DO UPDATE claim would lock the row only FOR NO KEY UPDATE and
// take the lease over in the middle of a fenced write: Claim never writes one.
package pglease

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/clock"
)

// ErrHeld is a lease another holder has, or a claim refused while a write or another claim holds
// its row: a *HeldError says who.
var ErrHeld = errs.Sentinel(errs.ErrPrecondition, "pglease: the lease is held by another holder")

// ErrLost is a lease no longer this holder's: taken over, released, or not renewed in time. It
// stays lost; holding the scope again is a new Claim.
var ErrLost = errs.Sentinel(errs.ErrPrecondition, "pglease: the lease was lost")

// HeldError is ErrHeld with its holder: who holds the lease and until when, as the row says.
// Busy is a claim refused because the row was locked (a fenced write or another claim in
// flight): the holder is then as a plain read found it, and may be empty.
type HeldError struct {
	Holder Holder
	Busy   bool
}

func (e *HeldError) Error() string {
	if e.Holder.ID == "" {
		return ErrHeld.Error()
	}
	return fmt.Sprintf("%s: %s (%s), until %s", ErrHeld, e.Holder.ID, e.Holder.Info, e.Holder.Until.Format(time.RFC3339))
}

func (e *HeldError) Is(target error) bool { return target == ErrHeld }

// Unwrap is ErrHeld, so a HeldError is the precondition ErrHeld is (errs.ErrPrecondition).
func (e *HeldError) Unwrap() error { return ErrHeld }

// Holder is a lease row's holder, as a claim found it.
type Holder struct {
	ID, Info string
	Until    time.Time
}

// Options name a lease and its timing.
type Options struct {
	// Table is the lease table, a plain identifier the connection's search_path finds.
	Table string
	// Scope is what the lease is of: one holder per scope.
	Scope string
	// TTL is how long a claim or a renew holds the lease (DefaultTTL). Renew every TTL/3.
	TTL time.Duration
	// Info is what the holder says of itself in holder_info, after the host, pid and the process's
	// start, which Claim adds: an operator tells a restart from a twin by them.
	Info string
}

// DefaultTTL is a lease's length when Options.TTL is 0.
const DefaultTTL = 30 * time.Second

// lockNotAvailable is PostgreSQL's SQLSTATE for a NOWAIT lock refused.
const lockNotAvailable = "55P03"

var plainIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// processStart is this process's start, as holder_info says it.
var processStart = time.Now()

// Lease is a held lease. Its methods are safe for concurrent use.
type Lease struct {
	conn dao.DataConn
	o    Options
	id   string // this claimant's random id: holder in the row
	sql  statements

	mu       sync.Mutex
	epoch    int64
	renewed  time.Time // the start of the last good claim or renew, on the monotonic clock
	lost     error     // sticky once set
	released bool
	now      func() time.Time
}

type statements struct{ insert, lock, peek, claim, renew, fence, release string }

func statementsFor(table string) statements {
	return statements{
		insert: `INSERT INTO ` + table + ` (scope, holder, epoch, lease_until) VALUES ($1, '', 0, to_timestamp(0)) ON CONFLICT (scope) DO NOTHING`,
		lock:   `SELECT holder, coalesce(holder_info, ''), lease_until, lease_until > clock_timestamp() FROM ` + table + ` WHERE scope = $1 FOR UPDATE NOWAIT`,
		peek:   `SELECT holder, coalesce(holder_info, ''), lease_until FROM ` + table + ` WHERE scope = $1`,
		claim: `UPDATE ` + table + ` SET holder = $2, epoch = epoch + 1, lease_until = clock_timestamp() + make_interval(secs => $3::float8),
			claimed_at = clock_timestamp(), renewed_at = clock_timestamp(), holder_info = $4 WHERE scope = $1 RETURNING epoch`,
		renew: `UPDATE ` + table + ` SET lease_until = clock_timestamp() + make_interval(secs => $4::float8), renewed_at = clock_timestamp()
			WHERE scope = $1 AND holder = $2 AND epoch = $3`,
		fence: `SELECT 1 FROM ` + table + ` WHERE scope = $1 AND holder = $2 AND epoch = $3 AND lease_until > clock_timestamp() FOR KEY SHARE`,
		release: `UPDATE ` + table + ` SET holder = '', epoch = epoch + 1, lease_until = clock_timestamp()
			WHERE scope = $1 AND holder = $2 AND epoch = $3`,
	}
}

func (o *Options) check() error {
	if !plainIdent.MatchString(o.Table) {
		return fmt.Errorf("pglease: the lease table %q is not a plain identifier", o.Table)
	}
	if o.Scope == "" {
		return errors.New("pglease: no scope")
	}
	if o.TTL == 0 {
		o.TTL = DefaultTTL
	}
	if o.TTL < 0 {
		return fmt.Errorf("pglease: a negative TTL %s", o.TTL)
	}
	return nil
}

// Claim tries once to take the lease: a *HeldError while another holder has it, or its row is
// locked by a write or a claim in flight. A lease that expired, or was released, is taken over at
// the next epoch.
func Claim(ctx context.Context, conn dao.DataConn, o Options) (*Lease, error) {
	if err := o.check(); err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	l := &Lease{conn: conn, o: o, id: hex.EncodeToString(b), sql: statementsFor(o.Table), now: time.Now}
	if err := l.claim(ctx); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Lease) claim(ctx context.Context) error {
	host, _ := os.Hostname()
	info := fmt.Sprintf("host=%s pid=%d start=%s", host, os.Getpid(), processStart.UTC().Format(time.RFC3339Nano))
	if l.o.Info != "" {
		info += " " + l.o.Info
	}
	start := l.now()
	tx, err := dao.BeginConnTx(ctx, l.conn, dao.TxOptions{Isolation: dao.TxReadCommitted})
	if err != nil {
		return fmt.Errorf("pglease: claim %s: %w", l.o.Scope, err)
	}
	epoch, err := l.claimIn(ctx, tx, info)
	if err != nil {
		_ = tx.Rollback()
		var held *HeldError
		if errors.As(err, &held) && held.Busy {
			held.Holder, _ = l.peek(ctx) // who, as a plain read sees it: it takes no lock
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("pglease: claim %s: %w", l.o.Scope, err)
	}
	l.mu.Lock()
	l.epoch, l.renewed = epoch, start
	l.mu.Unlock()
	return nil
}

func (l *Lease) claimIn(ctx context.Context, tx dao.TxConn, info string) (int64, error) {
	if _, err := tx.ExecContext(ctx, l.sql.insert, l.o.Scope); err != nil {
		return 0, fmt.Errorf("pglease: claim %s: %w", l.o.Scope, err)
	}
	var h Holder
	var live bool
	if err := scanOne(ctx, tx, l.sql.lock, []any{l.o.Scope}, &h.ID, &h.Info, &h.Until, &live); err != nil {
		if sqlState(err) == lockNotAvailable {
			return 0, &HeldError{Busy: true}
		}
		return 0, fmt.Errorf("pglease: claim %s: %w", l.o.Scope, err)
	}
	if live && h.ID != "" {
		return 0, &HeldError{Holder: h}
	}
	var epoch int64
	if err := scanOne(ctx, tx, l.sql.claim, []any{l.o.Scope, l.id, l.o.TTL.Seconds(), info}, &epoch); err != nil {
		return 0, fmt.Errorf("pglease: claim %s: %w", l.o.Scope, err)
	}
	return epoch, nil
}

func (l *Lease) peek(ctx context.Context) (Holder, error) {
	var h Holder
	err := scanOne(ctx, l.conn, l.sql.peek, []any{l.o.Scope}, &h.ID, &h.Info, &h.Until)
	return h, err
}

// Acquire claims the lease, trying every poll while another holder has it, until ctx ends (its
// error then). waiting is called once, with the holder, when the lease is first found held.
func Acquire(ctx context.Context, conn dao.DataConn, o Options, poll time.Duration, waiting func(Holder)) (*Lease, error) {
	told := false
	for {
		// a deadline already past ends the wait, though ctx's own timer has not yet fired: a pool
		// connection does not bound a statement by a deadline the context has not yet acted on
		if err := ended(ctx); err != nil {
			return nil, err
		}
		l, err := Claim(ctx, conn, o)
		var held *HeldError
		switch {
		case err == nil:
			return l, nil
		case !errors.As(err, &held):
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
			return nil, err
		}
		if !told && waiting != nil {
			waiting(held.Holder)
			told = true
		}
		// NOTE: polled, not waited on. Nothing tells a standby when the holder releases the lease
		// or stops renewing it, so a claim every poll keeps its takeover prompt.
		if !clock.Sleep(ctx, poll) {
			return nil, ctx.Err()
		}
	}
}

// ended is ctx's error once it has ended, counting a deadline that has passed though ctx's own
// timer has not yet fired.
func ended(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) {
		return context.DeadlineExceeded
	}
	return nil
}

// ID is this holder's id, as the row's holder column says it.
func (l *Lease) ID() string { return l.id }

// Epoch is the epoch this holder claimed at.
func (l *Lease) Epoch() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.epoch
}

// TTL is the lease's length.
func (l *Lease) TTL() time.Duration { return l.o.TTL }

// silence is how long a holder may go without a good renew before it counts the lease lost:
// TTL less a sixth, so it stops before the database's lease_until passes.
func (l *Lease) silence() time.Duration { return l.o.TTL - l.o.TTL/6 }

// Check is the lease as this holder knows it, with no I/O: ErrLost once it was lost, released, or
// went a TTL less a sixth without a good renew (by the monotonic clock, whatever the database
// answers). It is nil otherwise; a write's own fence confirms it in the database.
func (l *Lease) Check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.checkLocked()
}

func (l *Lease) checkLocked() error {
	if l.lost != nil {
		return l.lost
	}
	if l.now().Sub(l.renewed) > l.silence() {
		l.lost = fmt.Errorf("%w: no renew for %s", ErrLost, l.silence())
		return l.lost
	}
	return nil
}

func (l *Lease) markLost(err error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lost == nil {
		l.lost = err
	}
	return l.lost
}

// Renew extends the lease by a TTL from now, in the database's clock, bounded by a sixth of the
// TTL. It runs on the holder's pool, beside its writes: a pool too small for the writes that may
// hold its connections at once (an ingester's and the embedding worker's) can hold a renew up past
// its bound, and a holder silent for most of the TTL counts its lease lost. A lease taken over (its epoch moved) or released is ErrLost, and stays so; any other
// failure is returned as it is, leaving the lease to Check's silence rule.
func (l *Lease) Renew(ctx context.Context) error {
	l.mu.Lock()
	if err := l.checkLocked(); err != nil {
		l.mu.Unlock()
		return err
	}
	epoch := l.epoch
	l.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, l.o.TTL/6)
	defer cancel()
	start := l.now()
	res, err := l.conn.ExecContext(ctx, l.sql.renew, l.o.Scope, l.id, epoch, l.o.TTL.Seconds())
	if err != nil {
		return fmt.Errorf("pglease: renew %s: %w", l.o.Scope, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("pglease: renew %s: %w", l.o.Scope, err)
	} else if n == 0 {
		return l.markLost(fmt.Errorf("%w: taken over or released", ErrLost))
	}
	l.mu.Lock()
	if l.lost == nil && start.After(l.renewed) {
		l.renewed = start
	}
	l.mu.Unlock()
	return nil
}

// Fence confirms the lease inside a write transaction, as its first statement: q is that
// transaction. It holds the lease's row FOR KEY SHARE until the transaction ends, so no claim
// takes the lease over before this write commits or rolls back. A lease not this holder's, or
// expired by the database's clock, is ErrLost; the caller rolls the transaction back.
func (l *Lease) Fence(ctx context.Context, q dao.Querier) error {
	l.mu.Lock()
	if err := l.checkLocked(); err != nil {
		l.mu.Unlock()
		return err
	}
	epoch := l.epoch
	l.mu.Unlock()
	rows, err := q.QueryContext(ctx, l.sql.fence, l.o.Scope, l.id, epoch)
	if err != nil {
		return fmt.Errorf("pglease: fence %s: %w", l.o.Scope, err)
	}
	held := rows.Next()
	err = rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("pglease: fence %s: %w", l.o.Scope, err)
	case !held:
		return l.markLost(fmt.Errorf("%w: the fence found the lease another's, or expired", ErrLost))
	}
	return nil
}

// Release lets the lease go: another holder may claim it at once, and a renew of this one after it
// matches nothing (the epoch moved). ctx should be a fresh one, never the one whose end stopped the
// writer: a statement cancelled under it would leave the lease to expire instead.
func (l *Lease) Release(ctx context.Context) error {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return nil
	}
	epoch := l.epoch
	l.released, l.lost = true, fmt.Errorf("%w: released", ErrLost)
	l.mu.Unlock()
	if _, err := l.conn.ExecContext(ctx, l.sql.release, l.o.Scope, l.id, epoch); err != nil {
		return fmt.Errorf("pglease: release %s: %w", l.o.Scope, err)
	}
	return nil
}

// WriterSession is the pool option a lease holder's writes run under: a transaction left idle
// past twice the lease's TTL is ended by the database, so a hung writer's open fence cannot keep a
// standby from taking over for ever.
func WriterSession(ttl time.Duration) postgres.Option {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return postgres.WithConnectHook(func(ctx context.Context, c dao.ConnectedConn) error {
		ms := (2 * ttl).Milliseconds()
		if _, err := c.ExecContext(ctx, fmt.Sprintf("SET idle_in_transaction_session_timeout = %d", ms)); err != nil {
			return fmt.Errorf("pglease: set idle_in_transaction_session_timeout: %w", err)
		}
		return nil
	})
}

// sqlState is err's PostgreSQL SQLSTATE, "" when it carries none.
func sqlState(err error) string {
	var e interface{ SQLState() string }
	if errors.As(err, &e) {
		return e.SQLState()
	}
	return ""
}

// scanOne runs sql on q and scans its one row into dest.
func scanOne(ctx context.Context, q dao.Querier, sql string, args []any, dest ...any) error {
	rows, err := q.QueryContext(ctx, sql, args...)
	if err != nil {
		return err
	}
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
