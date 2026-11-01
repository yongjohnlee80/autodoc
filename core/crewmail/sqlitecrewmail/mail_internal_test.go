package sqlitecrewmail

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/mail/mailtest"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestSQLiteKeepsTheMailStateMachines: the mail tables on the daemon's SQLite store keep the mail
// module's state machines, as the in-memory store does (mailtest.Run), with SQLite's own unique
// indexes and its eligibility and expiry queries in play.
func TestSQLiteKeepsTheMailStateMachines(t *testing.T) {
	mailtest.Run(t, mailtest.Harness{New: func(t *testing.T, now func() time.Time) mailtest.Stores {
		s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "autodoc.db"), store.WithClock(now))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		st := New(s)
		return mailtest.Stores{Mail: st.Mail(), Crew: st, Inspect: inspector{t: t, st: st}}
	}})
}

// inspector reads every row through the tables' own schemas, for the cells' assertions.
type inspector struct {
	t  *testing.T
	st *Store
}

func (in inspector) read(fn func(*Tx) error) {
	in.t.Helper()
	if err := in.st.s.Read(context.Background(), func(tx *store.Tx) error { return fn(in.st.tx(tx)) }); err != nil {
		in.t.Fatal(err)
	}
}

func (in inspector) Deliveries() []mail.Delivery {
	var out []mail.Delivery
	in.read(func(tx *Tx) error {
		var err error
		out, err = deliveries(tx.m.deliveries.On(tx.tx).OrderBy(dao.Asc(bySeq)).Select())
		return err
	})
	return out
}

func (in inspector) Messages() []mail.Message {
	var out []mail.Message
	in.read(func(tx *Tx) error {
		rows, err := tx.m.messages.On(tx.tx).OrderBy(dao.Asc(byID)).Select()
		for _, r := range rows {
			out = append(out, *r)
		}
		return err
	})
	// the in-memory store lists them by creation; ids order nothing here
	slices.SortStableFunc(out, func(a, b mail.Message) int { return cmp.Compare(a.Created, b.Created) })
	return out
}

func (in inspector) Jobs() []mail.FallbackJob {
	var out []mail.FallbackJob
	in.read(func(tx *Tx) error {
		var err error
		out, err = jobs(tx.m.jobs.On(tx.tx).OrderBy(dao.Asc(byCreated)).Select())
		return err
	})
	return out
}

func (in inspector) Changes() []crewtest.InboxChange {
	var out []crewtest.InboxChange
	in.read(func(tx *Tx) error {
		rows, err := tx.t.inbox.On(tx.tx).OrderBy(dao.Asc(byID)).Select()
		for _, r := range rows {
			out = append(out, crewtest.InboxChange{Kind: r.Kind, Ref: r.Ref})
		}
		return err
	})
	return out
}
