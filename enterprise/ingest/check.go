package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// The bounds of CheckOrder: how many rows it reads, and how many cursors it resumes from.
const (
	CheckRows   = 2000
	CheckProbes = 200
)

// OrderReport is what CheckOrder read and confirmed.
type OrderReport struct {
	Rows, Pages int
	// Ties are the consecutive rows sharing an updated_at the view was resumed between, and
	// Boundaries the page boundaries it was resumed at: each time, the next row was the one
	// expected.
	Ties, Boundaries int
	// Capped says the view has more rows than CheckRows: the first CheckRows were checked.
	Capped bool
	// Warnings are what the check could not exercise on this data.
	Warnings []string
}

// CheckOrder runs the view against conn, the live source, in one read-only snapshot, and confirms
// that paging it by its cursor reads every row once: the order the view returns its rows in is the
// order its WHERE compares the cursor in, (updated_at, id).
//
// It reads the view from its start, page after page as an ingester does, up to upto and at most
// CheckRows rows. Every row must carry an id and an updated_at, and the updated_at values must not
// go back. Then, at each page boundary and between each two consecutive rows sharing an updated_at
// (at most CheckProbes in all), it resumes the view from the first row's (updated_at, id) and
// requires the second to be the next row returned. A view that orders by something else than its
// cursor (ORDER BY its JSON column, or by id), or whose WHERE compares updated_at alone, returns
// another row there, or skips one, and is refused with a *ViewError on "process".
//
// The ids within one updated_at are compared by the database (their type and collation), which is
// why the check runs against a real source rather than in this process. A source with no two rows
// sharing an updated_at cannot show the id order: the report warns, and the check passes.
//
// The check is read-only: it needs nothing beyond the SELECT the view already has, and is safe to
// run against production.
func (s *Source) CheckOrder(ctx context.Context, conn dao.DataConn, upto time.Time) (OrderReport, error) {
	var rep OrderReport
	tx, err := dao.BeginConnTx(ctx, conn, dao.TxOptions{Access: dao.TxReadOnly, Isolation: dao.TxRepeatableRead})
	if err != nil {
		return rep, err
	}
	defer func() { _ = tx.Rollback() }()
	snap := snapshot{TxConn: tx, base: conn}
	refuse := func(format string, args ...any) error {
		return &ViewError{Path: s.path, Field: "process", Reason: fmt.Sprintf(format, args...)}
	}

	var seq []row
	var boundaries []int // the index of each page's last row in seq
	var cur pgstore.Cursor
	for len(seq) < CheckRows {
		rows, err := readPage(ctx, s, snap, cur, upto)
		if errors.Is(err, ErrOrder) {
			return rep, refuse("%v", err)
		}
		if err != nil {
			return rep, err
		}
		if len(rows) == 0 {
			break
		}
		rep.Pages++
		for _, r := range rows {
			if !r.keyed() {
				return rep, refuse("row %d: %v: every row needs an id and an updated_at", len(seq)+1, r.bad)
			}
			if len(seq) > 0 && r.id == seq[len(seq)-1].id && r.at.Equal(seq[len(seq)-1].at) {
				return rep, refuse("row %s is returned twice: the view's WHERE does not start after the cursor", r.id)
			}
			seq = append(seq, r)
		}
		boundaries = append(boundaries, len(seq)-1)
		cur = seq[len(seq)-1].key()
	}
	rep.Rows = len(seq)
	if len(seq) >= CheckRows {
		rep.Capped = true
	}
	if len(seq) == 0 {
		rep.Warnings = append(rep.Warnings, "the view returned no rows up to upto_ts: its order was not checked")
		return rep, nil
	}

	boundary := map[int]bool{}
	for _, i := range boundaries {
		boundary[i] = true
	}
	probes := 0
	for i := 0; i+1 < len(seq) && probes < CheckProbes; i++ {
		tie := seq[i].at.Equal(seq[i+1].at)
		if !tie && !boundary[i] {
			continue
		}
		probes++
		rows, err := readPage(ctx, s, snap, seq[i].key(), upto)
		if errors.Is(err, ErrOrder) {
			return rep, refuse("resumed after row %s: %v", seq[i].id, err)
		}
		if err != nil {
			return rep, err
		}
		if len(rows) == 0 || rows[0].id != seq[i+1].id {
			got := "no row"
			if len(rows) > 0 {
				got = "row " + rows[0].id
			}
			return rep, refuse("resumed after row %s (updated_at %s), the view returns %s where row %s comes next: "+
				"it must ORDER BY its updated_at expression, then id, and compare (updated_at, id) > ($1, $2) in the same order",
				seq[i].id, stamp(seq[i].at), got, seq[i+1].id)
		}
		if tie {
			rep.Ties++
		} else {
			rep.Boundaries++
		}
	}
	if rep.Ties == 0 {
		rep.Warnings = append(rep.Warnings, "no two rows read share an updated_at: the order of ids within one timestamp was not exercised")
	}
	return rep, nil
}

// snapshot is a transaction as the view's query runs on it.
type snapshot struct {
	dao.TxConn
	base dao.DataConn
}

func (c snapshot) Dialect() dao.Dialect { return c.base.Dialect() }
func (c snapshot) Name() string         { return c.base.Name() }
func (c snapshot) Close() error         { return nil }
func (c snapshot) Begin(context.Context) (dao.TxConn, error) {
	return nil, errors.New("ingest: the check's snapshot takes no nested transaction")
}
