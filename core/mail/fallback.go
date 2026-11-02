package mail

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// FallbackChannel is a host's legacy channel (docs/decisions/crew.md#three-modules-and-one-protocol):
// a delivery the crew could not get received by its deadline is withdrawn and handed, once, to its
// recipient's host as a job, which the host delivers its own way (a window types it; Neovim files
// it in the recipient's mailbox). Hosts reach it over the daemon's fallback verbs; an in-process
// host may implement it.
type FallbackChannel interface {
	// Deliver delivers j, and says what came of it: Delivered, TypedNotSent, or Unknown when the
	// host can't tell whether an earlier attempt of the same job reached the agent.
	Deliver(ctx context.Context, j FallbackJob) (FallbackOutcome, error)
}

// FallbackJob is one withdrawn delivery handed to its recipient's host. Its states: open (offered)
// → leased (a host took it) → done | unknown; nothing else goes back to open, so a job is
// delivered at most once, by its host's say.
type FallbackJob struct {
	ID         string
	Delivery   string // one job a delivery, ever
	Message    string
	Recipient  string // profile id
	Host       string // the recipient's scope: a Host's ID is the scope it holds
	State      string // JobOpen | JobLeased | JobDone | JobUnknown
	Lease      string
	LeaseUntil *int64
	Tries      int64 // leases taken; an Offer's, those before it: a host's retry knows it is one
	Created    int64
	DoneAt     *int64
	Outcome    FallbackOutcome // as the host reported it, once done or unknown
}

// A fallback job's states.
const (
	JobOpen    = "open"
	JobLeased  = "leased"
	JobDone    = "done"
	JobUnknown = "unknown"
)

// When a delivery falls back (docs/decisions/crew.md#the-fallback-exactly-once): it was never
// leased, its recipient has a live session, and, counted from the later of the message's creation
// and that session's start, either fallbackUnreachable has passed with the session unreachable, or
// FallbackAfter has. From the session's start too, so a message that waited across its recipient's
// restart gets the whole window in the new session.
const fallbackUnreachable = 30 * time.Second

// FallbackAfter is how long a delivery to a reachable live session waits to be received before it
// falls back. Its own window, longer than a ring's (crew.WakeUnanswered): an agent busy in a long
// step is rung again after each unanswered wait, backing off (at 0:00, 2:05, 4:35 and 8:35), before
// its host is handed the message.
const FallbackAfter = 10 * time.Minute

// fallbackLease is how long a host holds a job it took before the job is offered to it again.
const fallbackLease = 60 * time.Second

// FallbackOutcome is what a host's delivery of a job came to.
type FallbackOutcome string

// The outcomes a host reports.
const (
	FallbackDelivered    FallbackOutcome = "delivered"      // in the agent's input and sent
	FallbackTypedNotSent FallbackOutcome = "typed_not_sent" // in its input, waiting on the user's Enter
	FallbackUnknown      FallbackOutcome = "unknown"        // the host can't tell: the user gets the text
)

// fallbackBatch bounds one pass of expire's fallback read, as expireBatch does its others.
const fallbackBatch = expireBatch

// fallBackDue opens a job for every delivery FallbackDue answers: expire's third pass.
func (m *Manager) fallBackDue(tx Tx) error {
	now := tx.Now()
	unreachable := now.Add(-fallbackUnreachable).UnixNano()
	unanswered := now.Add(-FallbackAfter).UnixNano()
	for {
		due, err := tx.FallbackDue(unreachable, unanswered, fallbackBatch)
		if err != nil {
			return err
		}
		for _, d := range due {
			if _, err := m.fallBack(tx, d); err != nil {
				return err
			}
		}
		if len(due) < fallbackBatch {
			return nil
		}
	}
}

// fallBack withdraws d and opens its job, in the same write: the withdraw only takes a delivery
// still queued and never leased, so a receive that won the race leaves nothing to fall back, and a
// delivery the agent ever leased (it may have read it) never falls back: exactly once. received
// is true, and nothing changes, when the recipient leased or acked it first.
func (m *Manager) fallBack(tx Tx, d Delivery) (received bool, err error) {
	cur, ok, err := tx.DeliveryOf(d.Message, d.Recipient)
	if err != nil || !ok {
		return false, err
	}
	if cur.State != Queued || cur.Attempts != 0 {
		return cur.State == Leased || cur.State == Acked, nil
	}
	p, ok, err := tx.Profile(cur.Recipient)
	if err != nil || !ok {
		return false, err
	}
	now := tx.Now().UnixNano()
	cur.State, cur.DoneAt, cur.Why = Withdrawn, &now, WhyFallback
	if err := tx.PutDelivery(cur); err != nil {
		return false, err
	}
	j := FallbackJob{ID: m.newID(), Delivery: cur.ID, Message: cur.Message, Recipient: cur.Recipient,
		Host: p.Scope, State: JobOpen, Created: now}
	if err := tx.PutFallbackJob(j); err != nil {
		return false, err
	}
	if err := m.settleIfDrained(tx, cur.Recipient); err != nil {
		return false, err
	}
	return false, tx.AddInboxChange(ChangeFallback, j.ID)
}

// settleIfDrained settles recipient's wake (crew's Settled: what was pending, not its failures)
// when it has nothing queued left, so no later wake rings for mail that is gone. A leased delivery
// doesn't keep the wake due: its lease lapsing raises pending on its own.
func (m *Manager) settleIfDrained(tx Tx, recipient string) error {
	left, err := tx.Unhandled(recipient)
	if err != nil {
		return err
	}
	for _, d := range left {
		if d.State == Queued {
			return nil
		}
	}
	s, live, err := tx.LiveSession(recipient)
	if err != nil || !live {
		return err
	}
	return m.crew.Settled(tx, s.ID)
}

// takesFallbacks is whether recipient's scope's host takes fallback jobs in its current epoch.
func takesFallbacks(tx Tx, recipient string) (bool, error) {
	p, ok, err := tx.Profile(recipient)
	if err != nil || !ok {
		return false, err
	}
	return tx.TakesFallbacks(p.Scope)
}

// nextDeadline is when mail next changes by time alone: the store's deadlines (leases, expiry)
// and the next delivery to fall back.
func (m *Manager) nextDeadline(tx Tx) (int64, bool, error) {
	at, has, err := tx.NextDeadline()
	if err != nil {
		return 0, false, err
	}
	fb, ok, err := tx.NextFallbackAt(int64(fallbackUnreachable), int64(FallbackAfter))
	if err != nil || !ok {
		return at, has, err
	}
	if !has || fb < at {
		return fb, true, nil
	}
	return at, has, nil
}

// fallbackOffersMax bounds how many jobs one mail.fallbacks answer leases.
const fallbackOffersMax = 8

// Offer is one fallback job its host took, with what the host needs to deliver it.
type Offer struct {
	Job      FallbackJob // leased to the host: Lease answers mail.fallback_done; Tries counts the leases before this one
	Message  Message
	From     string // the sender, as the recipient would address an answer (fromAddress)
	Codename string // the recipient's
	Kind     string // the recipient's CLI kind
}

// Fallbacks is mail.fallbacks (docs/decisions/crew.md#the-fallback-exactly-once): it records that
// the host holding scope at epoch takes fallback jobs, and leases it the jobs of its scope's
// profiles, open or whose lease lapsed, for fallbackLease, answering as soon as there is one, or
// at wait (at most crew.MaxWait). A lapsed job comes back with Tries one higher: the host decides
// from its own record whether it delivered it, and is never told to type twice.
func (m *Manager) Fallbacks(ctx context.Context, scope, run string, epoch int64, wait time.Duration) ([]Offer, error) {
	wait = min(max(wait, 0), crew.MaxWait)
	heard, stop := m.crew.Watch()
	defer stop()
	// NOTE: timed by the caller's long-poll: mail.fallbacks answers empty once wait passes, so the
	// host comes back; a job made, a lapsing lease or ctx ends the wait sooner.
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		var out []Offer
		next := time.Duration(-1)
		err := m.store.Write(ctx, func(tx Tx) error {
			out, next = nil, -1
			if err := m.crew.GuardHost(tx, scope, run, epoch); err != nil {
				return err
			}
			if err := tx.TakeFallbacks(scope, epoch); err != nil {
				return err
			}
			if err := m.expire(tx); err != nil {
				return err
			}
			var err error
			if out, err = m.lease(tx, scope); err != nil || len(out) > 0 {
				return err
			}
			now := tx.Now().UnixNano()
			at, has, err := m.nextDeadline(tx)
			if err != nil {
				return err
			}
			if lapse, ok, err := tx.NextFallbackLease(scope); err != nil {
				return err
			} else if ok && (!has || lapse < at) {
				at, has = lapse, true
			}
			if has {
				next = max(time.Duration(at-now), 0)
			}
			return nil
		})
		if err != nil || len(out) > 0 {
			return out, err
		}
		if timedOut, err := crew.WaitFor(ctx, heard, deadline.C, next); err != nil || timedOut {
			return nil, err
		}
	}
}

// lease leases scope's offerable jobs to its host, with what each needs.
func (m *Manager) lease(tx Tx, scope string) ([]Offer, error) {
	now := tx.Now()
	jobs, err := tx.FallbackOffers(scope, now.UnixNano(), fallbackOffersMax)
	if err != nil {
		return nil, err
	}
	var out []Offer
	for _, j := range jobs {
		until := now.Add(fallbackLease).UnixNano()
		j.State, j.Lease, j.LeaseUntil = JobLeased, m.newID(), &until
		taken := j
		taken.Tries++ // the row counts the leases taken; the offer, those before this one
		if err := tx.PutFallbackJob(taken); err != nil {
			return nil, err
		}
		msg, _, err := tx.Message(j.Message)
		if err != nil {
			return nil, err
		}
		p, _, err := tx.Profile(j.Recipient)
		if err != nil {
			return nil, err
		}
		from, err := fromAddress(tx, p.Scope, msg.Sender)
		if err != nil {
			return nil, err
		}
		out = append(out, Offer{Job: j, Message: msg, From: from, Codename: p.Codename, Kind: p.Kind})
	}
	return out, nil
}

// FallbackDone is mail.fallback_done: the host that leased job reports its outcome, once. A replay
// of the same lease's report answers ok; a report under a lease the host no longer holds (the job
// was offered again) is ErrLeaseLost. An unknown outcome tells the human, with the message's text
// to send by hand.
func (m *Manager) FallbackDone(ctx context.Context, scope, run string, epoch int64, job, lease string, outcome FallbackOutcome) error {
	switch outcome {
	case FallbackDelivered, FallbackTypedNotSent, FallbackUnknown:
	default:
		return fmt.Errorf("%w: outcome %q", ErrInvalid, outcome)
	}
	return m.store.Write(ctx, func(tx Tx) error {
		if err := m.crew.GuardHost(tx, scope, run, epoch); err != nil {
			return err
		}
		j, ok, err := tx.FallbackJob(job)
		if err != nil {
			return err
		}
		if !ok || j.Host != scope {
			return fmt.Errorf("%w: no job %q of this host", ErrInvalid, job)
		}
		if j.Lease != lease {
			return ErrLeaseLost
		}
		if j.State != JobLeased { // done or unknown under this lease: the replay of a report
			return nil
		}
		now := tx.Now().UnixNano()
		j.State, j.DoneAt, j.Outcome, j.LeaseUntil = JobDone, &now, outcome, nil
		if outcome == FallbackUnknown {
			j.State = JobUnknown
		}
		if err := tx.PutFallbackJob(j); err != nil {
			return err
		}
		if j.State == JobUnknown {
			return tx.AddInboxChange(ChangeFallbackUnknown, j.ID)
		}
		return tx.AddInboxChange(ChangeFallbackDone, j.ID)
	})
}

// FallbackText is what a host types into (or files for) a job's recipient: who sent it, and how to
// answer, since a withdrawn delivery takes no mail.reply. Its frame stays short
// (docs/decisions/crew.md#the-fallback-exactly-once): the message is the rest.
func FallbackText(from string, msg Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Mail from %s (by your host: the crew couldn't deliver it). Answer with autodoc mail send %s.\n", from, from)
	if msg.Subject != "" {
		b.WriteString(msg.Subject + "\n")
	}
	b.WriteString(msg.Body)
	return b.String()
}

// ErrNoJob is a fallback job the daemon has no row of.
var ErrNoJob = errs.Sentinel(errs.ErrInvalidArgument, "mail: no such fallback job")

// FallbackView is one fallback job as the human's feed shows it: whose mail it is, from whom, the
// text its host delivers, and whether that host holds its scope now.
type FallbackView struct {
	Job                         FallbackJob
	Codename, Scope, From, Text string
	Subject                     string
	HostPresent                 bool
}

// Fallback is the job id, as the human's feed reads it (inbox.fallback): a window shows a job whose
// host is away as waiting for it, and the text of one whose outcome is unknown.
func (m *Manager) Fallback(ctx context.Context, id string) (FallbackView, error) {
	var v FallbackView
	err := m.store.Read(ctx, func(tx Tx) error {
		j, ok, err := tx.FallbackJob(id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %q", ErrNoJob, id)
		}
		msg, _, err := tx.Message(j.Message)
		if err != nil {
			return err
		}
		p, _, err := tx.Profile(j.Recipient)
		if err != nil {
			return err
		}
		from, err := fromAddress(tx, p.Scope, msg.Sender)
		if err != nil {
			return err
		}
		h, held, err := tx.Host(j.Host)
		if err != nil {
			return err
		}
		v = FallbackView{Job: j, Codename: p.Codename, Scope: p.Scope, From: from, Subject: msg.Subject,
			Text: FallbackText(from, msg), HostPresent: held && crew.HostHolds(h, tx.Now())}
		return nil
	})
	return v, err
}
