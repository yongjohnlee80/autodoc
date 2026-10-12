package ingest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// The defaults of Options' durations.
const (
	DefaultRefresh   = 24 * time.Hour
	DefaultInterval  = time.Minute
	DefaultLag       = time.Minute
	DefaultPingEvery = 10 * time.Second
	DefaultLockPoll  = 5 * time.Second
)

// RetryLimit is how many quarantined rows a pass retries, the ones retried longest ago first: a
// row that fails again goes to the back, so a larger quarantine is worked through over passes.
const RetryLimit = 1000

// countCap bounds the quarantine count a run reports.
const countCap = 10000

var (
	// ErrOrder is a page whose rows break the view's contract: not in (updated_at, id) order, or
	// outside the cursor and upto_ts the page was asked for. Nothing of the page is written: a
	// cursor moved over misordered rows would skip some for good.
	ErrOrder = errs.Sentinel(errs.ErrPrecondition, "ingest: the view's rows are not in (updated_at, id) order within its arguments")
	// ErrStalled is a page with no row carrying both an id and an updated_at: the cursor cannot
	// move past it. Its rows are quarantined (those with an id); the view must give them their key.
	ErrStalled = errs.Sentinel(errs.ErrPrecondition, "ingest: a page with no row carrying both id and updated_at holds the cursor")
)

// Options configure an Ingester. A zero duration is its default.
type Options struct {
	// Schema is the schema the tenant's store lives in: with the tenant, it keys the writer lock,
	// so two servers on one schema and tenant never write at once. Required.
	Schema string
	// Refresh is how often Run reads every row again (DefaultRefresh).
	Refresh time.Duration
	// Interval is how often Run reads the rows past the cursor (DefaultInterval).
	Interval time.Duration
	// Lag is how far behind now a pass reads (DefaultLag): a business transaction open longer
	// than this could commit a row behind the cursor, which only the next refresh would read.
	// A negative Lag is none.
	Lag time.Duration
	// PingEvery is how often Run confirms it still holds the writer lock (DefaultPingEvery).
	PingEvery time.Duration
	// LockPoll is how often Acquire tries a lock another server holds (DefaultLockPoll).
	LockPoll time.Duration
	// Log receives each run's summary and every failure. nil: none.
	Log logger.Logger
	// Now is the time a pass's upper bound is measured back from. nil: the source database's
	// clock (SELECT now()), the clock its updated_at values are written with.
	Now func(ctx context.Context) (time.Time, error)
	// Clock is the time a refresh records and Run schedules refreshes by. nil: time.Now.
	Clock clock.Clock
}

// Ingester indexes one source into its tenant: the only writer of the tenant while it holds the
// tenant's lock. Pass reads the rows changed since the cursor, Refresh reads every row and deletes
// the documents of rows gone, and Run does both on their schedules.
//
// Every write is a pgstore.Batch, one per page of the view: its documents, its quarantined and
// released rows and its cursor commit together, after the writer lock is confirmed. A crash
// replays at most the page it interrupted, and writing a page again changes nothing.
//
// An Ingester belongs to one goroutine; Run's own ping goroutine is the exception, and it only
// checks the lock.
type Ingester struct {
	st       *pgstore.Store
	tenant   string
	src      *Source
	business dao.DataConn
	locks    dao.DataConn
	o        Options
	lock     *tenantLock
	// beforeCommit, when set, runs before each commit and fails it with its error: a test's way
	// to stop the process between a page's writes and its commit, as a crash would.
	beforeCommit func() error
}

// New is an ingester of src into st, the store of tenant. business is the database the view
// reads, on the role's own search_path; locks is a pool of its own for the writer lock, which
// pins one of its connections for as long as the lock is held.
func New(st *pgstore.Store, tenant string, src *Source, business, locks dao.DataConn, o Options) *Ingester {
	for _, d := range []struct {
		v   *time.Duration
		def time.Duration
	}{{&o.Refresh, DefaultRefresh}, {&o.Interval, DefaultInterval}, {&o.Lag, DefaultLag},
		{&o.PingEvery, DefaultPingEvery}, {&o.LockPoll, DefaultLockPoll}} {
		if *d.v == 0 {
			*d.v = d.def
		}
	}
	if o.Lag < 0 {
		o.Lag = 0
	}
	if o.Log == nil {
		o.Log = logger.Adapt(func(logger.Severity, any) {})
	}
	if o.Now == nil {
		o.Now = func(ctx context.Context) (time.Time, error) { return dbNow(ctx, business) }
	}
	return &Ingester{st: st, tenant: tenant, src: src, business: business, locks: locks, o: o}
}

// Tenant is the tenant the ingester writes.
func (in *Ingester) Tenant() string { return in.tenant }

// Acquire takes the tenant's writer lock, waiting while another server holds it, until ctx ends.
func (in *Ingester) Acquire(ctx context.Context) error {
	if in.lock != nil {
		return nil
	}
	l, err := acquire(ctx, in.locks, lockKey(in.o.Schema, in.tenant), in.o.LockPoll, func(holder int64) {
		logger.Notice(in.o.Log, fmt.Sprintf("tenant %s: waiting for its writer lock, held by backend %d (another server)", in.tenant, holder))
	})
	if err != nil {
		return err
	}
	in.lock = l
	return nil
}

// Release lets go of the writer lock.
func (in *Ingester) Release() {
	if in.lock != nil {
		in.lock.release()
		in.lock = nil
	}
}

// Stats are what one Pass or Refresh did.
type Stats struct {
	Pages, Read int
	Written     int // documents written
	Unchanged   int // refresh: rows whose document was already current
	Quarantined int // rows that failed and wait in quarantine
	Released    int // quarantined rows that ingested, or that the source no longer returns
	Deleted     int // refresh: documents of rows the source no longer returns
	// Unidentified are rows with no id: they can be neither indexed nor quarantined, and a refresh
	// that read one deletes nothing.
	Unidentified int
	// Complete is a refresh that read every row up to its bound, so it could delete.
	Complete bool
	// InQuarantine is how many rows wait in quarantine after the run (at most countCap).
	InQuarantine int
}

// Pass reads the rows past the tenant's cursor up to now − Lag, after retrying the quarantined
// rows. Each page of the view is one batch with the cursor moved to its last row, so rows sharing
// an updated_at across a page boundary are never skipped, and a failed row is quarantined, not
// passed for good.
//
// The quarantined rows are retried by reading the view from its start until each has been seen:
// the view's fixed arguments select by position, not by id. A row seen renders into its document
// and leaves quarantine, or fails again and stays. When the scan reaches the end, the rows it never
// saw are gone from the source and leave quarantine too.
//
// NOTE: the retry scan costs up to a full read of the view on every pass while anything is in
// quarantine, stopping at the last quarantined row it finds. A row that keeps failing keeps
// costing that; the log reports the quarantine on every pass so it is fixed.
func (in *Ingester) Pass(ctx context.Context) (Stats, error) {
	var st Stats
	if in.lock == nil {
		return st, ErrNotLocked
	}
	upto, err := in.upto(ctx)
	if err != nil {
		return st, err
	}
	if err := in.retry(ctx, upto, &st); err != nil {
		return st, err
	}
	cur, err := in.st.Cursor(ctx)
	if err != nil {
		return st, err
	}
	for {
		rows, err := readPage(ctx, in.src, in.business, cur, upto)
		if err != nil {
			return st, err
		}
		if len(rows) == 0 {
			break
		}
		st.Pages++
		st.Read += len(rows)
		b := in.st.Begin(ctx)
		next := cur
		for _, r := range rows {
			if r.id == "" {
				st.Unidentified++
				continue
			}
			if _, err := in.apply(ctx, b, r, &st, nil); err != nil {
				_ = b.Rollback()
				return st, err
			}
			if r.keyed() {
				next = r.key()
			}
		}
		moved := !sameKey(next, cur)
		if moved {
			if err := b.SetCursor(ctx, next); err != nil {
				_ = b.Rollback()
				return st, err
			}
		}
		if err := in.commit(ctx, b); err != nil {
			return st, err
		}
		if !moved {
			return st, ErrStalled
		}
		cur = next
	}
	in.count(ctx, &st)
	return st, nil
}

// retry reads the view from its start for the quarantined rows, as Pass says.
func (in *Ingester) retry(ctx context.Context, upto time.Time, st *Stats) error {
	held, err := in.st.Quarantined(ctx, RetryLimit)
	if err != nil || len(held) == 0 {
		return err
	}
	want := make(map[string]bool, len(held))
	for _, e := range held {
		want[e.SourceID] = true
	}
	var cur pgstore.Cursor
	complete := false
	for len(want) > 0 {
		rows, err := readPage(ctx, in.src, in.business, cur, upto)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			complete = true
			break
		}
		b := in.st.Begin(ctx)
		next := cur
		for _, r := range rows {
			if r.keyed() {
				next = r.key()
			}
			if r.id == "" || !want[r.id] {
				continue
			}
			delete(want, r.id)
			written, err := in.apply(ctx, b, r, st, nil)
			if err != nil {
				_ = b.Rollback()
				return err
			}
			if written {
				st.Released++
			}
		}
		if err := in.commit(ctx, b); err != nil {
			return err
		}
		if sameKey(next, cur) {
			break // keyless rows hold the scan: it is not complete
		}
		cur = next
	}
	if !complete || len(want) == 0 {
		return nil
	}
	b := in.st.Begin(ctx)
	for _, id := range sortedKeys(want) {
		if err := b.Release(ctx, id); err != nil {
			_ = b.Rollback()
			return err
		}
		st.Released++
	}
	return in.commit(ctx, b)
}

// Refresh reads every row of the view up to now − Lag. A row whose document is current (the same
// version and indexer) is left as it is; one whose content changed, though its updated_at did not,
// is written again; one that fails is quarantined and its document kept. Every row read with an id
// counts as seen, whether or not its body renders.
//
// Only when the scan read every row, with no error and no row lacking an id, are the documents of
// rows not seen deleted, the quarantined rows not seen released, and the refresh recorded; the
// cursor then moves up to the scan's last row if it was behind it. A scan that failed or stopped
// deletes nothing.
func (in *Ingester) Refresh(ctx context.Context) (Stats, error) {
	st := Stats{Complete: true}
	if in.lock == nil {
		return st, ErrNotLocked
	}
	started := in.o.Clock.Now()
	upto, err := in.upto(ctx)
	if err != nil {
		return st, err
	}
	stored, err := in.st.Cursor(ctx)
	if err != nil {
		return st, err
	}
	current, err := in.docs(ctx)
	if err != nil {
		return st, err
	}
	held, err := in.st.Quarantined(ctx, math.MaxInt32)
	if err != nil {
		return st, err
	}
	quarantined := make(map[string]bool, len(held))
	for _, e := range held {
		quarantined[e.SourceID] = true
	}
	seen := map[string]bool{}
	seenIDs := map[string]bool{}
	var cur pgstore.Cursor
	for {
		rows, err := readPage(ctx, in.src, in.business, cur, upto)
		if err != nil {
			st.Complete = false
			return st, err
		}
		if len(rows) == 0 {
			break
		}
		st.Pages++
		st.Read += len(rows)
		b := in.st.Begin(ctx)
		next := cur
		for _, r := range rows {
			if r.id == "" {
				st.Unidentified++
				st.Complete = false
				continue
			}
			seenIDs[r.id] = true
			if path, err := in.src.Path(r.id); err == nil {
				seen[path] = true
			}
			written, err := in.apply(ctx, b, r, &st, current)
			if err == nil && !written && quarantined[r.id] {
				// current, though it failed before: the cause was fixed without changing it
				if err = b.Release(ctx, r.id); err == nil {
					st.Released++
				}
			}
			if err != nil {
				_ = b.Rollback()
				st.Complete = false
				return st, err
			}
			if r.keyed() {
				next = r.key()
			}
		}
		if err := in.commit(ctx, b); err != nil {
			st.Complete = false
			return st, err
		}
		if sameKey(next, cur) {
			st.Complete = false
			return st, ErrStalled
		}
		cur = next
	}
	if !st.Complete {
		logger.Warning(in.o.Log, nil, fmt.Sprintf("tenant %s: refresh read %d rows with no id: it deletes nothing until the view gives every row one", in.tenant, st.Unidentified))
		in.count(ctx, &st)
		return st, nil
	}
	if err := in.finish(ctx, &st, current, seen, quarantined, seenIDs, stored, cur, started); err != nil {
		st.Complete = false
		return st, err
	}
	in.count(ctx, &st)
	return st, nil
}

// deleteBatch bounds how many documents one transaction of a refresh deletes.
const deleteBatch = 500

// finish ends a complete refresh: the documents of rows not seen are deleted, the quarantined rows
// not seen released, the refresh recorded and the cursor moved up to the scan's end when it was
// behind. The record and the cursor commit with the last deletions.
func (in *Ingester) finish(ctx context.Context, st *Stats, current map[string]pgstore.DocInfo, seen, quarantined, seenIDs map[string]bool,
	stored, last pgstore.Cursor, started time.Time) error {
	b := in.st.Begin(ctx)
	n := 0
	for _, path := range sortedKeys(current) {
		if seen[path] {
			continue
		}
		if err := b.Delete(ctx, path); err != nil {
			_ = b.Rollback()
			return err
		}
		st.Deleted++
		if n++; n%deleteBatch == 0 {
			if err := in.commit(ctx, b); err != nil {
				return err
			}
			b = in.st.Begin(ctx)
		}
	}
	for _, id := range sortedKeys(quarantined) {
		if seenIDs[id] {
			continue
		}
		if err := b.Release(ctx, id); err != nil {
			_ = b.Rollback()
			return err
		}
		st.Released++
	}
	if err := b.SetRefreshed(ctx, pgstore.Refresh{Started: started, Finished: in.o.Clock.Now()}); err != nil {
		_ = b.Rollback()
		return err
	}
	// The scan read every row up to last, so the cursor may stand there. Rows sharing last's
	// updated_at are ordered by the database's comparison of ids, which this process cannot make,
	// so the cursor moves only to a later updated_at.
	if !last.IsZero() && (stored.IsZero() || last.UpdatedAt.After(stored.UpdatedAt)) {
		if err := b.SetCursor(ctx, last); err != nil {
			_ = b.Rollback()
			return err
		}
	}
	return in.commit(ctx, b)
}

// apply writes row r into b: its document, out of quarantine; or, when it fails, into quarantine.
// With current (a refresh), a document already current is left as it is. written says a document
// was written.
func (in *Ingester) apply(ctx context.Context, b *pgstore.Batch, r row, st *Stats, current map[string]pgstore.DocInfo) (written bool, err error) {
	path, err := in.src.Path(r.id)
	var d pgstore.Document
	if err == nil {
		d, err = in.src.document(r, path)
	}
	if err != nil {
		st.Quarantined++
		return false, b.Quarantine(ctx, r.id, err)
	}
	if info, ok := current[path]; ok && info.Version == d.Version && info.Indexer == d.Indexer {
		st.Unchanged++
		return false, nil
	}
	if _, err := b.Put(ctx, d); err != nil {
		return false, err
	}
	st.Written++
	return true, b.Release(ctx, r.id)
}

// commit commits b once the writer lock is confirmed held, and rolls it back otherwise: a server
// that lost its lock writes nothing more.
func (in *Ingester) commit(ctx context.Context, b *pgstore.Batch) error {
	if in.beforeCommit != nil {
		if err := in.beforeCommit(); err != nil {
			_ = b.Rollback()
			return err
		}
	}
	if err := in.lock.check(ctx); err != nil {
		_ = b.Rollback()
		return err
	}
	return b.Commit()
}

// upto is a pass's upper bound: now − Lag.
//
// NOTE: Lag is a deadline the code can name, not a wait: a row committed by a transaction that
// began up to Lag ago may carry an updated_at behind rows already committed, so the cursor stays
// Lag behind the database's clock and never passes a timestamp such a transaction could still
// commit. A transaction open longer than Lag is caught by the next refresh.
func (in *Ingester) upto(ctx context.Context) (time.Time, error) {
	now, err := in.o.Now(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("ingest: the source database's time: %w", err)
	}
	return now.Add(-in.o.Lag), nil
}

// docs are every document of the tenant, by path.
func (in *Ingester) docs(ctx context.Context) (map[string]pgstore.DocInfo, error) {
	out := map[string]pgstore.DocInfo{}
	after := ""
	for {
		page, more, err := in.st.Docs(ctx, after, 1000)
		if err != nil {
			return nil, err
		}
		for _, d := range page {
			out[d.Path] = d
			after = d.Path
		}
		if !more {
			return out, nil
		}
	}
}

// count records how many rows wait in quarantine.
func (in *Ingester) count(ctx context.Context, st *Stats) {
	if held, err := in.st.Quarantined(ctx, countCap); err == nil {
		st.InQuarantine = len(held)
	}
}

// Run holds the tenant's writer lock and indexes on schedule until ctx ends: a refresh when the
// last one is older than Refresh (or there was none), else a pass, every Interval. A failed run is
// logged and tried again at the next tick. It confirms the lock every PingEvery; a lost lock stops
// it at once with ErrLockLost. It returns nil when ctx ends, with the lock released.
func (in *Ingester) Run(ctx context.Context) error {
	if err := in.Acquire(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer in.Release()
	ctx, cancel := context.WithCancelCause(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		in.ping(ctx, cancel)
	}()
	err := in.loop(ctx)
	cancel(nil)
	wg.Wait()
	if cause := context.Cause(ctx); errors.Is(cause, ErrLockLost) {
		return cause
	}
	if errors.Is(err, ErrLockLost) {
		return err
	}
	return nil
}

// ping confirms the lock every PingEvery, and cancels the run with the loss.
func (in *Ingester) ping(ctx context.Context, cancel context.CancelCauseFunc) {
	// NOTE: polled, because nothing can announce it: a session killed, or a connection dropped by
	// the network, tells no one until the connection is next used. A ping every PingEvery bounds
	// how long a server that lost its lock goes on believing it holds it; every commit confirms
	// the lock as well, so no write lands in that window.
	t := time.NewTicker(in.o.PingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := in.lock.check(ctx); errors.Is(err, ErrLockLost) {
				logger.Error(in.o.Log, err, "tenant "+in.tenant+": stopping")
				cancel(err)
				return
			}
		}
	}
}

func (in *Ingester) loop(ctx context.Context) error {
	refreshed, err := in.st.Refreshed(ctx)
	if err != nil {
		logger.Warning(in.o.Log, err, "tenant "+in.tenant+": reading its last refresh")
	}
	last := refreshed.Finished
	for {
		kind := "pass"
		var st Stats
		if last.IsZero() || in.o.Clock.Now().Sub(last) >= in.o.Refresh {
			kind = "refresh"
			st, err = in.Refresh(ctx)
			last = in.o.Clock.Now() // tried: the next one is a period away, whatever this one did
		} else {
			st, err = in.Pass(ctx)
		}
		switch {
		case errors.Is(err, ErrLockLost):
			return err
		case ctx.Err() != nil:
			return nil
		case err != nil:
			logger.Warning(in.o.Log, err, fmt.Sprintf("tenant %s: %s", in.tenant, kind))
		default:
			in.report(kind, st)
		}
		// NOTE: polled, because nothing can announce it: the business database tells no one when
		// its rows change (LISTEN/NOTIFY would need triggers in its schema), so the rows past the
		// cursor are read every Interval, as one batch per page; the refresh is due on this same
		// tick once Refresh has passed since the last one.
		if !clock.Sleep(ctx, in.o.Interval) {
			return nil
		}
	}
}

// report logs a run that did something, or that leaves rows in quarantine.
func (in *Ingester) report(kind string, st Stats) {
	if st.Written+st.Quarantined+st.Released+st.Deleted+st.Unidentified == 0 && st.InQuarantine == 0 {
		return
	}
	held := fmt.Sprint(st.InQuarantine)
	if st.InQuarantine >= countCap {
		held += "+"
	}
	msg := fmt.Sprintf("tenant %s: %s read %d rows: %d written, %d quarantined, %d released, %d deleted, %d without an id; %s in quarantine",
		in.tenant, kind, st.Read, st.Written, st.Quarantined, st.Released, st.Deleted, st.Unidentified, held)
	if st.InQuarantine > 0 || st.Unidentified > 0 {
		logger.Warning(in.o.Log, nil, msg)
		return
	}
	logger.Info(in.o.Log, msg)
}

// readPage reads one page of src's view on conn: the rows after the cursor after, up to upto, as
// the view's own LIMIT bounds them. On the tenant's first page (the zero cursor) after_id is NULL.
// The rows must come in (updated_at, id) order inside those bounds: a row before the cursor's
// updated_at, past upto, or earlier than the row before it is ErrOrder, and nothing of the page is
// used. (The order of ids within one updated_at is the database's to compare: CheckOrder checks it
// against a real source.)
func readPage(ctx context.Context, src *Source, conn dao.DataConn, after pgstore.Cursor, upto time.Time) ([]row, error) {
	var afterID any
	if !after.IsZero() {
		afterID = after.ID
	}
	rows, err := src.v.WithArgs(after.UpdatedAt, afterID, upto).Query(ctx, conn)
	if err != nil {
		return nil, err
	}
	var out []row
	prev := after.UpdatedAt
	for x, err := range rows.All() {
		if err != nil {
			return nil, err
		}
		r := decode(x)
		if r.keyed() {
			switch {
			case r.at.After(upto):
				return nil, fmt.Errorf("%w: row %s's updated_at %s is past upto_ts %s", ErrOrder, r.id, stamp(r.at), stamp(upto))
			case r.at.Before(prev):
				return nil, fmt.Errorf("%w: row %s's updated_at %s comes after %s", ErrOrder, r.id, stamp(r.at), stamp(prev))
			}
			prev = r.at
		}
		out = append(out, r)
	}
	return out, nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// sameKey reports whether two cursors are one position.
func sameKey(a, b pgstore.Cursor) bool { return a.UpdatedAt.Equal(b.UpdatedAt) && a.ID == b.ID }

// dbNow is the database's clock.
func dbNow(ctx context.Context, conn dao.DataConn) (time.Time, error) {
	rows, err := conn.QueryContext(ctx, "SELECT now()")
	if err != nil {
		return time.Time{}, err
	}
	var t time.Time
	err = scanRows(rows, &t)
	return t, err
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
