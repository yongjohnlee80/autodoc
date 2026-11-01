package mail

import (
	"errors"
	"fmt"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// resolveRecipient is the profile a new delivery to profile goes to: profile itself while active,
// else the survivor its merged_into links lead to, at most mergeHops of them
// (docs/decisions/crew.md#durable-mail). A missing profile, a cycle, a longer chain or a merge to
// nowhere is errRecipientGone, and so is an active profile of a scope that is gone (a discard ended
// it): nothing can ever receive there, so no new delivery may wait there.
func resolveRecipient(tx Tx, profile string) (string, error) {
	seen := map[string]bool{}
	for p := profile; ; {
		row, ok, err := tx.Profile(p)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errRecipientGone
		}
		if row.Status == crew.ProfileActive {
			gone, err := scopeGone(tx, row.Scope)
			switch {
			case err != nil:
				return "", err
			case gone:
				return "", errRecipientGone
			}
			return p, nil
		}
		if seen[p] || len(seen) >= mergeHops || row.MergedInto == "" {
			return "", errRecipientGone
		}
		seen[p] = true
		p = row.MergedInto
	}
}

// scopeGone reports whether scope has ended for good (crew's 'gone': discarded, or adopted away).
func scopeGone(tx Tx, scope string) (bool, error) {
	sc, ok, err := tx.Scope(scope)
	if err != nil {
		return false, err
	}
	return ok && sc.State == crew.ScopeGone, nil
}

// deliver inserts msg's delivery to profile (bound to session when it is not ""), the one way every
// new delivery is made. A profile that resolves to nothing gets a failed delivery
// (recipient_gone, on the original id) and its sender is told; the caller's write still commits.
func (m *Manager) deliver(tx Tx, msg Message, profile, session string) (Delivery, error) {
	now := tx.Now().UnixNano()
	to, err := profile, error(nil)
	if session == "" {
		to, err = resolveRecipient(tx, profile)
	}
	gone := errors.Is(err, errRecipientGone)
	if err != nil && !gone {
		return Delivery{}, err
	}
	if gone {
		to = profile
	}
	seq, err := tx.NextSeq(to)
	if err != nil {
		return Delivery{}, err
	}
	d := Delivery{ID: m.newID(), Message: msg.ID, Recipient: to, Session: session, Seq: seq, State: Queued}
	if gone {
		d.State, d.Why, d.DoneAt = Failed, WhyRecipientGone, &now
	}
	if err := tx.PutDelivery(d); err != nil {
		return Delivery{}, err
	}
	if gone {
		return d, m.notifySender(tx, d, WhyRecipientGone)
	}
	return d, m.eligible(tx, msg, d)
}

// eligible tells whoever waits on d that it can be received: the recipient's live session through
// crew's wake generation, or the human through its feed.
func (m *Manager) eligible(tx Tx, msg Message, d Delivery) error {
	user, err := isUser(tx, d.Recipient)
	if err != nil {
		return err
	}
	if user {
		return tx.AddInboxChange(feedKind(msg), msg.ID)
	}
	return m.crew.RaisePending(tx, d.Recipient)
}

// feedKind is the human feed's change for a message reaching the human.
func feedKind(msg Message) string {
	switch msg.Kind {
	case KindRequest:
		return ChangeRequest
	case KindEvent:
		return ChangeUndeliverable
	}
	return ChangeReport
}

func isUser(tx Tx, profile string) (bool, error) {
	p, ok, err := tx.Profile(profile)
	if err != nil || !ok {
		return false, err
	}
	return p.Scope == GlobalScope && p.Codename == UserCodename, nil
}

// notifySender tells d's sender that d ended without being handled, for why
// (docs/decisions/crew.md#durable-mail): one event from the daemon's profile, keyed so that a
// retried write or a replay makes no second one. A notice itself never bounces: an event's failed
// delivery only shows in the human's history.
func (m *Manager) notifySender(tx Tx, d Delivery, why string) error {
	msg, ok, err := tx.Message(d.Message)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("mail: delivery %s names no message %s", d.ID, d.Message)
	}
	if msg.Kind == KindEvent {
		return tx.AddInboxChange(ChangeUndeliverable, d.Message)
	}
	key := "event:" + why + ":" + d.ID
	if _, ok, err := tx.MessageByKey(DaemonProfile, key); err != nil || ok {
		return err
	}
	ev := Message{ID: m.newID(), Sender: DaemonProfile, IdemKey: key, Digest: []byte(key), Kind: KindEvent,
		Subject: why, Body: why, ReplyTo: d.Message, State: MessageOpen, Created: tx.Now().UnixNano()}
	if err := tx.PutMessage(ev); err != nil {
		return err
	}
	_, err = m.deliver(tx, ev, msg.Sender, "")
	return err
}

// expire makes the time-driven changes real (docs/decisions/crew.md#the-crew-and-mail-tables),
// first in every write that reads eligibility: a lapsed lease is queued again (its recipient
// woken), or failed after MaxAttempts; a delivery past its message's expiry is expired, each one's
// sender told; then a delivery not received in time falls back to its recipient's host
// (docs/decisions/crew.md#the-fallback-exactly-once).
func (m *Manager) expire(tx Tx) error {
	now := tx.Now().UnixNano()
	for {
		due, err := tx.LeasesDue(now, expireBatch)
		if err != nil {
			return err
		}
		for _, d := range due {
			d.Attempts++
			d.Lease, d.LeaseUntil = "", nil
			if d.Attempts >= MaxAttempts {
				err = m.end(tx, d, Failed, WhyAttempts)
			} else {
				d.State = Queued
				if err = tx.PutDelivery(d); err == nil {
					err = m.crew.RaisePending(tx, d.Recipient)
				}
			}
			if err != nil {
				return err
			}
		}
		if len(due) < expireBatch {
			break
		}
	}
	for {
		gone, err := tx.Expiring(now, expireBatch)
		if err != nil {
			return err
		}
		for _, d := range gone {
			if err := m.expireOne(tx, d); err != nil {
				return err
			}
		}
		if len(gone) < expireBatch {
			return m.fallBackDue(tx)
		}
	}
}

// expireOne ends d as expired; a request or report to the human that expires also leaves the feed.
func (m *Manager) expireOne(tx Tx, d Delivery) error {
	d.Lease, d.LeaseUntil = "", nil
	if err := m.end(tx, d, Expired, WhyExpired); err != nil {
		return err
	}
	user, err := isUser(tx, d.Recipient)
	if err != nil || !user {
		return err
	}
	msg, ok, err := tx.Message(d.Message)
	if err != nil || !ok || msg.State != MessageOpen {
		return err
	}
	msg.State = MessageExpired
	if err := tx.PutMessage(msg); err != nil {
		return err
	}
	return tx.AddInboxChange(ChangeExpired, msg.ID)
}

// end puts d in a terminal state for why, and tells its sender.
func (m *Manager) end(tx Tx, d Delivery, state, why string) error {
	now := tx.Now().UnixNano()
	d.State, d.Why, d.DoneAt = state, why, &now
	if err := tx.PutDelivery(d); err != nil {
		return err
	}
	return m.notifySender(tx, d, why)
}
