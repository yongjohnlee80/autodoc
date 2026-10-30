// Package sqlitecrewmail is the crew, mail and inbox modules' tables on the daemon's SQLite store
// (ADR 1793271500 §4.2, migration 000026): one Store whose every transaction is a crew.Tx (and,
// with the mail module, a mail.Tx), so a verb's guard, crew's change and mail's effect commit as
// one. The crew_* half is here; the mail_* and inbox_* half lives beside it.
package sqlitecrewmail

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// Store is the crew tables on a store.
type Store struct{ s *store.Store }

// New is the crew tables on s.
func New(s *store.Store) *Store { return &Store{s: s} }

var _ crew.Store = (*Store)(nil)

// Write runs fn in one write transaction on the store's writer.
func (st *Store) Write(ctx context.Context, fn func(crew.Tx) error) error {
	return st.s.Write(ctx, func(tx *store.Tx) error { return fn(st.tx(tx)) })
}

// Read runs fn in one read transaction on a reader.
func (st *Store) Read(ctx context.Context, fn func(crew.Tx) error) error {
	return st.s.Read(ctx, func(tx *store.Tx) error { return fn(st.tx(tx)) })
}

func (st *Store) tx(tx *store.Tx) *Tx {
	return &Tx{tx: tx.Transaction(), t: tables.In(tx), m: mailDecl.In(tx), now: st.s.Clock().Now()}
}

// Tx is one transaction over the tables: a crew.Tx, and a mail.Tx (mail.go).
type Tx struct {
	tx  *dao.Transaction
	t   *crewTables
	m   *mailTables
	now time.Time
}

var _ crew.Tx = (*Tx)(nil)

// Now is the time the transaction began, from the store's clock.
func (t *Tx) Now() time.Time { return t.now }

// one is the first row rows hold, and whether there was one.
func one[R any](rows []R, err error) (R, bool, error) {
	var zero R
	if err != nil || len(rows) == 0 {
		return zero, false, err
	}
	return rows[0], true, nil
}

// got is a Get's row, with dao.ErrNoRows as "not there".
func got[R any](r R, err error) (R, bool, error) {
	var zero R
	switch {
	case errors.Is(err, dao.ErrNoRows):
		return zero, false, nil
	case err != nil:
		return zero, false, err
	}
	return r, true, nil
}

// blob is b for a NOT NULL BLOB column: never nil, which would bind as NULL.
func blob(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return slices.Clone(b)
}
