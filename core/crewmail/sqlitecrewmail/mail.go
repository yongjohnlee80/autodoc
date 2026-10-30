package sqlitecrewmail

import (
	"context"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// The mail tables' own columns, beside the crew ones in crew.go.
const (
	cSender        col = "sender"
	cSenderSession col = "sender_session"
	cIdemKey       col = "idem_key"
	cDigest        col = "digest"
	cSubject       col = "subject"
	cBody          col = "body"
	cReplyTo       col = "reply_to"
	cExpires       col = "expires"
	cNextSeq       col = "next_seq"
	cMessage       col = "message"
	cRecipient     col = "recipient"
	cLease         col = "lease"
	cLeaseUntil    col = "lease_until"
	cAttempts      col = "attempts"
	cReadAt        col = "read_at"
	cDoneAt        col = "done_at"
	cWhy           col = "why"

	bySeq     sort = "seq"
	byLease   sort = "lease_until"
	byExpires sort = "expires"
)

// recipientRow is one recipient's sequence high-water (mail_recipient).
type recipientRow struct {
	Profile string
	NextSeq int64
}

// mailTables are the mail tables, declared to the store with the crew ones.
type mailTables struct {
	messages   *schema[*mail.Message]
	recipients *schema[*recipientRow]
	deliveries *schema[*mail.Delivery]
}

var mailDecl = store.Declare("crewmail-mail", newMailTables)

func newMailTables(c dao.DataConn) *mailTables {
	dfs := map[col]dao.Field[*mail.Delivery]{}
	for n, scan := range map[col]func(*mail.Delivery) any{
		cID: func(r *mail.Delivery) any { return &r.ID }, cMessage: func(r *mail.Delivery) any { return &r.Message },
		cRecipient: func(r *mail.Delivery) any { return &r.Recipient }, cSession: func(r *mail.Delivery) any { return &r.Session },
		cSeq: func(r *mail.Delivery) any { return &r.Seq }, cState: func(r *mail.Delivery) any { return &r.State },
		cLease: func(r *mail.Delivery) any { return &r.Lease }, cLeaseUntil: func(r *mail.Delivery) any { return &r.LeaseUntil },
		cAttempts: func(r *mail.Delivery) any { return &r.Attempts }, cReadAt: func(r *mail.Delivery) any { return &r.ReadAt },
		cDoneAt: func(r *mail.Delivery) any { return &r.DoneAt }, cWhy: func(r *mail.Delivery) any { return &r.Why },
	} {
		dfs[n] = store.Col("mail_delivery", n, scan)
	}
	return &mailTables{
		messages: sortedTable(c, "mail_message", map[sort]string{byExpires: `"mail_message"."expires"`}, map[col]func(*mail.Message) any{
			cID: func(r *mail.Message) any { return &r.ID }, cSender: func(r *mail.Message) any { return &r.Sender },
			cSenderSession: func(r *mail.Message) any { return &r.SenderSession }, cIdemKey: func(r *mail.Message) any { return &r.IdemKey },
			cDigest: func(r *mail.Message) any { return &r.Digest }, cKind: func(r *mail.Message) any { return &r.Kind },
			cSubject: func(r *mail.Message) any { return &r.Subject }, cBody: func(r *mail.Message) any { return &r.Body },
			cReplyTo: func(r *mail.Message) any { return &r.ReplyTo }, cState: func(r *mail.Message) any { return &r.State },
			cCreated: func(r *mail.Message) any { return &r.Created }, cExpires: func(r *mail.Message) any { return &r.Expires },
		}, cID),
		recipients: table(c, "mail_recipient", map[col]func(*recipientRow) any{
			cProfile: func(r *recipientRow) any { return &r.Profile }, cNextSeq: func(r *recipientRow) any { return &r.NextSeq },
		}, cProfile),
		deliveries: dao.New(c,
			dao.Table[*mail.Delivery, col, sort, string]("mail_delivery"),
			dao.Fields[*mail.Delivery, col, sort, string](dfs),
			dao.SortMap[*mail.Delivery, col, sort, string](map[sort]string{
				byID: `"mail_delivery"."id"`, bySeq: `"mail_delivery"."recipient", "mail_delivery"."seq"`,
				byLease: `"mail_delivery"."lease_until"`}),
			dao.Conflict[*mail.Delivery, col, sort, string](cID),
		),
	}
}

// Mail is the same tables as the mail module takes them: every transaction is both a crew.Tx and
// a mail.Tx, so crew's guard, crew's change and mail's effect commit together.
func (st *Store) Mail() mail.Store { return mailStore{st} }

type mailStore struct{ st *Store }

func (m mailStore) Write(ctx context.Context, fn func(mail.Tx) error) error {
	return m.st.Write(ctx, func(tx crew.Tx) error { return fn(tx.(*Tx)) })
}

func (m mailStore) Read(ctx context.Context, fn func(mail.Tx) error) error {
	return m.st.s.Read(ctx, func(tx *store.Tx) error { return fn(m.st.tx(tx)) })
}

var _ mail.Tx = (*Tx)(nil)

// unhandled is a delivery queued or leased.
var unhandled = dao.In(`"mail_delivery"."state"`, []any{mail.Queued, mail.Leased})

// unexpired is a delivery whose message has not expired by now; expiredBy, one whose has.
func unexpired(now int64) dao.Predicate {
	return store.Correlated(true, "mail_message", "id", "mail_delivery", "message", messageExpiredBy(now)...)
}

func expiredBy(now int64) dao.Predicate {
	return store.Correlated(false, "mail_message", "id", "mail_delivery", "message", messageExpiredBy(now)...)
}

func messageExpiredBy(now int64) []dao.Predicate {
	return []dao.Predicate{dao.IsNotNull(`"mail_message"."expires"`), dao.Lte(`"mail_message"."expires"`, now)}
}

func (t *Tx) Message(id string) (mail.Message, bool, error) {
	r, ok, err := got(t.m.messages.On(t.tx).With(cID, id).Get())
	if !ok {
		return mail.Message{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) MessageByKey(sender, key string) (mail.Message, bool, error) {
	r, ok, err := one(t.m.messages.On(t.tx).With(cSender, sender).With(cIdemKey, key).Select())
	if !ok {
		return mail.Message{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) Response(replyTo, sender string) (mail.Message, bool, error) {
	r, ok, err := one(t.m.messages.On(t.tx).With(cReplyTo, replyTo).With(cSender, sender).
		With(cKind, mail.KindResponse).Select())
	if !ok {
		return mail.Message{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutMessage(m mail.Message) error {
	return t.m.messages.On(t.tx).SetMap(map[col]any{cID: m.ID, cSender: m.Sender, cSenderSession: m.SenderSession,
		cIdemKey: m.IdemKey, cDigest: blob(m.Digest), cKind: m.Kind, cSubject: m.Subject, cBody: m.Body,
		cReplyTo: m.ReplyTo, cState: m.State, cCreated: m.Created, cExpires: m.Expires}).Upsert()
}

// NextSeq is recipient's next seq; the high-water rises past it in the same transaction.
func (t *Tx) NextSeq(recipient string) (int64, error) {
	n := int64(1)
	r, ok, err := got(t.m.recipients.On(t.tx).With(cProfile, recipient).Get())
	if err != nil {
		return 0, err
	}
	if ok {
		n = r.NextSeq
	}
	return n, t.m.recipients.On(t.tx).SetMap(map[col]any{cProfile: recipient, cNextSeq: n + 1}).Upsert()
}

func (t *Tx) Delivery(id string) (mail.Delivery, bool, error) {
	r, ok, err := got(t.m.deliveries.On(t.tx).With(cID, id).Get())
	if !ok {
		return mail.Delivery{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) DeliveryOf(message, recipient string) (mail.Delivery, bool, error) {
	r, ok, err := one(t.m.deliveries.On(t.tx).With(cMessage, message).With(cRecipient, recipient).Select())
	if !ok {
		return mail.Delivery{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutDelivery(d mail.Delivery) error {
	return t.m.deliveries.On(t.tx).SetMap(map[col]any{cID: d.ID, cMessage: d.Message, cRecipient: d.Recipient,
		cSession: d.Session, cSeq: d.Seq, cState: d.State, cLease: d.Lease, cLeaseUntil: d.LeaseUntil,
		cAttempts: d.Attempts, cReadAt: d.ReadAt, cDoneAt: d.DoneAt, cWhy: d.Why}).Upsert()
}

func (t *Tx) DeliveriesOf(message string) ([]mail.Delivery, error) {
	return deliveries(t.m.deliveries.On(t.tx).With(cMessage, message).OrderBy(dao.Asc(bySeq)).Select())
}

func (t *Tx) Eligible(recipient, session string, now int64, limit int) ([]mail.Delivery, error) {
	return deliveries(t.m.deliveries.On(t.tx).With(cRecipient, recipient).With(cState, mail.Queued).
		WithPredicate(dao.In(`"mail_delivery"."session"`, []any{"", session})).WithPredicate(unexpired(now)).
		OrderBy(dao.Asc(bySeq)).Limit(uint64(limit)).Select())
}

func (t *Tx) Unhandled(recipient string) ([]mail.Delivery, error) {
	return deliveries(t.m.deliveries.On(t.tx).With(cRecipient, recipient).WithPredicate(unhandled).
		OrderBy(dao.Asc(bySeq)).Select())
}

func (t *Tx) BoundTo(session string) ([]mail.Delivery, error) {
	return deliveries(t.m.deliveries.On(t.tx).With(cSession, session).WithPredicate(unhandled).
		OrderBy(dao.Asc(bySeq)).Select())
}

func (t *Tx) LeasesDue(now int64, limit int) ([]mail.Delivery, error) {
	return deliveries(t.m.deliveries.On(t.tx).With(cState, mail.Leased).
		WithPredicate(dao.Lte(`"mail_delivery"."lease_until"`, now)).OrderBy(dao.Asc(bySeq)).Limit(uint64(limit)).Select())
}

func (t *Tx) Expiring(now int64, limit int) ([]mail.Delivery, error) {
	return deliveries(t.m.deliveries.On(t.tx).WithPredicate(unhandled).WithPredicate(expiredBy(now)).
		OrderBy(dao.Asc(bySeq)).Limit(uint64(limit)).Select())
}

// NextDeadline is the earliest lease_until of a leased delivery and the earliest expiry of a message
// with an unhandled delivery: one ordered, one-row read each.
func (t *Tx) NextDeadline() (int64, bool, error) {
	var at int64
	has := false
	take := func(v *int64) {
		if v != nil && (!has || *v < at) {
			at, has = *v, true
		}
	}
	lease, err := deliveries(t.m.deliveries.On(t.tx).With(cState, mail.Leased).
		WithPredicate(dao.IsNotNull(`"mail_delivery"."lease_until"`)).OrderBy(dao.Asc(byLease)).Limit(1).Select())
	if err != nil {
		return 0, false, err
	}
	if len(lease) > 0 {
		take(lease[0].LeaseUntil)
	}
	// the earliest expiry of a message that still has an unhandled delivery: read from mail_message
	// itself, ordered by its own column
	msg, ok, err := one(t.m.messages.On(t.tx).WithPredicate(dao.IsNotNull(`"mail_message"."expires"`)).
		WithPredicate(store.Correlated(false, "mail_delivery", "message", "mail_message", "id", unhandled)).
		OrderBy(dao.Asc(byExpires)).Limit(1).Select())
	if err != nil {
		return 0, false, err
	}
	if ok {
		take(msg.Expires)
	}
	return at, has, nil
}

func deliveries(rows []*mail.Delivery, err error) ([]mail.Delivery, error) {
	if err != nil {
		return nil, err
	}
	out := make([]mail.Delivery, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out, nil
}
