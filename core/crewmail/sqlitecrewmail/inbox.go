package sqlitecrewmail

import (
	"context"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// The inbox module's reads (inbox.Tx), over the same tables: the feed's journal, the human's
// deliveries and the raised attention.

// Inbox is the same tables as the inbox module takes them: every transaction is a crew.Tx, a
// mail.Tx and an inbox.Tx.
func (st *Store) Inbox() inbox.Store { return inboxStore{st} }

type inboxStore struct{ st *Store }

func (s inboxStore) Write(ctx context.Context, fn func(inbox.Tx) error) error {
	return s.st.Write(ctx, func(tx crew.Tx) error { return fn(tx.(*Tx)) })
}

func (s inboxStore) Read(ctx context.Context, fn func(inbox.Tx) error) error {
	return s.st.Read(ctx, func(tx crew.Tx) error { return fn(tx.(*Tx)) })
}

var _ inbox.Tx = (*Tx)(nil)

// limited applies limit to q when it is positive: a limit of 0 is every row, never LIMIT 0.
func limited[Q interface{ Limit(uint64) Q }](q Q, limit int) Q {
	if limit > 0 {
		return q.Limit(uint64(limit))
	}
	return q
}

func (t *Tx) Changes(after int64, limit int) ([]inbox.Change, error) {
	q := t.t.inbox.On(t.tx).WithPredicate(dao.Gt(`"inbox_change"."seq"`, after)).OrderBy(dao.Asc(byID))
	rows, err := limited(q, limit).Select()
	if err != nil {
		return nil, err
	}
	out := make([]inbox.Change, len(rows))
	for i, r := range rows {
		out[i] = inbox.Change{Seq: r.Seq, Kind: r.Kind, Ref: r.Ref, At: r.At}
	}
	return out, nil
}

// ChangeBounds reads the journal's two ends: seq is its primary key, so each is one index step.
func (t *Tx) ChangeBounds() (int64, int64, error) {
	first, err := t.t.inbox.On(t.tx).OrderBy(dao.Asc(byID)).Limit(1).Select()
	if err != nil || len(first) == 0 {
		return 0, 0, err
	}
	last, err := t.t.inbox.On(t.tx).OrderBy(dao.Desc(byID)).Limit(1).Select()
	if err != nil || len(last) == 0 {
		return 0, 0, err
	}
	return first[0].Seq, last[0].Seq, nil
}

func (t *Tx) Raised() ([]crew.Attention, error) {
	rows, err := t.t.attn.On(t.tx).WithPredicate(dao.IsNull(`"crew_attention"."cleared"`)).OrderBy(dao.Asc(byID)).Select()
	if err != nil {
		return nil, err
	}
	out := make([]crew.Attention, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out, nil
}

// openNonEvent is a delivery whose message is open and not a generated notice; aNotice, one whose
// message is a generated notice. Each is a correlated probe of the message by id (store.Correlated).
var (
	openNonEvent = store.Correlated(false, "mail_message", "id", "mail_delivery", "message",
		dao.Eq(`"mail_message"."state"`, mail.MessageOpen), dao.NotIn(`"mail_message"."kind"`, []any{mail.KindEvent}))
	aNotice = store.Correlated(false, "mail_message", "id", "mail_delivery", "message",
		dao.Eq(`"mail_message"."kind"`, mail.KindEvent))
)

func (t *Tx) OpenToUser(user string, after int64, limit int) ([]mail.Item, error) {
	q := t.m.deliveries.On(t.tx).With(cRecipient, user).WithPredicate(openNonEvent).
		WithPredicate(dao.Gt(`"mail_delivery"."seq"`, after)).OrderBy(dao.Asc(bySeq))
	return t.items(deliveries(limited(q, limit).Select()))
}

func (t *Tx) ToUser(user string, before int64, limit int) ([]mail.Item, error) {
	q := t.m.deliveries.On(t.tx).With(cRecipient, user)
	if before > 0 {
		q = q.WithPredicate(dao.Lt(`"mail_delivery"."seq"`, before))
	}
	return t.items(deliveries(limited(q.OrderBy(dao.Desc(bySeq)), limit).Select()))
}

func (t *Tx) FailedNotices(limit int) ([]mail.Item, error) {
	q := t.m.deliveries.On(t.tx).With(cState, mail.Failed).WithPredicate(aNotice).
		OrderBy(dao.Desc(byDone), dao.Asc(byID))
	return t.items(deliveries(limited(q, limit).Select()))
}

// items pairs each delivery with its message, read in one query.
func (t *Tx) items(ds []mail.Delivery, err error) ([]mail.Item, error) {
	if err != nil || len(ds) == 0 {
		return []mail.Item{}, err
	}
	ids := make([]any, 0, len(ds))
	for _, d := range ds {
		ids = append(ids, d.Message)
	}
	rows, err := t.m.messages.On(t.tx).WithPredicate(dao.In(`"mail_message"."id"`, ids)).Select()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]mail.Message, len(rows))
	for _, r := range rows {
		byID[r.ID] = *r
	}
	out := make([]mail.Item, 0, len(ds))
	for _, d := range ds {
		out = append(out, mail.Item{Delivery: d, Message: byID[d.Message]})
	}
	return out, nil
}
