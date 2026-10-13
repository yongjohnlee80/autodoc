package ingest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// The defaults of Options' durations.
const (
	DefaultRefresh      = 24 * time.Hour
	DefaultRefreshRetry = 15 * time.Minute
	DefaultInterval     = time.Minute
	DefaultLag          = time.Minute
	DefaultPingEvery    = 10 * time.Second
	DefaultLockPoll     = 5 * time.Second
)

// RetryLimit is how many quarantined rows a pass retries, the ones retried longest ago first: a
// row that fails again goes to the back, so a larger quarantine is worked through over passes.
const RetryLimit = 1000

// countCap bounds the quarantine count a run reports.
const countCap = 10000

var (
	// ErrLockLost is the tenant's writer lock gone from under its ingester: its connection closed
	// or was killed. The ingester stops writing at once (a page it was writing is rolled back) and
	// Run returns it, so the process exits non-zero and its supervisor restarts it to take the lock
	// again; it never goes on writing unlocked. It is pgstore.ErrWriterLost.
	ErrLockLost = pgstore.ErrWriterLost
	// ErrNotLocked is a write asked of an ingester that does not hold its tenant's lock.
	ErrNotLocked = errs.Sentinel(errs.ErrPrecondition, "ingest: the tenant's writer lock is not held")
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
	// RefreshRetry is how soon Run tries a refresh again after one that failed or could not read
	// every row (DefaultRefreshRetry, at most Refresh): only a complete refresh deletes the
	// documents of rows gone, so waiting a whole Refresh would keep them that long.
	RefreshRetry time.Duration
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
	// Committed, when set, is called after each batch commits: the embedding worker's wake, so the
	// chunks a page wrote are embedded without waiting for the worker's own poll. It runs on the
	// ingester's goroutine and must not block.
	Committed func()
	// Locked, when set, is called once the writer lock is taken: the tenant is this server's now,
	// and the embedding worker, which leaves a tenant alone until then, may begin on it without
	// waiting for its poll (a pass that finds nothing new commits nothing to wake it). Like
	// Committed, it runs on the ingester's goroutine and must not block.
	Locked func()
}

// Ingester indexes one source into its tenant: the only writer of the tenant while it holds the
// tenant's lock. Pass reads the rows changed since the cursor, Refresh reads every row and deletes
// the documents of rows gone, and Run does both on their schedules.
//
// Every write is a pgstore.Batch, one per page of the view: its documents and its quarantined and
// released rows commit together, after the writer lock is confirmed. A pass's page commits its
// cursor with them; a refresh moves the cursor only when it finishes, in the batch that records it.
// A crash replays at most the page it interrupted, and writing a page again changes nothing.
//
// An Ingester belongs to one goroutine; Run's own ping goroutine is the exception, and it only
// checks the lock, as Confirm does from any goroutine.
type Ingester struct {
	st       *pgstore.Store
	tenant   string
	src      *Source
	business dao.DataConn
	locks    dao.DataConn
	o        Options
	// lock is the writer lock while it is held, nil otherwise. It is atomic because Confirm reads
	// it from another goroutine (the embedding worker's) while this one takes and releases it.
	lock atomic.Pointer[pgstore.WriterLock]
	// state is the last pass's and refresh's outcome (State), and waiting whether Acquire waits for
	// another server's lock: an operator's status reads them from another goroutine
	state   atomic.Pointer[State]
	waiting atomic.Bool
}

// beforeCommit, when set, runs before each commit and fails it with its error: a test's way to
// stop the process between a page's writes and its commit, as a crash would. It is nil in
// production, and set only by a test, before the ingester runs.
var beforeCommit func() error

// New is an ingester of src into st, the store of tenant. business is the database the view
// reads, on the role's own search_path; locks is a pool of its own for the writer lock, which
// pins one of its connections for as long as the lock is held.
func New(st *pgstore.Store, tenant string, src *Source, business, locks dao.DataConn, o Options) *Ingester {
	for _, d := range []struct {
		v   *time.Duration
		def time.Duration
	}{{&o.Refresh, DefaultRefresh}, {&o.RefreshRetry, DefaultRefreshRetry}, {&o.Interval, DefaultInterval},
		{&o.Lag, DefaultLag}, {&o.PingEvery, DefaultPingEvery}, {&o.LockPoll, DefaultLockPoll}} {
		if *d.v == 0 {
			*d.v = d.def
		}
	}
	o.RefreshRetry = min(o.RefreshRetry, o.Refresh)
	if o.Lag < 0 {
		o.Lag = 0
	}
	if o.Log == nil {
		o.Log = logger.Adapt(func(logger.Severity, any) {})
	}
	if o.Now == nil {
		o.Now = func(ctx context.Context) (time.Time, error) { return pgstore.Now(ctx, business) }
	}
	return &Ingester{st: st, tenant: tenant, src: src, business: business, locks: locks, o: o}
}

// Tenant is the tenant the ingester writes.
func (in *Ingester) Tenant() string { return in.tenant }

// Acquire takes the tenant's writer lock, waiting while another server holds it, until ctx ends.
func (in *Ingester) Acquire(ctx context.Context) error {
	if in.lock.Load() != nil {
		return nil
	}
	defer in.waiting.Store(false)
	l, err := pgstore.AcquireWriter(ctx, in.locks, in.o.Schema, in.tenant, in.o.LockPoll, func(holder int64) {
		in.waiting.Store(true)
		logger.Notice(in.o.Log, fmt.Sprintf("tenant %s: waiting for its writer lock, held by backend %d (another server)", in.tenant, holder))
	})
	if err != nil {
		return err
	}
	in.lock.Store(l)
	if in.o.Locked != nil {
		in.o.Locked()
	}
	return nil
}

// Release lets go of the writer lock.
func (in *Ingester) Release() {
	if l := in.lock.Swap(nil); l != nil {
		l.Release()
	}
}

// Confirm reports whether the ingester holds its tenant's writer lock now: nil when it does and
// the lock answers as this session's; ErrNotLocked while it waits for the lock (another server
// holds it) or after it let go; ErrLockLost when the lock was lost. It is for another writer of
// the tenant in the same process, the embedding worker, which writes only while the tenant is
// this server's, and confirms it before each of its writes as the ingester does before each
// commit. Unlike the ingester's other methods it may be called from any goroutine.
func (in *Ingester) Confirm(ctx context.Context) error {
	l := in.lock.Load()
	if l == nil {
		return ErrNotLocked
	}
	return l.Check(ctx)
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
	if in.lock.Load() == nil {
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
// The scan then goes on past now − Lag to the view's end, only to count the rows there as seen:
// a row updated within the lag is still in the source, though the bounded scan cannot reach it.
// The next pass indexes it once it is older than the lag. Both scans read one read-only snapshot of
// the source, so every row is read exactly once, in one place: a row updated while the scan runs
// is seen as it was when the scan began, whatever updated_at the update gives it. Read page by page
// without one, a row moved behind the scan's position by an update (a transaction that began
// before the scan got there, or an updated_at that went back) would be read nowhere, and its
// document deleted.
//
// Only when the scan read every row, with no error and no row lacking an id, are the documents of
// rows not seen deleted, the quarantined rows not seen released, and the refresh recorded; the
// cursor then moves up to the last row read before now − Lag if it was behind it. A scan that
// failed or stopped deletes nothing.
//
// NOTE: a refresh holds every document's path, version and indexer, and every quarantined id, in
// memory for the length of its scan: O(documents), some hundred bytes each. A row's document is
// then compared without a query per row, and the paths not seen are known when the scan ends.
//
// NOTE: the snapshot is a read-only REPEATABLE READ transaction on the source, open for the
// length of the refresh: it holds back the source's vacuum of rows updated meanwhile, as any
// long read does, once per Refresh period.
func (in *Ingester) Refresh(ctx context.Context) (Stats, error) {
	var st Stats
	if in.lock.Load() == nil {
		return st, ErrNotLocked
	}
	st.Complete = true
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
	tx, err := dao.BeginConnTx(ctx, in.business, dao.TxOptions{Access: dao.TxReadOnly, Isolation: dao.TxRepeatableRead})
	if err != nil {
		st.Complete = false
		return st, err
	}
	defer func() { _ = tx.Rollback() }()
	source := snapshot{TxConn: tx, base: in.business}
	seen := map[string]bool{}
	seenIDs := map[string]bool{}
	var cur pgstore.Cursor
	for {
		rows, err := readPage(ctx, in.src, source, cur, upto)
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
			in.see(r, seen, seenIDs)
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
	if st.Complete {
		if err := in.present(ctx, source, cur, seen, seenIDs, &st); err != nil {
			st.Complete = false
			return st, err
		}
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

// beyond is the upper bound a refresh's presence scan reads to: later than any updated_at a source
// holds, so the view returns every row after the cursor.
var beyond = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// present reads the rows after cur on source, past the refresh's bound, and counts each as seen
// without indexing it: rows updated within the lag are still in the source, and their documents and
// quarantine entries must outlive the refresh.
func (in *Ingester) present(ctx context.Context, source dao.DataConn, cur pgstore.Cursor, seen, seenIDs map[string]bool, st *Stats) error {
	for {
		rows, err := readPage(ctx, in.src, source, cur, beyond)
		if err != nil || len(rows) == 0 {
			return err
		}
		st.Pages++
		st.Read += len(rows)
		next := cur
		for _, r := range rows {
			if r.id == "" {
				st.Unidentified++
				st.Complete = false
				continue
			}
			in.see(r, seen, seenIDs)
			if r.keyed() {
				next = r.key()
			}
		}
		if sameKey(next, cur) {
			return ErrStalled
		}
		cur = next
	}
}

// see counts row r as present in the source: its id, and its document's path when it has one.
func (in *Ingester) see(r row, seen, seenIDs map[string]bool) {
	seenIDs[r.id] = true
	if path, err := in.src.Path(r.id); err == nil {
		seen[path] = true
	}
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
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			_ = b.Rollback()
			return err
		}
	}
	if err := in.lock.Load().Check(ctx); err != nil {
		_ = b.Rollback()
		return err
	}
	if err := b.Commit(); err != nil {
		return err
	}
	if in.o.Committed != nil {
		in.o.Committed()
	}
	return nil
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

// viewError is a view's query failing on the business database, as an operator's status tells it
// from the index's own database (opcode.View).
type viewError struct{ error }

func (e viewError) Unwrap() error { return e.error }

// Outcome is how a pass or a refresh ended: when, what it did, and its failure's code.
type Outcome struct {
	At    time.Time // the zero time: none yet
	Stats Stats
	Error opcode.Code
}

// State is what an ingester shows an operator (enterprise/ops): whether it holds its tenant's
// writer lock or waits for another server's, and how its last pass and refresh ended. It is
// published whole after each change, and safe to read from any goroutine.
type State struct {
	Locked, Waiting       bool
	LastPass, LastRefresh Outcome
}

// State is the ingester's state now.
func (in *Ingester) State() State {
	st := State{}
	if p := in.state.Load(); p != nil {
		st = *p
	}
	st.Locked = in.lock.Load() != nil
	st.Waiting = !st.Locked && in.waiting.Load()
	return st
}

// record publishes how a run ended, as its kind's outcome.
func (in *Ingester) record(kind string, st Stats, err error) {
	next := State{}
	if p := in.state.Load(); p != nil {
		next = *p
	}
	o := Outcome{At: in.o.Clock.Now(), Stats: st, Error: codeOf(err)}
	if kind == "refresh" {
		next.LastRefresh = o
	} else {
		next.LastPass = o
	}
	in.state.Store(&next)
}

// codeOf is err's code for an operator: the lock, the view's order, the view's query, or the
// index's database, which every other failure of a run is.
func codeOf(err error) opcode.Code {
	var ve viewError
	switch {
	case err == nil:
		return opcode.None
	case errors.Is(err, ErrLockLost):
		return opcode.LockLost
	case errors.Is(err, ErrOrder), errors.Is(err, ErrStalled):
		return opcode.Order
	case errors.As(err, &ve):
		return opcode.View
	}
	return opcode.Database
}

// count records how many rows wait in quarantine.
func (in *Ingester) count(ctx context.Context, st *Stats) {
	if held, err := in.st.Quarantined(ctx, countCap); err == nil {
		st.InQuarantine = len(held)
	}
}

// Run holds the tenant's writer lock and indexes on schedule until ctx ends: a refresh when the
// last complete one is older than Refresh (or there was none), else a pass, every Interval. A failed
// pass is logged and tried again at the next tick; a refresh that failed or was not complete is
// logged and tried again RefreshRetry later. It confirms the lock every PingEvery; a lost lock stops
// it at once with ErrLockLost. It returns nil when ctx ends, with the lock released.
func (in *Ingester) Run(ctx context.Context) error {
	if err := in.Acquire(ctx); err != nil {
		// a wait ended by ctx is a clean stop: ctx's error, or its deadline passed, which Acquire
		// answers as soon as it has, a moment before ctx's own timer may mark it done. Any other
		// failure is one, though it wrap a deadline of its own (a connection's connect_timeout)
		if pgstore.Ended(ctx) != nil {
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
			if err := in.lock.Load().Check(ctx); errors.Is(err, ErrLockLost) {
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
	var due time.Time // the next refresh's time; zero: at once
	if !refreshed.Finished.IsZero() {
		due = refreshed.Finished.Add(in.o.Refresh)
	}
	for {
		kind := "pass"
		var st Stats
		if !in.o.Clock.Now().Before(due) {
			kind = "refresh"
			st, err = in.Refresh(ctx)
			if err == nil && st.Complete {
				due = in.o.Clock.Now().Add(in.o.Refresh)
			} else {
				// it deleted nothing, and no pass can: the rows gone keep their documents until a
				// refresh completes, so the next is tried soon rather than a whole period away
				due = in.o.Clock.Now().Add(in.o.RefreshRetry)
			}
		} else {
			st, err = in.Pass(ctx)
		}
		if ctx.Err() == nil {
			in.record(kind, st, err)
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
		// tick once Refresh has passed since the last complete one, or RefreshRetry since one that
		// failed.
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
		return nil, viewError{err}
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
