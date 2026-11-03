package sqlitecrewmail

import (
	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// RETENTION (docs/decisions/crew.md#handled-mail-is-kept-for-a-window): each read is one bounded
// page, by an index, so a sweep's every transaction is short: the messages are read oldest first
// by mail_message_created (000034), at most a page of them, never the young ones. A message's
// deliveries are found by mail_delivery's UNIQUE (message, recipient), and a delivery's fallback
// job by mail_fallback's UNIQUE (delivery): mail_fallback has no index on message, so a job is
// reached through its delivery, never by scanning the jobs.

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

// HandledBefore reads one page of the oldest messages by mail_message_created, (created, id) past
// after: the rows read are the page, whatever else the store holds, and each one's deliveries are
// read by their (message, recipient) index.
func (t *Tx) HandledBefore(cutoff int64, after mail.MessageCursor, limit int) ([]string, mail.MessageCursor, int, error) {
	rows, err := t.m.messages.On(t.tx).
		WithPredicate(dao.Lte(`"mail_message"."created"`, cutoff)).
		WithPredicate(dao.Raw(`("mail_message"."created", "mail_message"."id") > (?, ?)`, after.Created, after.ID)).
		OrderBy(dao.Asc(byAge)).Limit(uint64(limit)).Select()
	if err != nil {
		return nil, after, 0, err
	}
	var ids []string
	last := after
	for _, r := range rows {
		last = mail.MessageCursor{Created: r.Created, ID: r.ID}
		pending, err := t.m.deliveries.On(t.tx).With(cMessage, r.ID).WithPredicate(pendingAt(cutoff)).Limit(1).Select()
		if err != nil {
			return nil, after, 0, err
		}
		if len(pending) == 0 {
			ids = append(ids, r.ID)
		}
	}
	return ids, last, len(rows), nil
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
