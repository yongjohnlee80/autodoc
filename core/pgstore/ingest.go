package pgstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
)

// The enterprise search server's ingest state (ADR 1791762085 §4.3, §4.6): a tenant's cursor into
// its source, the source rows that failed and wait in quarantine, and the tenant's access policy.
// The cursor and the quarantine are written through a Batch, in the same transaction as the
// documents of the page that moved them; the policy is the server's, written at its start.

// Cursor is a tenant's position in its source: the (updated_at, id) of the last row a committed
// page passed, compared lexicographically, so rows sharing a timestamp across a page boundary are
// never skipped. The zero Cursor is the source's start.
type Cursor struct {
	UpdatedAt time.Time
	ID        string
}

// IsZero says no page has moved the cursor.
func (c Cursor) IsZero() bool { return c.UpdatedAt.IsZero() && c.ID == "" }

// Refresh is a tenant's last full refresh: when it started and when it finished. The zero Refresh
// is none recorded.
type Refresh struct {
	Started, Finished time.Time
}

// IngestError is a quarantined source row: its id in the source, why it failed last, when it first
// failed and when it last did.
type IngestError struct {
	SourceID, Err       string
	FirstSeen, LastSeen time.Time
}

// Policy is a tenant's access policy: the document facet fields every scoped read must be given
// values for (ViewWithin), and the view the tenant is indexed from.
type Policy struct {
	AccessFields []string
	View         string
}

var (
	// ErrNoPolicy is a tenant with no access policy recorded.
	ErrNoPolicy = errs.Sentinel(errs.ErrPrecondition, "pgstore: the tenant has no access policy")
	// ErrNoAccess is an access policy or scope with no field, or a field with no name or no values,
	// or a name a facet cannot hold: an empty scope would be no scope at all.
	ErrNoAccess = errs.Sentinel(errs.ErrInvalidArgument, "pgstore: no access scope")
)

// clock is the time a quarantine records (a variable, so a cell can step it).
var clock = time.Now

// SetCursor moves the tenant's cursor to c inside the batch: it commits with the page's documents,
// or not at all, so a crash replays at most that page. Its time is kept to the microsecond, as
// PostgreSQL keeps a timestamptz, and the driver rounds it down (TestACursorCommitsWithItsPage pins
// that): a cursor never passes a row it did not read, and a replayed row is written again,
// idempotently. The last refresh's times are left as they are.
func (b *Batch) SetCursor(ctx context.Context, c Cursor) error {
	at := c.UpdatedAt
	err := b.s.rw.cursor.On(b.tx, dao.WithQueryContext(ctx)).
		Set(kTenant, b.s.tenant).Set(kUpdated, &at).Set(kID, c.ID).Upsert()
	if err != nil {
		return fmt.Errorf("pgstore: set cursor: %w", err)
	}
	return nil
}

// SetRefreshed records the tenant's last full refresh inside the batch, leaving the cursor as it
// is.
func (b *Batch) SetRefreshed(ctx context.Context, r Refresh) error {
	err := b.s.rw.cursor.On(b.tx, dao.WithQueryContext(ctx)).
		Set(kTenant, b.s.tenant).Set(kStarted, &r.Started).Set(kFinished, &r.Finished).Upsert()
	if err != nil {
		return fmt.Errorf("pgstore: set refreshed: %w", err)
	}
	return nil
}

// Quarantine records that the source row sourceID failed with cause, inside the batch. A row
// already quarantined keeps when it first failed; its error and when it last failed are this
// one's.
func (b *Batch) Quarantine(ctx context.Context, sourceID string, cause error) error {
	if sourceID == "" || cause == nil {
		return errs.Wrap(errs.ErrInvalidArgument, "pgstore: a quarantine needs a source id and its error")
	}
	now := clock()
	d := b.s.rw.ingErr.On(b.tx, dao.WithQueryContext(ctx)).Set(qTenant, b.s.tenant).Set(qSource, sourceID).
		Set(qError, cause.Error()).Set(qFirst, now).Set(qLast, now)
	if err := dao.UpsertOnly(d, qError, qLast); err != nil {
		return fmt.Errorf("pgstore: quarantine %s: %w", sourceID, err)
	}
	return nil
}

// Release takes the source row sourceID out of quarantine inside the batch: it ingested, or the
// source no longer returns it. A row not quarantined is no error.
func (b *Batch) Release(ctx context.Context, sourceID string) error {
	err := b.s.rw.ingErr.On(b.tx, dao.WithQueryContext(ctx)).With(qTenant, b.s.tenant).With(qSource, sourceID).Delete()
	if err != nil {
		return fmt.Errorf("pgstore: release %s: %w", sourceID, err)
	}
	return nil
}

// Cursor is the tenant's committed cursor; the zero Cursor when no page has moved it.
func (s *Store) Cursor(ctx context.Context) (Cursor, error) {
	r, err := s.rw.cursor.DAO(dao.WithQueryContext(ctx)).With(kTenant, s.tenant).Get(kUpdated, kID)
	switch {
	case errors.Is(err, dao.ErrNoRows):
		return Cursor{}, nil
	case err != nil:
		return Cursor{}, fmt.Errorf("pgstore: cursor: %w", err)
	}
	c := Cursor{ID: r.ID}
	if r.UpdatedAt != nil {
		c.UpdatedAt = *r.UpdatedAt
	}
	return c, nil
}

// Refreshed is the tenant's last recorded full refresh; the zero Refresh when none is.
func (s *Store) Refreshed(ctx context.Context) (Refresh, error) {
	r, err := s.rw.cursor.DAO(dao.WithQueryContext(ctx)).With(kTenant, s.tenant).Get(kStarted, kFinished)
	switch {
	case errors.Is(err, dao.ErrNoRows):
		return Refresh{}, nil
	case err != nil:
		return Refresh{}, fmt.Errorf("pgstore: refreshed: %w", err)
	}
	var out Refresh
	if r.RefreshStarted != nil {
		out.Started = *r.RefreshStarted
	}
	if r.RefreshFinished != nil {
		out.Finished = *r.RefreshFinished
	}
	return out, nil
}

// Quarantined is up to limit of the tenant's quarantined rows, the one retried longest ago first
// (last seen, then source id): a pass that retries a page of them and quarantines the failures
// again moves through all of them in turn.
func (s *Store) Quarantined(ctx context.Context, limit int) ([]IngestError, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.rw.ingErr.DAO(dao.WithQueryContext(ctx)).With(qTenant, s.tenant).
		OrderBy(dao.Asc(qByLast), dao.Asc(qBySource)).Limit(uint64(limit)).Select(qSource, qError, qFirst, qLast)
	if err != nil {
		return nil, fmt.Errorf("pgstore: quarantined: %w", err)
	}
	out := make([]IngestError, len(rows))
	for i, r := range rows {
		out[i] = IngestError{SourceID: r.SourceID, Err: r.Error, FirstSeen: r.FirstSeen, LastSeen: r.LastSeen}
	}
	return out, nil
}

// SetPolicy records the tenant's access policy, replacing any it had. A policy with no access
// field is refused (ErrNoAccess): there is no policy that turns access checking off.
func (s *Store) SetPolicy(ctx context.Context, p Policy) error {
	if err := checkFields(p.AccessFields); err != nil {
		return err
	}
	err := s.rw.policy.DAO(dao.WithQueryContext(ctx)).Set(pTenant, s.tenant).
		Set(pAccess, slices.Clone(p.AccessFields)).Set(pView, p.View).Upsert()
	if err != nil {
		return fmt.Errorf("pgstore: set policy: %w", err)
	}
	return nil
}

// Policy is the tenant's access policy: ErrNoPolicy when none is recorded.
func (s *Store) Policy(ctx context.Context) (Policy, error) {
	r, err := s.rw.policy.DAO(dao.WithQueryContext(ctx)).With(pTenant, s.tenant).Get(pAccess, pView)
	switch {
	case errors.Is(err, dao.ErrNoRows):
		return Policy{}, ErrNoPolicy
	case err != nil:
		return Policy{}, fmt.Errorf("pgstore: policy: %w", err)
	}
	return Policy{AccessFields: r.AccessFields, View: r.View}, nil
}

// checkFields refuses access fields no scope can be built on: none, an empty name, a repeat, or a
// name with '=' in it, which a stored field=value facet could not tell from its value.
func checkFields(fields []string) error {
	if len(fields) == 0 {
		return ErrNoAccess
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if err := checkField(f); err != nil {
			return err
		}
		if seen[f] {
			return fmt.Errorf("%w: the access field %q is named twice", ErrNoAccess, f)
		}
		seen[f] = true
	}
	return nil
}

func checkField(f string) error {
	if f == "" || strings.Contains(f, "=") {
		return fmt.Errorf("%w: %q is not an access field name", ErrNoAccess, f)
	}
	return nil
}
