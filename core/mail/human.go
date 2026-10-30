package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// The human's operations on mail (docs/decisions/crew.md#the-human-inbox): inbox.reply,
// inbox.dismiss and inbox.ack. They are mail's state transitions, so they run here, inside mail's
// own write, with expire, deliver and notifySender; core/inbox serves them under the user's rules
// (no credential).

var (
	// ErrAlreadyAnswered is a reply or dismiss of a request the human already answered or
	// dismissed; the message says which, and when.
	ErrAlreadyAnswered = errs.Sentinel(errs.ErrPrecondition, "mail: the request is already answered")

	// ErrNotARequest is a reply or dismiss of a message that is no request to the human.
	ErrNotARequest = errs.Sentinel(errs.ErrInvalidArgument, "mail: not a request to the user")

	// ErrExpired is a reply, dismiss or ack of a message that expired first.
	ErrExpired = errs.Sentinel(errs.ErrPrecondition, "mail: the message has expired")
)

// EnsureReserved makes the reserved profiles a fresh store lacks: the daemon's, which sends every
// generated notice, and the human's, codename user in the global scope
// (docs/decisions/crew.md#durable-mail). It is idempotent, and a store that already has an active
// user profile keeps it.
func (m *Manager) EnsureReserved(ctx context.Context) error {
	return m.store.Write(ctx, func(tx Tx) error {
		now := tx.Now().UnixNano()
		if _, ok, err := tx.Profile(DaemonProfile); err != nil {
			return err
		} else if !ok {
			if err := tx.PutProfile(crew.Profile{ID: DaemonProfile, Scope: GlobalScope, Codename: "autodoc",
				Kind: "autodoc", Status: crew.ProfileActive, Created: now}); err != nil {
				return err
			}
		}
		if _, ok, err := tx.ActiveProfile(GlobalScope, UserCodename); err != nil || ok {
			return err
		}
		return tx.PutProfile(crew.Profile{ID: UserProfile, Scope: GlobalScope, Codename: UserCodename,
			Kind: UserCodename, Status: crew.ProfileActive, Created: now})
	})
}

// keepExpiry makes an expiry real when an operation was refused for it. Each human operation runs
// expire inside its own write, so an expiry is caught however late it falls, but the refusal rolls
// that write back. Left so, a request refused as expired would stay open, unjournaled (every
// window's toast up) and its sender untold, until some other write expired it. So after an
// ErrExpired the expiry is committed in a write of its own, and the refusal still answered.
func (m *Manager) keepExpiry(ctx context.Context, err error) error {
	if !errors.Is(err, ErrExpired) {
		return err
	}
	if werr := m.store.Write(ctx, func(tx Tx) error { return m.expire(tx) }); werr != nil {
		return werr
	}
	return err
}

// userProfile is the human's profile id.
func userProfile(tx Tx) (string, error) {
	p, ok, err := tx.ActiveProfile(GlobalScope, UserCodename)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("mail: the store has no user profile (EnsureReserved has not run)")
	}
	return p.ID, nil
}

// toUser is message's delivery to the human, refused as errNot when there is none.
func toUser(tx Tx, user, message string, errNot error) (Message, Delivery, error) {
	msg, ok, err := tx.Message(message)
	if err != nil {
		return Message{}, Delivery{}, err
	}
	if !ok {
		return Message{}, Delivery{}, fmt.Errorf("%w: no message %s", errNot, message)
	}
	d, ok, err := tx.DeliveryOf(message, user)
	if err != nil {
		return Message{}, Delivery{}, err
	}
	if !ok {
		return Message{}, Delivery{}, fmt.Errorf("%w: message %s was not sent to the user", errNot, message)
	}
	return msg, d, nil
}

// openRequest is message as a request to the human still open, or why it can't be answered.
func openRequest(tx Tx, user, message string) (Message, Delivery, error) {
	req, d, err := toUser(tx, user, message, ErrNotARequest)
	switch {
	case err != nil:
		return Message{}, Delivery{}, err
	case req.Kind != KindRequest:
		return Message{}, Delivery{}, fmt.Errorf("%w: message %s is a %s", ErrNotARequest, message, req.Kind)
	case req.State == MessageExpired:
		return Message{}, Delivery{}, ErrExpired
	case req.State != MessageOpen:
		at := ""
		if d.DoneAt != nil {
			at = " at " + time.Unix(0, *d.DoneAt).UTC().Format(time.RFC3339)
		}
		return Message{}, Delivery{}, fmt.Errorf("%w: %s%s", ErrAlreadyAnswered, req.State, at)
	}
	return req, d, nil
}

// closeDelivery ends the human's delivery d: acked, done now, so it reaches retention.
func closeDelivery(tx Tx, d Delivery) error {
	now := tx.Now().UnixNano()
	d.State, d.DoneAt, d.Lease, d.LeaseUntil = Acked, &now, "", nil
	return tx.PutDelivery(d)
}

// Answer is inbox.reply: the human's response to request message, in one write
// (docs/decisions/crew.md#the-human-inbox). Any window may answer any request, and the first answer
// wins. key is bound to the request and the body: an exact replay after a lost answer returns the
// same response (even once the request is answered), and the key with another body or request is
// ErrKeyReused.
func (m *Manager) Answer(ctx context.Context, message, body, key string) (Message, error) {
	switch {
	case len(body) > MaxBody:
		return Message{}, fmt.Errorf("%w: %d bytes, at most %d", ErrTooLarge, len(body), MaxBody)
	case key == "":
		return Message{}, fmt.Errorf("%w: an empty key", ErrInvalid)
	}
	var out Message
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := m.expire(tx); err != nil {
			return err
		}
		user, err := userProfile(tx)
		if err != nil {
			return err
		}
		sum := answerDigest(message, body)
		if old, ok, err := tx.MessageByKey(user, key); err != nil {
			return err
		} else if ok {
			if old.ReplyTo != message || !bytes.Equal(old.Digest, sum) {
				return ErrKeyReused
			}
			out = old
			return nil
		}
		req, d, err := openRequest(tx, user, message)
		if err != nil {
			return err
		}
		req.State = MessageAnswered
		if err := tx.PutMessage(req); err != nil {
			return err
		}
		if err := closeDelivery(tx, d); err != nil {
			return err
		}
		out = Message{ID: m.newID(), Sender: user, IdemKey: key, Digest: sum, Kind: KindResponse, Body: body,
			ReplyTo: req.ID, State: MessageOpen, Created: tx.Now().UnixNano()}
		if err := tx.PutMessage(out); err != nil {
			return err
		}
		if _, err := m.deliver(tx, out, req.Sender, ""); err != nil {
			return err
		}
		return tx.AddInboxChange(ChangeAnswered, req.ID)
	})
	return out, m.keepExpiry(ctx, err)
}

func answerDigest(message, body string) []byte {
	h := sha256.New()
	for _, s := range []string{message, body} {
		_ = binary.Write(h, binary.BigEndian, uint64(len(s)))
		h.Write([]byte(s))
	}
	return h.Sum(nil)
}

// Dismiss is inbox.dismiss: the human declines request message, and its sender is told, in one
// write (docs/decisions/crew.md#the-human-inbox). A second dismiss of a dismissed request is a
// replay, and answers ok.
func (m *Manager) Dismiss(ctx context.Context, message string) error {
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := m.expire(tx); err != nil {
			return err
		}
		user, err := userProfile(tx)
		if err != nil {
			return err
		}
		if cur, ok, err := tx.Message(message); err != nil {
			return err
		} else if ok && cur.Kind == KindRequest && cur.State == MessageDismissed {
			return nil // a replay after a lost answer
		}
		req, d, err := openRequest(tx, user, message)
		if err != nil {
			return err
		}
		req.State = MessageDismissed
		if err := tx.PutMessage(req); err != nil {
			return err
		}
		if err := closeDelivery(tx, d); err != nil {
			return err
		}
		if err := m.notifySender(tx, d, WhyDismissed); err != nil {
			return err
		}
		return tx.AddInboxChange(ChangeDismissed, req.ID)
	})
	return m.keepExpiry(ctx, err)
}

// Read is inbox.ack: the human has read report message (docs/decisions/crew.md#the-human-inbox). A
// request is answered or dismissed, never acked; a second ack is a replay, and answers ok.
func (m *Manager) Read(ctx context.Context, message string) error {
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := m.expire(tx); err != nil {
			return err
		}
		user, err := userProfile(tx)
		if err != nil {
			return err
		}
		msg, d, err := toUser(tx, user, message, ErrInvalid)
		switch {
		case err != nil:
			return err
		case msg.Kind == KindRequest:
			return fmt.Errorf("%w: message %s is a request: answer or dismiss it", ErrInvalid, message)
		case msg.State == MessageRead:
			return nil
		case msg.State == MessageExpired:
			return ErrExpired
		}
		msg.State = MessageRead
		if err := tx.PutMessage(msg); err != nil {
			return err
		}
		if err := closeDelivery(tx, d); err != nil {
			return err
		}
		return tx.AddInboxChange(ChangeRead, msg.ID)
	})
	return m.keepExpiry(ctx, err)
}
