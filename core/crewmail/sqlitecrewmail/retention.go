package sqlitecrewmail

import (
	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// RETENTION (docs/decisions/crew.md#handled-mail-is-kept-for-a-window): each read is one bounded
// page, by an index, so a sweep's every transaction is short. A message's deliveries are found by
// mail_delivery's UNIQUE (message, recipient), and a delivery's fallback job by mail_fallback's
// UNIQUE (delivery): mail_fallback has no index on message, so a job is reached through its
// delivery, never by scanning the jobs.

// pendingAt is a delivery that keeps its message past cutoff: not done (queued or leased, or no
// done_at), done after cutoff, or with a fallback job still open or leased.
func pendingAt(cutoff int64) dao.Predicate {
	return dao.Or(
		dao.In(`"mail_delivery"."state"`, []any{mail.Queued, mail.Leased}),
		dao.IsNull(`"mail_delivery"."done_at"`),
		dao.Gt(`"mail_delivery"."done_at"`, cutoff),
		store.Correlated(false, "mail_fallback", "delivery", "mail_delivery", "id",
			dao.In(`"mail_fallback"."state"`, []any{mail.JobOpen, mail.JobLeased})),
	)
}

func (t *Tx) HandledBefore(cutoff int64, after string, limit int) ([]string, error) {
	rows, err := t.m.messages.On(t.tx).
		WithPredicate(dao.Gt(`"mail_message"."id"`, after)).
		WithPredicate(dao.Lte(`"mail_message"."created"`, cutoff)).
		WithPredicate(store.Correlated(true, "mail_delivery", "message", "mail_message", "id", pendingAt(cutoff))).
		OrderBy(dao.Asc(byID)).Limit(uint64(limit)).Select()
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids, nil
}

func (t *Tx) DeleteMessage(id string) error {
	ds, err := t.DeliveriesOf(id)
	if err != nil {
		return err
	}
	if len(ds) > 0 {
		ids := make([]any, len(ds))
		for i, d := range ds {
			ids[i] = d.ID
		}
		if err := t.m.jobs.On(t.tx).WithPredicate(dao.In(`"mail_fallback"."delivery"`, ids)).Delete(); err != nil {
			return err
		}
		if err := t.m.deliveries.On(t.tx).With(cMessage, id).Delete(); err != nil {
			return err
		}
	}
	return t.m.messages.On(t.tx).With(cID, id).Delete()
}

func (t *Tx) PruneInbox(before int64, limit int) (int, error) {
	_, newest, err := t.ChangeBounds()
	if err != nil || newest == 0 {
		return 0, err
	}
	rows, err := t.t.inbox.On(t.tx).WithPredicate(dao.Lt(`"inbox_change"."seq"`, newest)).
		WithPredicate(dao.Lt(`"inbox_change"."at"`, before)).OrderBy(dao.Asc(byID)).Limit(uint64(limit)).Select()
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	seqs := make([]any, len(rows))
	for i, r := range rows {
		seqs[i] = r.Seq
	}
	if err := t.t.inbox.On(t.tx).WithPredicate(dao.In(`"inbox_change"."seq"`, seqs)).Delete(); err != nil {
		return 0, err
	}
	return len(rows), nil
}
