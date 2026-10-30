package mail

import (
	"fmt"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// The mail module is crew's SessionEnder: the effects of a session's end, an adoption and a discard.
var _ crew.SessionEnder = (*Manager)(nil)

// mailTx is tx as the mail module's: the crew module calls these effects inside its own write, on
// the engine's one transaction, which is both. Any other is a wiring fault, answered as an error.
func mailTx(tx crew.Tx) (Tx, error) {
	t, ok := tx.(Tx)
	if !ok {
		return nil, fmt.Errorf("mail: %T is not a mail transaction", tx)
	}
	return t, nil
}

// EndSessionDeliveries is what follows a session's end, in the write that ended it (§4.3): every
// unhandled delivery bound to it fails (session_ended), and each sender is told. Its profile's
// unbound leases lapse as any lease does, and go to the profile's next session.
func (m *Manager) EndSessionDeliveries(ctx crew.Tx, s crew.Session, why string) error {
	tx, err := mailTx(ctx)
	if err != nil {
		return err
	}
	ds, err := tx.BoundTo(s.ID)
	if err != nil {
		return err
	}
	for _, d := range ds {
		d.Lease, d.LeaseUntil = "", nil
		if err := m.end(tx, d, Failed, WhySessionEnded); err != nil {
			return err
		}
	}
	return nil
}

// MergeDeliveries is adoption's mail half for one profile of the scope adopted (§4.3), in crew's
// write: from's unhandled deliveries move to into, or stay from's when into is "" (the profile
// moves whole). Session-bound ones fail (session_ended). Where into already has the message, from's
// copy is merged, and a failed copy of into's is reopened, its sender told. Every surviving row is
// normalized: profile-bound, queued, no lease.
func (m *Manager) MergeDeliveries(ctx crew.Tx, from, into string) error {
	tx, err := mailTx(ctx)
	if err != nil {
		return err
	}
	if err := m.expire(tx); err != nil {
		return err
	}
	ds, err := tx.Unhandled(from)
	if err != nil {
		return err
	}
	woke := false
	for _, d := range ds {
		switch {
		case d.Session != "":
			d.Lease, d.LeaseUntil = "", nil
			err = m.end(tx, d, Failed, WhySessionEnded)
		case into == "":
			err = m.normalize(tx, d, from)
			woke = true
		default:
			woke = true
			err = m.adoptOne(tx, d, into)
		}
		if err != nil {
			return err
		}
	}
	if !woke {
		return nil
	}
	if into == "" {
		into = from
	}
	return m.crew.RaisePending(tx, into)
}

func (m *Manager) adoptOne(tx Tx, d Delivery, into string) error {
	e, ok, err := tx.DeliveryOf(d.Message, into)
	if err != nil {
		return err
	}
	if !ok {
		return m.normalize(tx, d, into)
	}
	if e.State == Failed {
		if err := m.normalize(tx, e, into); err != nil {
			return err
		}
		if err := m.notifySender(tx, e, WhyReopened); err != nil {
			return err
		}
	}
	now := tx.Now().UnixNano()
	d.State, d.Why, d.DoneAt, d.Lease, d.LeaseUntil = Merged, "", &now, "", nil
	return tx.PutDelivery(d)
}

// normalize is the one way a surviving obligation is made receivable again (§4.3): bound to no
// session, queued, no lease or end; a row that moves takes the new recipient's next seq. Its
// attempts carry on, and its message's expiry stands.
func (m *Manager) normalize(tx Tx, d Delivery, to string) error {
	if to != d.Recipient {
		seq, err := tx.NextSeq(to)
		if err != nil {
			return err
		}
		d.Recipient, d.Seq = to, seq
	}
	d.Session, d.State, d.Lease, d.LeaseUntil, d.DoneAt, d.Why = "", Queued, "", nil, nil, ""
	return tx.PutDelivery(d)
}

// HasEligible reports whether profile has a delivery a new session of it may receive now: queued,
// unbound, and its message not past its expiry. crew.register seeds the session's wake with it (§4.4).
func (m *Manager) HasEligible(ctx crew.Tx, profile string) (bool, error) {
	tx, err := mailTx(ctx)
	if err != nil {
		return false, err
	}
	ds, err := tx.Eligible(profile, "", expireBatch)
	if err != nil {
		return false, err
	}
	now := tx.Now().UnixNano()
	for _, d := range ds {
		msg, ok, err := tx.Message(d.Message)
		if err != nil {
			return false, err
		}
		if ok && d.Session == "" && (msg.Expires == nil || *msg.Expires > now) {
			return true, nil
		}
	}
	return false, nil
}

// UnhandledCount is how many of profile's deliveries are queued or leased, for crew.orphans.
func (m *Manager) UnhandledCount(ctx crew.Tx, profile string) (int, error) {
	tx, err := mailTx(ctx)
	if err != nil {
		return 0, err
	}
	ds, err := tx.Unhandled(profile)
	return len(ds), err
}

// DiscardDeliveries fails every unhandled delivery of profile (discarded), each sender told, in
// crew.discard's write (§4.3).
func (m *Manager) DiscardDeliveries(ctx crew.Tx, profile string) error {
	tx, err := mailTx(ctx)
	if err != nil {
		return err
	}
	ds, err := tx.Unhandled(profile)
	if err != nil {
		return err
	}
	for _, d := range ds {
		d.Lease, d.LeaseUntil = "", nil
		if err := m.end(tx, d, Failed, WhyDiscarded); err != nil {
			return err
		}
	}
	return nil
}
