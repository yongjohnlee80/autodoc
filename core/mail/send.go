package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// Send is what a sender asks of mail.send.
type Send struct {
	To      []string // "name" in the sender's scope, "name@scope", "user", or "session:<id>"
	Kind    string   // message or request; "" is message
	Subject string
	Body    string
	Key     string        // the idempotency key: the same key answers the first send
	TTL     time.Duration // 0 never expires
	// Fire is fired or nothing (SendAsUser alone, to one recipient): the send stands once the
	// recipient's doorbell rang for it, and is withdrawn otherwise (ErrUnreachable), so a window
	// that then types the text into the agent's prompt leaves nothing in the daemon to come twice.
	Fire bool
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
	From     string // the sender as the receiver would address a reply: see fromAddress
}

// fromAddress is sender as an address the receiver (in scope) can send to: the bare codename in the
// same scope; codename@project in another AutoVim scope; codename@<scope id> otherwise; "user" for
// the human; "autodoc" for the daemon's notices. The sender is resolved as a new delivery to it
// would be (resolveRecipient), so a profile merged since it sent names its survivor, and a reply
// to the address reaches it; the message keeps the original sender for its history. A sender that
// resolves to nothing (gone, missing, a chain too long) is still named, for display, as it was.
func fromAddress(tx Tx, scope, sender string) (string, error) {
	from, _, err := senderOf(tx, scope, sender)
	return from, err
}

// senderOf is fromAddress, and the profile it names (the zero Profile for the daemon, or a sender
// that resolves to nothing).
func senderOf(tx Tx, scope, sender string) (string, crew.Profile, error) {
	if sender == DaemonProfile {
		return "autodoc", crew.Profile{}, nil
	}
	who := sender
	if to, err := resolveRecipient(tx, sender); err == nil {
		who = to
	} else if !errors.Is(err, errRecipientGone) {
		return "", crew.Profile{}, err
	}
	p, ok, err := tx.Profile(who)
	if err != nil || !ok {
		return sender, crew.Profile{}, err
	}
	switch {
	case p.Scope == GlobalScope && p.Codename == UserCodename:
		return UserCodename, p, nil
	case p.Scope == scope:
		return p.Codename, p, nil
	}
	if project, ok := strings.CutPrefix(p.Scope, "autovim:"); ok {
		return p.Codename + "@" + project, p, nil
	}
	return p.Codename + "@" + p.Scope, p, nil
}

// receiverScope is the scope of s's profile, against which fromAddress shortens a sender.
func receiverScope(tx Tx, s crew.Session) (string, error) {
	p, _, err := tx.Profile(s.Profile)
	return p.Scope, err
}

// Send stores a message and its deliveries in one write, from credential's session
// (docs/decisions/crew.md#durable-mail). A key the sender already used answers that send while its
// message is retained, when the content is the same, and ErrKeyReused when it is not.
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
		from, ok, err := tx.Profile(s.Profile)
		if err != nil {
			return err
		}
		if !ok {
			return crew.ErrBadCredential
		}
		out, err = m.send(tx, s.Profile, s.ID, from.Scope, p)
		return err
	})
	return out, err
}

// SendAsUser is inbox.send: the user's own message or request, from the window that holds scope
// (run's process at epoch, checked in the write, as a host verb is), to addresses resolved in that
// scope, in one write (docs/decisions/crew.md#the-human-inbox). The sender is the user's profile,
// which only the daemon sets: no session's credential can send as the user, so an agent tells the
// user's own instruction from a peer's by the sender alone. Its key is the user's, as Send's is a
// session's profile's.
func (m *Manager) SendAsUser(ctx context.Context, scope, run string, epoch int64, p Send) (Sent, error) {
	if err := checkSend(p); err != nil {
		return Sent{}, err
	}
	if p.Fire && len(p.To) != 1 {
		return Sent{}, fmt.Errorf("%w: a send to fire names one recipient", ErrInvalid)
	}
	var out Sent
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := m.crew.GuardHost(tx, scope, run, epoch); err != nil {
			return err
		}
		user, err := userProfile(tx)
		if err != nil {
			return err
		}
		out, err = m.send(tx, user, "", scope, p)
		return err
	})
	if err != nil || !p.Fire {
		return out, err
	}
	return out, m.fire(ctx, out)
}

// UnverifiedAssigner opens an assignment's mail that autodoc sends because who made the assignment
// couldn't be verified (SendAsAssigner): an agent's call, never the user's.
const UnverifiedAssigner = "Who made this assignment couldn't be verified: an agent made it, not the user."

// SendAsAssigner is inbox.forward: an assignment's mail from the window that holds scope (run's
// process at epoch, guarded as a host verb is), sent from the one by names, the assignment's maker
// as the daemon recorded it (docs/decisions/todos.md#an-assignment-says-who-made-it), never from
// someone the window names. by is "" for the user's surface: from the user, as SendAsUser. An
// agent's codename@scope: from that agent's profile, so its recipient reads it as a peer's and a
// reply reaches the agent. The unverified marker, or an agent no longer in the crew: from autodoc,
// the body opened by UnverifiedAssigner. Never fired: it is queued as any mail. The key is the
// sender's, so a retry answers the first send.
func (m *Manager) SendAsAssigner(ctx context.Context, scope, run string, epoch int64, by string, p Send) (Sent, error) {
	p.Fire = false
	if err := checkSend(p); err != nil {
		return Sent{}, err
	}
	var out Sent
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := m.crew.GuardHost(tx, scope, run, epoch); err != nil {
			return err
		}
		sender, err := assigner(tx, by)
		if err != nil {
			return err
		}
		if sender == DaemonProfile {
			p.Body = UnverifiedAssigner + "\n\n" + p.Body
			if err := checkSend(p); err != nil {
				return err
			}
		}
		out, err = m.send(tx, sender, "", scope, p)
		return err
	})
	return out, err
}

// assigner is the profile an assignment by by is sent from (SendAsAssigner): the user's for "",
// the agent's active profile for its codename@scope, and the daemon's for anything else.
func assigner(tx Tx, by string) (string, error) {
	if by == "" {
		return userProfile(tx)
	}
	if codename, scope, ok := strings.Cut(by, "@"); ok && by != module.CallerUnverified && codename != "" && codename != UserCodename && scope != "" {
		p, found, err := tx.ActiveProfile(scope, codename)
		if err != nil {
			return "", err
		}
		if found && p.ID != DaemonProfile && p.ID != UserProfile {
			return p.ID, nil
		}
	}
	return DaemonProfile, nil
}

// fireBound is how long a send to fire waits for its doorbell: the call and an attempt the loop
// already holds alike.
const fireBound = 5 * time.Second

// fire rings sent's one recipient's doorbell (crew's Fire, bounded by fireBound): fired, the send
// stands; not, its delivery is handed over (handOver): ErrHandedOver when its host carries it as a
// job, else ErrUnreachable, nothing left. A delivery the recipient leased or acked meanwhile was received: the send stands
// whatever the doorbell did. A replay of a send already withdrawn is ErrUnreachable again, never a
// send that stands.
func (m *Manager) fire(ctx context.Context, sent Sent) error {
	if len(sent.Deliveries) != 1 {
		return fmt.Errorf("mail: a send to fire has %d deliveries", len(sent.Deliveries))
	}
	d := sent.Deliveries[0]
	if d.State == Withdrawn { // a replay answers as the first send did
		if d.Why == WhyFallback {
			return ErrHandedOver
		}
		return ErrUnreachable
	}
	fctx, cancel := context.WithTimeout(ctx, fireBound)
	fired, ferr := m.crew.Fire(fctx, d.Recipient)
	cancel()
	if fired && ferr == nil {
		return nil
	}
	// handed over, the send is unreachable whatever the doorbell's error was: a host that takes
	// fallbacks delivers the job, and the caller must not send it some other way; else nothing is
	// left, and the caller may. A hand-over that fails leaves the delivery queued, its outcome
	// unknown to the caller (the store's error): the next wake, or expire's fallback, delivers it.
	received, handed, err := m.handOver(context.WithoutCancel(ctx), d)
	switch {
	case err != nil:
		return err
	case received:
		return nil
	case handed:
		return ErrHandedOver
	}
	return ErrUnreachable
}

// handOver ends a send to fire whose doorbell never rang, in a write of its own: its recipient's
// host carries it as a fallback job when that host takes them (TakesFallbacks: handed), else it is
// withdrawn and the window that sent it types it, as a host that doesn't take jobs always has.
// received is true, and nothing changes, when the recipient leased or acked it first.
func (m *Manager) handOver(ctx context.Context, d Delivery) (received, handed bool, err error) {
	err = m.store.Write(ctx, func(tx Tx) error {
		takes, err := takesFallbacks(tx, d.Recipient)
		if err != nil {
			return err
		}
		if takes {
			received, err = m.fallBack(tx, d)
			handed = !received
			return err
		}
		received, err = m.withdraw(tx, d)
		return err
	})
	return received, handed, err
}

// withdraw ends delivery d, still queued, as Withdrawn: done, never receivable. received is true,
// and nothing changes, when the recipient leased or acked it first.
func (m *Manager) withdraw(tx Tx, d Delivery) (received bool, err error) {
	cur, ok, err := tx.DeliveryOf(d.Message, d.Recipient)
	if err != nil {
		return false, err
	}
	if !ok || cur.State != Queued {
		return ok && (cur.State == Leased || cur.State == Acked), nil
	}
	now := tx.Now().UnixNano()
	cur.State, cur.DoneAt, cur.Why = Withdrawn, &now, WhyWithdrawn
	if err := tx.PutDelivery(cur); err != nil {
		return false, err
	}
	return false, m.settleIfDrained(tx, cur.Recipient)
}

// send is a send's write, in tx, from profile sender (its session senderSession, "" for the user)
// to addresses resolved in scope: a key sender already used answers that send while its message is
// retained, when the content is the same, and ErrKeyReused when it is not.
func (m *Manager) send(tx Tx, sender, senderSession, scope string, p Send) (Sent, error) {
	sum := sendDigest(p)
	if old, ok, err := tx.MessageByKey(sender, p.Key); err != nil {
		return Sent{}, err
	} else if ok {
		if !bytes.Equal(old.Digest, sum) {
			return Sent{}, ErrKeyReused
		}
		ds, err := tx.DeliveriesOf(old.ID)
		return Sent{Message: old, Deliveries: ds}, err
	}
	to, err := addresses(tx, scope, p.To)
	if err != nil {
		return Sent{}, err
	}
	for _, a := range to {
		if !p.Fire {
			break
		}
		// no doorbell to ring: refused before anything is stored
		if ok, err := m.crew.Reachable(tx, a.profile); err != nil {
			return Sent{}, err
		} else if !ok {
			return Sent{}, ErrUnreachable
		}
	}
	now := tx.Now()
	msg := Message{ID: m.newID(), Sender: sender, SenderSession: senderSession, IdemKey: p.Key, Digest: sum,
		Kind: p.Kind, Subject: p.Subject, Body: p.Body, State: MessageOpen, Created: now.UnixNano()}
	if msg.Kind == "" {
		msg.Kind = KindMessage
	}
	if p.TTL > 0 {
		at := now.Add(p.TTL).UnixNano()
		msg.Expires = &at
	}
	if err := tx.PutMessage(msg); err != nil {
		return Sent{}, err
	}
	out := Sent{Message: msg}
	for _, a := range to {
		d, err := m.deliver(tx, msg, a.profile, a.session)
		if err != nil {
			return Sent{}, err
		}
		out.Deliveries = append(out.Deliveries, d)
	}
	return out, nil
}

func checkSend(p Send) error {
	switch {
	case len(p.Body) > MaxBody:
		return fmt.Errorf("%w: %d bytes, at most %d", ErrTooLarge, len(p.Body), MaxBody)
	case len(p.Subject) > MaxSubject:
		return fmt.Errorf("%w: a subject of %d bytes, at most %d", ErrInvalid, len(p.Subject), MaxSubject)
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

// addresses resolves every address once, in scope; any that resolves to nothing is
// ErrNoRecipient, and the daemon's own profile is never a recipient. A message has one delivery per
// recipient profile (mail_delivery's UNIQUE (message, recipient)): the same address twice is one,
// and two addresses binding one profile differently (gold and session:<gold's>, or two of its
// sessions) are refused, never silently narrowed or widened.
func addresses(tx Tx, scope string, to []string) ([]address, error) {
	var out []address
	bound := map[string]string{} // profile → the session its address binds ("" for any)
	for _, t := range to {
		a, err := resolveAddress(tx, scope, t)
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

// uniqueProfileNamed is the one active profile of codename name in a scope other than scope, live
// (its scope not gone) and not the daemon's: ok false when there is none, ErrAmbiguous naming each
// as name@scope when two or more have it, so the sender can say which.
func uniqueProfileNamed(tx Tx, scope, name string) (crew.Profile, bool, error) {
	found, err := profilesNamedElsewhere(tx, scope, name)
	if err != nil {
		return crew.Profile{}, false, err
	}
	switch len(found) {
	case 0:
		return crew.Profile{}, false, nil
	case 1:
		return found[0], true, nil
	}
	return crew.Profile{}, false, fmt.Errorf("%w: %s is %s", ErrAmbiguous, name, strings.Join(addressesOf(found), " or "))
}

// profilesNamedElsewhere are the active profiles of codename name in scopes other than scope, live
// (their scope not gone) and not the daemon's: the ones a bare name the sender's scope lacks may
// reach.
func profilesNamedElsewhere(tx Tx, scope, name string) ([]crew.Profile, error) {
	all, err := tx.ActiveProfiles()
	if err != nil {
		return nil, err
	}
	var found []crew.Profile
	for _, p := range all {
		if p.Codename != name || p.Scope == scope || p.ID == DaemonProfile {
			continue
		}
		gone, err := scopeGone(tx, p.Scope)
		if err != nil {
			return nil, err
		}
		if !gone {
			found = append(found, p)
		}
	}
	return found, nil
}

// addressesOf is each profile as the address that reaches it, name@scope (an AutoVim project's by
// its name), sorted.
func addressesOf(ps []crew.Profile) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = addressOf(p)
	}
	slices.Sort(names)
	return names
}

// addressOf is p as the address that reaches it from any scope: codename@scope, an AutoVim
// project's by its name (jarvis@mcat).
func addressOf(p crew.Profile) string {
	if proj, ok := strings.CutPrefix(p.Scope, "autovim:"); ok {
		return p.Codename + "@" + proj
	}
	return p.Codename + "@" + p.Scope
}

// ReachState is where a bare name sent from a scope goes.
type ReachState string

const (
	ReachOwn       ReachState = "own"       // the sender's scope has a profile of it
	ReachOther     ReachState = "other"     // the one other scope's profile of it (Reach.Address)
	ReachNone      ReachState = "none"      // no profile of it anywhere: a send is ErrNoRecipient
	ReachAmbiguous ReachState = "ambiguous" // two or more other scopes': a send is ErrAmbiguous
)

// Reach is where mail sent to a bare name from a scope goes, resolved as a send resolves it
// (resolveAddress), without sending.
type Reach struct {
	State      ReachState
	Address    string   // ReachOther: the address that reaches it, name@scope
	Candidates []string // ReachAmbiguous: each one's address, sorted
}

// Reach is where mail sent to the bare name from scope would go: its own scope's profile, the one
// other scope's, none, or two or more (each named, so the sender can say which). A read: it sends
// and records nothing. The user's codename and a name with an @ or a session: prefix are not bare,
// and are ErrInvalid.
func (m *Manager) Reach(ctx context.Context, scope, name string) (Reach, error) {
	if name == "" || name == UserCodename || strings.Contains(name, "@") || strings.HasPrefix(name, "session:") {
		return Reach{}, fmt.Errorf("%w: %q is not a bare codename", ErrInvalid, name)
	}
	var out Reach
	err := m.store.Read(ctx, func(tx Tx) error {
		p, ok, err := tx.ActiveProfile(scope, name)
		if err != nil {
			return err
		}
		if ok && p.ID == DaemonProfile {
			out = Reach{State: ReachNone} // as a send: the daemon's own profile is never a recipient
			return nil
		}
		if ok {
			gone, err := scopeGone(tx, p.Scope)
			if err != nil {
				return err
			}
			if gone {
				out = Reach{State: ReachNone} // a send is refused: its scope is gone
			} else {
				out = Reach{State: ReachOwn}
			}
			return nil
		}
		found, err := profilesNamedElsewhere(tx, scope, name)
		if err != nil {
			return err
		}
		switch len(found) {
		case 0:
			out = Reach{State: ReachNone}
		case 1:
			out = Reach{State: ReachOther, Address: addressOf(found[0])}
		default:
			out = Reach{State: ReachAmbiguous, Candidates: addressesOf(found)}
		}
		return nil
	})
	return out, err
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
		scope = "autovim:" + in // jarvis@mcat: an AutoVim project by name (docs/decisions/crew.md#scopes-and-fenced-ownership)
	}
	p, ok, err := tx.ActiveProfile(scope, name)
	if err == nil && !ok && !cross && name != "" && name != UserCodename {
		// one daemon is one crew (docs/decisions/crew.md#one-daemon-is-one-crew): a bare name the
		// sender's scope has no profile of reaches the one other scope's that has it
		p, ok, err = uniqueProfileNamed(tx, scope, name)
	}
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
// LeaseFor (docs/decisions/crew.md#durable-mail). More is true while eligible mail remains: the
// guide drains until it is false.
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
		scope, err := receiverScope(tx, s)
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
			d.State, d.Lease, d.LeaseUntil = Leased, m.newID(), &until
			if d.ReadAt == nil {
				at := now.UnixNano()
				d.ReadAt = &at
			}
			if err := tx.PutDelivery(d); err != nil {
				return err
			}
			from, err := fromAddress(tx, scope, msg.Sender)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, Item{Delivery: d, Message: msg, From: from})
		}
		return nil
	})
	return out, err
}

// owned is the delivery id names when session s may act on it: the recipient's, unbound or bound
// to s. id is the delivery's, or the id of a message s's profile has a delivery of when that
// delivery holds exactly lease (docs/decisions/crew.md#durable-mail): the lease is one receive's,
// so holding it proves the hold, and the message's id with it names the same item; viaMessage says
// it was named so. Anything else answers as a lost lease, which says nothing of another's mail.
func owned(tx Tx, s crew.Session, id, lease string) (d Delivery, viaMessage bool, err error) {
	mine := func(d Delivery) bool { return d.Recipient == s.Profile && (d.Session == "" || d.Session == s.ID) }
	d, ok, err := tx.Delivery(id)
	if err != nil {
		return Delivery{}, false, err
	}
	if ok && mine(d) {
		return d, false, nil
	}
	if !ok && lease != "" {
		if d, ok, err = tx.DeliveryOf(id, s.Profile); err != nil {
			return Delivery{}, false, err
		}
		if ok && mine(d) && d.Lease == lease {
			return d, true, nil
		}
	}
	return Delivery{}, false, ErrLeaseLost
}

// ReplyKey is the idempotency key the CLI gives a reply to delivery: one response per request's
// delivery, so a retry after a lost answer gets that response back. A reply that names the
// delivery by its message's id (owned) is keyed as the delivery's.
func ReplyKey(delivery string) string { return "reply:" + delivery }

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
		d, _, err := owned(tx, s, delivery, lease)
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

// Show is a delivery the caller holds, read again: its message, under the lease it holds, with the
// lease and its attempts unchanged (mail.show).
// The caller's own delivery that is done says so: acked (or replied to) is ErrDone; failed, expired,
// merged, withdrawn (handed to its host as a fallback), or its message past its expiry with no write
// yet to expire it, is ErrEnded. Another session's, another lease, or one that lapsed on a live
// message is ErrLeaseLost. It writes nothing: what it reads as lapsed or ended is left for the next
// write to settle.
func (m *Manager) Show(ctx context.Context, credential, delivery, lease string) (Item, error) {
	var out Item
	err := m.store.Read(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		d, _, err := owned(tx, s, delivery, lease)
		if err != nil {
			return err
		}
		switch d.State { // the caller's own delivery, done: say what became of it
		case Acked:
			return ErrDone
		case Failed, Expired, Merged, Withdrawn:
			return ErrEnded
		}
		msg, ok, err := tx.Message(d.Message)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("mail: delivery %s names no message %s", d.ID, d.Message)
		}
		if msg.Expires != nil && *msg.Expires <= tx.Now().UnixNano() {
			return ErrEnded // the message ended, though no write has expired its delivery yet
		}
		if d.State != Leased || d.Lease != lease || lease == "" || d.LeaseUntil == nil || *d.LeaseUntil <= tx.Now().UnixNano() {
			return ErrLeaseLost
		}
		scope, err := receiverScope(tx, s)
		if err != nil {
			return err
		}
		from, err := fromAddress(tx, scope, msg.Sender)
		if err != nil {
			return err
		}
		out = Item{Delivery: d, Message: msg, From: from}
		return nil
	})
	return out, err
}

// Answered is what an ack or a reply did: the delivery its handle resolved to (the delivery's own
// id, also when it was named by its message's id) and its state then.
type Answered struct {
	Delivery string
	State    AnswerState
}

// AnswerState is what an ack or a reply found and did.
type AnswerState string

const (
	AnsweredAcked        AnswerState = "acked"         // this ack wrote the delivery done
	AnsweredAlreadyAcked AnswerState = "already_acked" // an ack of the same lease had: a replay
	AnsweredReplied      AnswerState = "replied"       // the request's response is sent (this time or before)
)

// Ack is done with a delivery under its lease, and answers the delivery it resolved to and whether
// this ack wrote it done or found it so (a replay of the same lease answers already_acked). Another
// lease, or one that lapsed, is ErrLeaseLost.
func (m *Manager) Ack(ctx context.Context, credential, delivery, lease string) (Answered, error) {
	var out Answered
	err := m.store.Write(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		if err := m.expire(tx); err != nil {
			return err
		}
		d, _, err := owned(tx, s, delivery, lease)
		if err != nil {
			return err
		}
		already, err := ack(tx, d, lease)
		if err != nil {
			return err
		}
		out = Answered{Delivery: d.ID, State: AnsweredAcked}
		if already {
			out.State = AnsweredAlreadyAcked
		}
		return nil
	})
	return out, err
}

// ack writes d done under lease; already when an ack of the same lease had.
func ack(tx Tx, d Delivery, lease string) (already bool, err error) {
	switch {
	case lease == "" || d.Lease != lease:
		return false, ErrLeaseLost
	case d.State == Acked:
		return true, nil
	case d.State != Leased:
		return false, ErrLeaseLost
	}
	now := tx.Now().UnixNano()
	d.State, d.DoneAt, d.LeaseUntil = Acked, &now, nil
	return false, tx.PutDelivery(d)
}

// Reply is what a replier gives mail.reply.
type Reply struct {
	Subject string
	Body    string
	Key     string
}

// Reply acks a request's delivery under its lease and sends its response, in one write
// (docs/decisions/crew.md#durable-mail). A replier's response to a request is one: a retry with the
// same content answers it, even after the lease is gone, and other content is ErrKeyReused. It
// answers the response and the delivery the handle resolved to, replied.
func (m *Manager) Reply(ctx context.Context, credential, delivery, lease string, p Reply) (Message, Answered, error) {
	switch {
	case len(p.Body) > MaxBody:
		return Message{}, Answered{}, fmt.Errorf("%w: %d bytes, at most %d", ErrTooLarge, len(p.Body), MaxBody)
	case len(p.Subject) > MaxSubject:
		return Message{}, Answered{}, fmt.Errorf("%w: a subject of %d bytes, at most %d", ErrInvalid, len(p.Subject), MaxSubject)
	case p.Key == "":
		return Message{}, Answered{}, fmt.Errorf("%w: an empty key", ErrInvalid)
	}
	var out Message
	var ans Answered
	err := m.store.Write(ctx, func(tx Tx) error {
		s, err := m.crew.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		if err := m.expire(tx); err != nil {
			return err
		}
		d, viaMessage, err := owned(tx, s, delivery, lease)
		if err != nil {
			return err
		}
		if viaMessage && p.Key == ReplyKey(delivery) { // the CLI's key for the item, named by its message
			p.Key = ReplyKey(d.ID)
		}
		req, ok, err := tx.Message(d.Message)
		if err != nil {
			return err
		}
		if !ok || req.Kind != KindRequest {
			return fmt.Errorf("%w: delivery %s is not a request's", ErrInvalid, delivery)
		}
		ans = Answered{Delivery: d.ID, State: AnsweredReplied}
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
		if _, err := ack(tx, d, lease); err != nil {
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
	if err != nil {
		return Message{}, Answered{}, err
	}
	return out, ans, nil
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
	// Held is how many deliveries the session holds leased and hasn't answered (acked or replied
	// to): one it forgot comes to it again when its lease lapses.
	Held int
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
		scope, err := receiverScope(tx, s)
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
				from, err := fromAddress(tx, scope, msg.Sender)
				if err != nil {
					return err
				}
				out.Items = append(out.Items, Item{Delivery: d, Message: msg, From: from})
			}
		}
		// held: the session's mail (its profile's, unbound or bound to it, as owned reads a
		// delivery) leased under a lease that hasn't lapsed, of a message that hasn't expired (one
		// that has can't be answered, though no write has expired its delivery yet); a delivery
		// doesn't record the session that leased it, and a profile has one live session
		unhandled, err := tx.Unhandled(s.Profile)
		if err != nil {
			return err
		}
		now := tx.Now().UnixNano()
		for _, d := range unhandled {
			if (d.Session != "" && d.Session != s.ID) || d.State != Leased || d.LeaseUntil == nil || *d.LeaseUntil <= now {
				continue
			}
			msg, ok, err := tx.Message(d.Message)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("mail: delivery %s names no message %s", d.ID, d.Message)
			}
			if msg.Expires == nil || *msg.Expires > now {
				out.Held++
			}
		}
		return nil
	})
	return out, err
}
