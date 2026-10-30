package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// Send is what a sender asks of mail.send.
type Send struct {
	To      []string // "name" in the sender's scope, "name@scope", "user", or "session:<id>"
	Kind    string   // message or request; "" is message
	Subject string
	Body    string
	Key     string        // the idempotency key: the same key answers the first send
	TTL     time.Duration // 0 never expires
}

// Sent is a stored message and its deliveries.
type Sent struct {
	Message    Message
	Deliveries []Delivery
}

// Item is a delivery with its message, as a receive or a preview gives it.
type Item struct {
	Delivery Delivery
	Message  Message
}

// Send stores a message and its deliveries in one write, from credential's session (§4.6). A key the sender
// already used answers that send while its message is retained, when the content is the same, and
// ErrKeyReused when it is not.
func (m *Manager) Send(ctx context.Context, credential string, p Send) (Sent, error) {
	if err := checkSend(p); err != nil {
		return Sent{}, err
	}
	var out Sent
	err := m.store.Write(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		sum := sendDigest(p)
		if old, ok, err := tx.MessageByKey(s.Profile, p.Key); err != nil {
			return err
		} else if ok {
			if !bytes.Equal(old.Digest, sum) {
				return ErrKeyReused
			}
			ds, err := tx.DeliveriesOf(old.ID)
			out = Sent{Message: old, Deliveries: ds}
			return err
		}
		to, err := m.addresses(tx, s, p.To)
		if err != nil {
			return err
		}
		now := tx.Now()
		msg := Message{ID: m.newID(), Sender: s.Profile, SenderSession: s.ID, IdemKey: p.Key, Digest: sum,
			Kind: p.Kind, Subject: p.Subject, Body: p.Body, State: MessageOpen, Created: now.UnixNano()}
		if msg.Kind == "" {
			msg.Kind = KindMessage
		}
		if p.TTL > 0 {
			at := now.Add(p.TTL).UnixNano()
			msg.Expires = &at
		}
		if err := tx.PutMessage(msg); err != nil {
			return err
		}
		out = Sent{Message: msg}
		for _, a := range to {
			d, err := m.deliver(tx, msg, a.profile, a.session)
			if err != nil {
				return err
			}
			out.Deliveries = append(out.Deliveries, d)
		}
		return nil
	})
	return out, err
}

func checkSend(p Send) error {
	switch {
	case len(p.Body) > MaxBody:
		return fmt.Errorf("%w: %d bytes, at most %d", ErrTooLarge, len(p.Body), MaxBody)
	case p.Kind != "" && p.Kind != KindMessage && p.Kind != KindRequest:
		return fmt.Errorf("%w: kind %q is not message or request", ErrInvalid, p.Kind)
	case p.Key == "":
		return fmt.Errorf("%w: an empty key", ErrInvalid)
	case len(p.To) == 0:
		return fmt.Errorf("%w: no recipient", ErrInvalid)
	case p.TTL < 0:
		return fmt.Errorf("%w: a negative ttl", ErrInvalid)
	}
	return nil
}

// sendDigest is the content a send's key is bound to: a replay with other content is a new send
// under a reused key.
func sendDigest(p Send) []byte {
	h := sha256.New()
	field := func(s string) { _ = binary.Write(h, binary.BigEndian, uint64(len(s))); h.Write([]byte(s)) }
	field(p.Kind)
	field(p.Subject)
	field(p.Body)
	for _, t := range p.To {
		field(t)
	}
	_ = binary.Write(h, binary.BigEndian, int64(p.TTL))
	return h.Sum(nil)
}

type address struct{ profile, session string }

// addresses resolves every address once, in s's scope; any that resolves to nothing is
// ErrNoRecipient, and the daemon's own profile is never a recipient. A message has one delivery per
// recipient profile (mail_delivery's UNIQUE (message, recipient)): the same address twice is one,
// and two addresses binding one profile differently (gold and session:<gold's>, or two of its
// sessions) are refused, never silently narrowed or widened.
func (m *Manager) addresses(tx Tx, s crew.Session, to []string) ([]address, error) {
	from, ok, err := tx.Profile(s.Profile)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, crew.ErrBadCredential
	}
	var out []address
	bound := map[string]string{} // profile → the session its address binds ("" for any)
	for _, t := range to {
		a, err := resolveAddress(tx, from.Scope, t)
		if err != nil {
			return nil, err
		}
		if s, ok := bound[a.profile]; ok {
			if s != a.session {
				return nil, fmt.Errorf("%w: two addresses name one recipient differently (%s)", ErrInvalid, t)
			}
			continue
		}
		bound[a.profile] = a.session
		out = append(out, a)
	}
	return out, nil
}

func resolveAddress(tx Tx, scope, to string) (address, error) {
	if id, ok := strings.CutPrefix(to, "session:"); ok {
		s, ok, err := tx.Session(id)
		if err != nil {
			return address{}, err
		}
		if !ok || s.Ended != nil {
			return address{}, fmt.Errorf("%w: %s is no live session", ErrNoRecipient, to)
		}
		return address{profile: s.Profile, session: s.ID}, nil
	}
	name, in, cross := strings.Cut(to, "@")
	switch {
	case to == UserCodename:
		name, scope = UserCodename, GlobalScope
	case cross && strings.Contains(in, ":"):
		scope = in
	case cross:
		scope = "autovim:" + in // jarvis@mcat: an AutoVim project by name (§4.3)
	}
	p, ok, err := tx.ActiveProfile(scope, name)
	if err != nil {
		return address{}, err
	}
	if !ok || p.ID == DaemonProfile || name == "" {
		return address{}, fmt.Errorf("%w: %s", ErrNoRecipient, to)
	}
	// a profile of a scope that is gone receives nothing again: the sender hears so now
	if gone, err := scopeGone(tx, p.Scope); err != nil {
		return address{}, err
	} else if gone {
		return address{}, fmt.Errorf("%w: %s's scope has ended", ErrNoRecipient, to)
	}
	return address{profile: p.ID}, nil
}

// Received is one receive's leased deliveries, and whether eligible mail remains.
type Received struct {
	Items []Item
	More  bool
}

// Receive leases the credential's session's next eligible deliveries, at most ReceiveMax, in seq order, each for
// LeaseFor (§4.6). More is true while eligible mail remains: the guide drains until it is false.
func (m *Manager) Receive(ctx context.Context, credential string) (Received, error) {
	var out Received
	err := m.store.Write(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		if err := m.expire(tx); err != nil {
			return err
		}
		if err := m.crew.Received(tx, s.ID); err != nil {
			return err
		}
		ds, err := tx.Eligible(s.Profile, s.ID, tx.Now().UnixNano(), ReceiveMax+1)
		if err != nil {
			return err
		}
		if len(ds) > ReceiveMax {
			ds, out.More = ds[:ReceiveMax], true
		}
		now := tx.Now()
		until := now.Add(LeaseFor).UnixNano()
		for _, d := range ds {
			msg, ok, err := tx.Message(d.Message)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("mail: delivery %s names no message %s", d.ID, d.Message)
			}
			d.State, d.Lease, d.LeaseUntil = Leased, m.newID(), &until
			if d.ReadAt == nil {
				at := now.UnixNano()
				d.ReadAt = &at
			}
			if err := tx.PutDelivery(d); err != nil {
				return err
			}
			out.Items = append(out.Items, Item{Delivery: d, Message: msg})
		}
		return nil
	})
	return out, err
}

// owned is delivery id when session s may act on it: the recipient's, unbound or bound to s.
// Anything else answers as a lost lease, which says nothing of another's mail.
func owned(tx Tx, s crew.Session, id string) (Delivery, error) {
	d, ok, err := tx.Delivery(id)
	if err != nil {
		return Delivery{}, err
	}
	if !ok || d.Recipient != s.Profile || (d.Session != "" && d.Session != s.ID) {
		return Delivery{}, ErrLeaseLost
	}
	return d, nil
}

// Renew extends a lease the caller holds by LeaseFor from now; it does not count as an attempt.
func (m *Manager) Renew(ctx context.Context, credential, delivery, lease string) error {
	return m.store.Write(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		if err := m.expire(tx); err != nil {
			return err
		}
		d, err := owned(tx, s, delivery)
		if err != nil {
			return err
		}
		if d.State != Leased || d.Lease != lease || lease == "" {
			return ErrLeaseLost
		}
		until := tx.Now().Add(LeaseFor).UnixNano()
		d.LeaseUntil = &until
		return tx.PutDelivery(d)
	})
}

// Ack is done with a delivery under its lease. Replaying an ack of the same lease answers ok;
// another lease, or one that lapsed, is ErrLeaseLost.
func (m *Manager) Ack(ctx context.Context, credential, delivery, lease string) error {
	return m.store.Write(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		if err := m.expire(tx); err != nil {
			return err
		}
		d, err := owned(tx, s, delivery)
		if err != nil {
			return err
		}
		return ack(tx, d, lease)
	})
}

func ack(tx Tx, d Delivery, lease string) error {
	switch {
	case lease == "" || d.Lease != lease:
		return ErrLeaseLost
	case d.State == Acked:
		return nil
	case d.State != Leased:
		return ErrLeaseLost
	}
	now := tx.Now().UnixNano()
	d.State, d.DoneAt, d.LeaseUntil = Acked, &now, nil
	return tx.PutDelivery(d)
}

// Reply is what a replier gives mail.reply.
type Reply struct {
	Subject string
	Body    string
	Key     string
}

// Reply acks a request's delivery under its lease and sends its response, in one write (§4.6). A
// replier's response to a request is one: a retry with the same content answers it, even after
// the lease is gone, and other content is ErrKeyReused.
func (m *Manager) Reply(ctx context.Context, credential, delivery, lease string, p Reply) (Message, error) {
	switch {
	case len(p.Body) > MaxBody:
		return Message{}, fmt.Errorf("%w: %d bytes, at most %d", ErrTooLarge, len(p.Body), MaxBody)
	case p.Key == "":
		return Message{}, fmt.Errorf("%w: an empty key", ErrInvalid)
	}
	var out Message
	err := m.store.Write(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		if err := m.expire(tx); err != nil {
			return err
		}
		d, err := owned(tx, s, delivery)
		if err != nil {
			return err
		}
		req, ok, err := tx.Message(d.Message)
		if err != nil {
			return err
		}
		if !ok || req.Kind != KindRequest {
			return fmt.Errorf("%w: delivery %s is not a request's", ErrInvalid, delivery)
		}
		sum := replyDigest(d.ID, p)
		// before the lease: a retry after a lost answer gets its reply back, never ErrLeaseLost
		if old, ok, err := tx.Response(req.ID, s.Profile); err != nil {
			return err
		} else if ok {
			if !bytes.Equal(old.Digest, sum) || old.IdemKey != p.Key {
				return ErrKeyReused
			}
			out = old
			return nil
		}
		if _, ok, err := tx.MessageByKey(s.Profile, p.Key); err != nil {
			return err
		} else if ok {
			return ErrKeyReused
		}
		if err := ack(tx, d, lease); err != nil {
			return err
		}
		out = Message{ID: m.newID(), Sender: s.Profile, SenderSession: s.ID, IdemKey: p.Key, Digest: sum,
			Kind: KindResponse, Subject: p.Subject, Body: p.Body, ReplyTo: req.ID, State: MessageOpen,
			Created: tx.Now().UnixNano()}
		if err := tx.PutMessage(out); err != nil {
			return err
		}
		_, err = m.deliver(tx, out, req.Sender, "")
		return err
	})
	return out, err
}

func replyDigest(delivery string, p Reply) []byte {
	h := sha256.New()
	for _, s := range []string{delivery, p.Subject, p.Body} {
		_ = binary.Write(h, binary.BigEndian, uint64(len(s)))
		h.Write([]byte(s))
	}
	return h.Sum(nil)
}

// previewMax bounds a preview's count: past it the count says "at least".
const previewMax = 1000

// Inbox is a preview of a session's eligible mail: never leased.
type Inbox struct {
	Unread int // at most previewMax
	Items  []Item
}

// Preview is the credential's session's eligible mail, the first limit of it with its messages,
// leasing nothing (mail.inbox). It is a read, so expire does not run: eligibility itself leaves out
// a delivery whose message has expired (Tx.Eligible), before the count's bound.
func (m *Manager) Preview(ctx context.Context, credential string, limit int) (Inbox, error) {
	limit = min(max(limit, 0), previewMax)
	var out Inbox
	err := m.store.Read(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		ds, err := tx.Eligible(s.Profile, s.ID, tx.Now().UnixNano(), previewMax)
		if err != nil {
			return err
		}
		for _, d := range ds {
			msg, ok, err := tx.Message(d.Message)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("mail: delivery %s names no message %s", d.ID, d.Message)
			}
			out.Unread++
			if len(out.Items) < limit {
				out.Items = append(out.Items, Item{Delivery: d, Message: msg})
			}
		}
		return nil
	})
	return out, err
}
