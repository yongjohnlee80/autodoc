// Package mailtest holds the mail module's test kit: an in-memory Store whose transactions are
// crewtest's with the mail tables beside them, so a crew verb's mail effect and a mail verb's crew
// guard commit together, as on the engine's one transaction.
package mailtest

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// Mem is a mail.Store (and, through Crew, a crew.Store) in memory. A Write works on copies and
// commits them only when fn returns nil.
type Mem struct {
	mu   sync.Mutex
	crew *crewtest.Mem
	st   state
}

// Change is one row of the human feed's journal.
type Change struct{ Kind, Ref string }

type state struct {
	messages   map[string]mail.Message
	deliveries map[string]mail.Delivery
	nextSeq    map[string]int64
	changes    []Change
}

func (s state) clone() state {
	return state{messages: maps.Clone(s.messages), deliveries: maps.Clone(s.deliveries),
		nextSeq: maps.Clone(s.nextSeq), changes: slices.Clone(s.changes)}
}

// NewMem is an empty store whose transactions read the time from now.
func NewMem(now func() time.Time) *Mem {
	return &Mem{crew: crewtest.NewMem(now), st: state{messages: map[string]mail.Message{},
		deliveries: map[string]mail.Delivery{}, nextSeq: map[string]int64{}}}
}

// Write runs fn on copies of the crew and mail state, and commits both if fn succeeds.
func (m *Mem) Write(ctx context.Context, fn func(mail.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.crew.Write(ctx, func(ct crew.Tx) error {
		tx := &memTx{Tx: ct, st: m.st.clone()}
		if err := fn(tx); err != nil {
			return err
		}
		m.st = tx.st
		return nil
	})
}

// Read runs fn on copies; its changes are dropped.
func (m *Mem) Read(ctx context.Context, fn func(mail.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.crew.Read(ctx, func(ct crew.Tx) error { return fn(&memTx{Tx: ct, st: m.st.clone()}) })
}

// Crew is the same store as the crew module takes it: its transactions are mail's too.
func (m *Mem) Crew() crew.Store { return crewStore{m} }

type crewStore struct{ m *Mem }

func (s crewStore) Write(ctx context.Context, fn func(crew.Tx) error) error {
	return s.m.Write(ctx, func(tx mail.Tx) error { return fn(tx) })
}

func (s crewStore) Read(ctx context.Context, fn func(crew.Tx) error) error {
	return s.m.Read(ctx, func(tx mail.Tx) error { return fn(tx) })
}

// Changes is the feed's journal so far.
func (m *Mem) Changes() []Change {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.changes)
}

// Deliveries is every delivery of message, by recipient seq.
func (m *Mem) Deliveries() []mail.Delivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sorted(slices.Collect(maps.Values(m.st.deliveries)))
}

// Messages is every message stored.
func (m *Mem) Messages() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Collect(maps.Values(m.st.messages))
	slices.SortFunc(out, func(a, b mail.Message) int { return int(a.Created - b.Created) })
	return out
}

type memTx struct {
	crew.Tx
	st state
}

func (t *memTx) Message(id string) (mail.Message, bool, error) {
	msg, ok := t.st.messages[id]
	return msg, ok, nil
}

func (t *memTx) MessageByKey(sender, key string) (mail.Message, bool, error) {
	for _, msg := range t.st.messages {
		if msg.Sender == sender && msg.IdemKey == key {
			return msg, true, nil
		}
	}
	return mail.Message{}, false, nil
}

func (t *memTx) Response(replyTo, sender string) (mail.Message, bool, error) {
	for _, msg := range t.st.messages {
		if msg.Kind == mail.KindResponse && msg.ReplyTo == replyTo && msg.Sender == sender {
			return msg, true, nil
		}
	}
	return mail.Message{}, false, nil
}

func (t *memTx) PutMessage(msg mail.Message) error { t.st.messages[msg.ID] = msg; return nil }

func (t *memTx) NextSeq(recipient string) (int64, error) {
	n := max(t.st.nextSeq[recipient], 1)
	t.st.nextSeq[recipient] = n + 1
	return n, nil
}

func (t *memTx) Delivery(id string) (mail.Delivery, bool, error) {
	d, ok := t.st.deliveries[id]
	return d, ok, nil
}

func (t *memTx) DeliveryOf(message, recipient string) (mail.Delivery, bool, error) {
	for _, d := range t.st.deliveries {
		if d.Message == message && d.Recipient == recipient {
			return d, true, nil
		}
	}
	return mail.Delivery{}, false, nil
}

func (t *memTx) PutDelivery(d mail.Delivery) error { t.st.deliveries[d.ID] = d; return nil }

func (t *memTx) DeliveriesOf(message string) ([]mail.Delivery, error) {
	return t.where(func(d mail.Delivery) bool { return d.Message == message }, 0), nil
}

func (t *memTx) Eligible(recipient, session string, limit int) ([]mail.Delivery, error) {
	return t.where(func(d mail.Delivery) bool {
		return d.Recipient == recipient && d.State == mail.Queued && (d.Session == "" || d.Session == session)
	}, limit), nil
}

func (t *memTx) Unhandled(recipient string) ([]mail.Delivery, error) {
	return t.where(func(d mail.Delivery) bool { return d.Recipient == recipient && unhandled(d) }, 0), nil
}

func (t *memTx) BoundTo(session string) ([]mail.Delivery, error) {
	return t.where(func(d mail.Delivery) bool { return d.Session == session && unhandled(d) }, 0), nil
}

func (t *memTx) LeasesDue(now int64, limit int) ([]mail.Delivery, error) {
	return t.where(func(d mail.Delivery) bool {
		return d.State == mail.Leased && d.LeaseUntil != nil && *d.LeaseUntil <= now
	}, limit), nil
}

func (t *memTx) Expiring(now int64, limit int) ([]mail.Delivery, error) {
	return t.where(func(d mail.Delivery) bool {
		msg := t.st.messages[d.Message]
		return unhandled(d) && msg.Expires != nil && *msg.Expires <= now
	}, limit), nil
}

func (t *memTx) AddInboxChange(kind, ref string) error {
	t.st.changes = append(t.st.changes, Change{Kind: kind, Ref: ref})
	return nil
}

func unhandled(d mail.Delivery) bool { return d.State == mail.Queued || d.State == mail.Leased }

func (t *memTx) where(keep func(mail.Delivery) bool, limit int) []mail.Delivery {
	var out []mail.Delivery
	for _, d := range t.st.deliveries {
		if keep(d) {
			out = append(out, d)
		}
	}
	out = sorted(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sorted(ds []mail.Delivery) []mail.Delivery {
	slices.SortFunc(ds, func(a, b mail.Delivery) int {
		if a.Recipient != b.Recipient {
			if a.Recipient < b.Recipient {
				return -1
			}
			return 1
		}
		return int(a.Seq - b.Seq)
	})
	return ds
}
