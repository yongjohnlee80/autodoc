package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/pglease"
)

// One writer per tenant, for a server that indexes a source into it: the writer holds the
// tenant's lease (AcquireWriter), a row of rag_writer_lease claimed and renewed in transactions
// (core/pglease), and every write transaction of its Store confirms the lease first, inside the
// transaction (Store.Fenced, Writer.Fence): a writer that lost it writes nothing more, and no other
// writer takes the lease over while one of its writes is open.

// ErrWriterLost is a tenant's writer lease gone from under its writer: taken over after it
// expired, released, or not renewed in time. The writer must stop writing; holding the tenant again
// is a new AcquireWriter.
var ErrWriterLost = errs.Sentinel(errs.ErrPrecondition, "pgstore: the tenant's writer lock was lost")

// LeaseTable is the table a tenant's writer lease is a row of (000007), in the store's schema.
const LeaseTable = "rag_writer_lease"

// LeaseScope is tenant's row in LeaseTable.
func LeaseScope(tenant string) string { return "tenant:" + tenant }

// Writer is a tenant's writer lease. Its methods are safe for concurrent use: the ingester
// renews it on a ticker of its own while its writes, and the embedding worker's, fence on it.
type Writer struct{ l *pglease.Lease }

// AcquireWriter takes tenant's writer lease on conn, the store's pool, trying every poll while
// another writer holds it, until ctx ends (ctx's error then). ttl is the lease's length
// (pglease.DefaultTTL when 0); its holder renews it every ttl/3 (Renew). waiting is called once,
// with the holder, when the lease is first found held.
func AcquireWriter(ctx context.Context, conn dao.DataConn, tenant string, ttl, poll time.Duration, waiting func(holder pglease.Holder)) (*Writer, error) {
	l, err := pglease.Acquire(ctx, conn, pglease.Options{Table: LeaseTable, Scope: LeaseScope(tenant), TTL: ttl, Info: "tenant=" + tenant}, poll, waiting)
	if err != nil {
		if err := Ended(ctx); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("pgstore: take the writer lease: %w", err)
	}
	return &Writer{l: l}, nil
}

// lost is err as the writer's: ErrWriterLost when the lease is gone, err itself otherwise.
func lost(err error) error {
	if errors.Is(err, pglease.ErrLost) {
		return fmt.Errorf("%w: %w", ErrWriterLost, err)
	}
	return err
}

// Check is the lease as the writer knows it, with no I/O: ErrWriterLost once it was lost or went
// unrenewed too long (pglease.Lease.Check), nil otherwise.
func (w *Writer) Check() error { return lost(w.l.Check()) }

// Renew extends the lease: ErrWriterLost when it was taken over or released; any other failure
// as it is, the lease then left to Check's silence rule.
func (w *Writer) Renew(ctx context.Context) error { return lost(w.l.Renew(ctx)) }

// Fence confirms the lease inside the write transaction q, as its first statement, and holds it
// against a takeover until q ends: the Store.Fenced fence of the writer's Store.
func (w *Writer) Fence(ctx context.Context, q dao.Querier) error { return lost(w.l.Fence(ctx, q)) }

// Release lets the lease go, so a standby takes the tenant at its next poll. It runs on a fresh
// context, bounded: the writer's own ended as it stopped, and a statement cancelled mid-flight
// would leave the lease to expire instead.
func (w *Writer) Release() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = w.l.Release(ctx)
}

// Holder is the lease's holder id, as rag_writer_lease's holder column says it.
func (w *Writer) Holder() string { return w.l.ID() }

// RenewEvery is how often a holder of a lease of ttl renews it: a third of it.
func RenewEvery(ttl time.Duration) time.Duration {
	if ttl == 0 {
		ttl = pglease.DefaultTTL
	}
	return ttl / 3
}

// Ended is ctx's error once it has ended, counting a deadline that has passed though ctx's own
// timer has not yet fired. The driver bounds a write by the socket's deadline, set to ctx's, and the
// runtime can fail that write with an i/o timeout a moment before the timer marks ctx done: in that
// window ctx.Err() is still nil, and the caller's deadline would read as a broken connection.
func Ended(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) {
		return context.DeadlineExceeded
	}
	return nil
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
