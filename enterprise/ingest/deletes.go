package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/view"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// The fields every row of a deletes view carries.
const (
	FieldDeletedAt = "deleted_at" // when the source row was deleted, on the source database's clock
)

// Deletes is a source's deletes view, loaded and checked: the tombstones of its rows deleted
// outright. It takes the main view's Args (after_ts, after_id, upto_ts) and returns rows of
// {id, deleted_at} past the cursor, deleted_at at most upto_ts, ordered by (deleted_at, id). Every
// pass reads it past its own cursor, after the rows. A Deletes is immutable and safe for
// concurrent use.
type Deletes struct {
	path string
	v    *view.View
}

// LoadDeletes reads the deletes view at path and checks it can be read page by page past a cursor
// (its source is postgres, its args are exactly Args, its query uses $1, $2 and $3 and no other).
// A file that breaks a rule is a *ViewError. Its export, if any, is unused: a tombstone names its
// row by id.
func LoadDeletes(path string) (*Deletes, error) {
	v, _, err := pagedView(path)
	if err != nil {
		return nil, err
	}
	return &Deletes{path: path, v: v}, nil
}

// File is the deletes view's path.
func (d *Deletes) File() string { return d.path }

// tombstone is one row of the deletes view: the source row's id and when it was deleted.
type tombstone struct {
	id string
	at time.Time
}

func (t tombstone) key() pgstore.Cursor { return pgstore.Cursor{UpdatedAt: t.at, ID: t.id} }

// readTombstones reads one page of d past after, up to upto: in (deleted_at, id) order, each with an
// id and a deleted_at no later than upto (ErrOrder otherwise: a page out of order, or past upto,
// would move the cursor past a tombstone it never applied). A row with no id or deleted_at is a
// view's error, which stops the deletes pass: a tombstone cannot be quarantined.
func readTombstones(ctx context.Context, d *Deletes, conn dao.DataConn, after pgstore.Cursor, upto time.Time) ([]tombstone, error) {
	var afterID any
	if !after.IsZero() {
		afterID = after.ID
	}
	rows, err := d.v.WithArgs(after.UpdatedAt, afterID, upto).Query(ctx, conn)
	if err != nil {
		return nil, viewError{err}
	}
	var out []tombstone
	prev := after.UpdatedAt
	for x, err := range rows.All() {
		if err != nil {
			return nil, err
		}
		m, err := fields(x)
		if err != nil {
			return nil, viewError{fmt.Errorf("a deletes row: %w", err)}
		}
		var t tombstone
		switch id := m[FieldID].(type) {
		case string:
			t.id = id
		case json.Number:
			t.id = id.String()
		}
		at, _ := m[FieldDeletedAt].(string)
		if t.at, err = parseTime(at); err != nil || t.id == "" {
			return nil, viewError{fmt.Errorf("a deletes row needs an id and a deleted_at (RFC 3339): %s", x)}
		}
		switch {
		case t.at.After(upto):
			return nil, fmt.Errorf("%w: tombstone %s's deleted_at %s is past upto_ts %s", ErrOrder, t.id, stamp(t.at), stamp(upto))
		case t.at.Before(prev):
			return nil, fmt.Errorf("%w: tombstone %s's deleted_at %s comes after %s", ErrOrder, t.id, stamp(t.at), stamp(prev))
		}
		prev = t.at
		out = append(out, t)
	}
	return out, nil
}

// passDeletes applies the deletes view's tombstones past the tenant's deletes cursor, up to upto,
// page by page, each page one batch with the deletes cursor moved to its last tombstone. A
// tombstone deletes its row's document unless the document was written after it (DeleteSource),
// and takes the row out of quarantine. It returns the view's failure; the rows' pass has committed
// before it, so a failing deletes view never holds the rows back.
func (in *Ingester) passDeletes(ctx context.Context, upto time.Time, st *Stats) error {
	cur, err := in.st.DeletesCursor(ctx)
	if err != nil {
		return err
	}
	for {
		page, err := readTombstones(ctx, in.src.Deletes(), in.business, cur, upto)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		b := in.st.Begin(ctx)
		for _, t := range page {
			gone, err := b.DeleteSource(ctx, t.id, t.at)
			if err == nil {
				err = b.Release(ctx, t.id)
			}
			if err != nil {
				_ = b.Rollback()
				return err
			}
			if gone {
				st.DeletedView++
			} else {
				st.DeleteNoop++
			}
		}
		next := page[len(page)-1].key()
		if err := b.SetDeletesCursor(ctx, next); err != nil {
			_ = b.Rollback()
			return err
		}
		if err := in.commit(ctx, b); err != nil {
			return err
		}
		if sameKey(next, cur) {
			return ErrStalled
		}
		cur = next
	}
}

// deletes runs passDeletes for a source with a deletes view (Source.WithDeletes; without one, a row
// deleted outright leaves the index at the next complete refresh), and says a failure: in the log
// at every pass, and in the ingester's state (DeletesError) until a deletes pass succeeds. A lost
// lease is returned, as from any write.
func (in *Ingester) deletes(ctx context.Context, upto time.Time, st *Stats) error {
	if in.src.Deletes() == nil {
		return nil
	}
	err := in.passDeletes(ctx, upto, st)
	code := codeOf(err)
	in.deletesErr.Store(&code)
	switch {
	case err == nil:
		in.deletesTo.Store(&upto)
		return nil
	case errors.Is(err, ErrLockLost):
		return err
	case ctx.Err() == nil:
		logger.Warning(in.o.Log, err, "tenant "+in.tenant+": its deletes view "+in.src.Deletes().File()+": deleted rows wait for it, or for the next complete refresh")
	}
	return nil
}
