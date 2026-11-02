package mail

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// The rows the mail module reads and writes (docs/decisions/crew.md#the-crew-and-mail-tables).
// Times are Unix nanoseconds; a nil *int64 is SQL NULL. Each engine's adapter maps them onto its
// tables.
type (
	// Message is what a sender sent, once, whatever its recipients.
	Message struct {
		ID            string
		Sender        string // profile id, from the authenticated session; never a param
		SenderSession string
		IdemKey       string
		Digest        []byte // the content the key is bound to
		Kind          string // message | request | response | event
		Subject       string
		Body          string
		ReplyTo       string
		State         string // a request or report to the human: open | answered | dismissed | read | expired
		Created       int64
		Expires       *int64
	}

	// Delivery is one message's obligation to one recipient profile.
	Delivery struct {
		ID         string
		Message    string
		Recipient  string // profile id
		Session    string // "" for any session of the profile; else bound to that one
		Seq        int64  // the recipient's, from its high-water: never reused
		State      string // queued | leased | acked | failed | expired | merged
		Lease      string
		LeaseUntil *int64
		Attempts   int64
		ReadAt     *int64
		DoneAt     *int64
		Why        string
	}
)

// Message kinds, message and delivery states, and the reasons a delivery ends unhandled.
const (
	KindMessage  = "message"
	KindRequest  = "request"
	KindResponse = "response"
	KindEvent    = "event"

	MessageOpen      = "open"
	MessageAnswered  = "answered"
	MessageDismissed = "dismissed"
	MessageRead      = "read"
	MessageExpired   = "expired"

	Queued = "queued"
	Leased = "leased"
	Acked  = "acked"
	Failed = "failed"
	// Withdrawn is a delivery handed to its recipient's host instead (WhyFallback, a fallback job),
	// or, before the fallback, a send to fire whose doorbell never rang (WhyWithdrawn): done, never
	// receivable.
	Withdrawn = "withdrawn"
	Expired   = "expired"
	Merged    = "merged"

	WhyAttempts      = "attempts"
	WhyExpired       = "expired"
	WhySessionEnded  = "session_ended"
	WhyDiscarded     = "discarded"
	WhyRecipientGone = "recipient_gone"
	WhyReopened      = "reopened_by_adoption"
	WhyDismissed     = "dismissed" // the human declined a request
	WhyWithdrawn     = "withdrawn" // before the fallback: a send to fire whose doorbell never rang, typed by the window
	WhyFallback      = "fallback"  // not received in time: handed to its recipient's host, as a fallback job
)

// The reserved profiles: the daemon, which sends every generated notice and is never a
// recipient, and the human, codename "user" in the global scope. EnsureReserved makes both, with
// these fixed ids.
const (
	DaemonProfile = "crew:autodoc"
	UserProfile   = "crew:user"
	GlobalScope   = "global"
	UserCodename  = "user"
)

// The human feed's change kinds this module appends (docs/decisions/crew.md#the-human-inbox).
const (
	ChangeRequest       = "request"
	ChangeReport        = "report"
	ChangeAnswered      = "answered"
	ChangeDismissed     = "dismissed"
	ChangeRead          = "read"
	ChangeExpired       = "expired"
	ChangeUndeliverable = "undeliverable"
	// ChangeFallback is a fallback job made (its ref is the job): the human sees the mail went by
	// its recipient's host, and that it waits while the host is away.
	ChangeFallback = "fallback"
	// ChangeFallbackUnknown is a job its host could not say it delivered (its ref is the job): the
	// human gets the message's text, to send by hand.
	ChangeFallbackUnknown = "fallback_unknown"
	// ChangeFallbackDone is a job its host delivered (its ref is the job): a window showing it as
	// waiting for its host closes that.
	ChangeFallbackDone = "fallback_done"
)

// Tx is one transaction over the crew and mail tables together: a crew verb's mail effect and a
// mail verb's crew guard commit in the same write (docs/decisions/crew.md#sessions-and-the-guard).
// Every engine's one adapter implements it.
type Tx interface {
	crew.Tx

	Message(id string) (Message, bool, error)
	MessageByKey(sender, key string) (Message, bool, error)
	// Response is the response sender gave to the request replyTo, if any (mail_one_reply).
	Response(replyTo, sender string) (Message, bool, error)
	PutMessage(Message) error

	// NextSeq is recipient's next delivery seq; it raises the high-water past it.
	NextSeq(recipient string) (int64, error)

	Delivery(id string) (Delivery, bool, error)
	DeliveryOf(message, recipient string) (Delivery, bool, error)
	PutDelivery(Delivery) error
	// DeliveriesOf is every delivery of message.
	DeliveriesOf(message string) ([]Delivery, error)
	// Eligible is recipient's queued deliveries for session (unbound, or bound to it) whose message
	// has not expired by now, in seq order: the store applies both before the limit, so no prefix
	// of expired mail can hide a live delivery behind it (eligible(now),
	// docs/decisions/crew.md#the-crew-and-mail-tables).
	Eligible(recipient, session string, now int64, limit int) ([]Delivery, error)
	// Unhandled is every queued or leased delivery of recipient, in seq order.
	Unhandled(recipient string) ([]Delivery, error)
	// BoundTo is every queued or leased delivery bound to session.
	BoundTo(session string) ([]Delivery, error)
	// LeasesDue is the leased deliveries whose lease_until is at or before now.
	LeasesDue(now int64, limit int) ([]Delivery, error)
	// Expiring is the queued or leased deliveries whose message expires at or before now.
	Expiring(now int64, limit int) ([]Delivery, error)
	// NextDeadline is the earliest instant a delivery changes by time alone: a lease's lease_until,
	// or the expiry of a message with an unhandled delivery; false when there is none.
	NextDeadline() (int64, bool, error)

	// FallbackDue is the queued deliveries never leased (Attempts 0) with no fallback job, whose
	// recipient has a live session and a scope whose host takes fallbacks (TakeFallbacks in its
	// current epoch), and whose message was created, and that session started, at or before
	// unansweredBefore, or both at or before unreachableBefore with that session unreachable (no
	// endpoint, or crew.UnreachableAfter failed wakes): in seq order, at most limit
	// (docs/decisions/crew.md#the-fallback-exactly-once).
	FallbackDue(unreachableBefore, unansweredBefore int64, limit int) ([]Delivery, error)
	// NextFallbackAt is the earliest instant a queued delivery with no job falls back by time alone,
	// from the later of its message's creation and its recipient's live session's start (since):
	// since+unreachable when that session is unreachable, else since+unanswered. false when there is
	// none. Never earlier than FallbackDue would answer: the scheduler's timer waits for it.
	NextFallbackAt(unreachable, unanswered int64) (int64, bool, error)
	// TakeFallbacks records that host, at epoch, takes fallback jobs (crew_host.fallbacks): its
	// scope's deliveries may fall back from now on, until its epoch changes.
	TakeFallbacks(host string, epoch int64) error
	// TakesFallbacks is whether host took fallbacks (TakeFallbacks) in its current epoch.
	TakesFallbacks(host string) (bool, error)
	FallbackJob(id string) (FallbackJob, bool, error)
	PutFallbackJob(FallbackJob) error
	// FallbackOffers is host's jobs a host may take at now: open, or leased with lease_until at or
	// before now, oldest first, at most limit.
	FallbackOffers(host string, now int64, limit int) ([]FallbackJob, error)
	// NextFallbackLease is the earliest lease_until of host's leased jobs; false when none.
	NextFallbackLease(host string) (int64, bool, error)

	// AddInboxChange (the human feed's journal) comes with crew.Tx.
}

// Store runs transactions over the crew and mail tables, each one atomic.
type Store interface {
	Write(ctx context.Context, fn func(Tx) error) error
	Read(ctx context.Context, fn func(Tx) error) error
}

// Crew is what the mail module needs of the crew module, inside mail's own transaction.
type Crew interface {
	// Authenticate is the live session a credential names, its secret checked and the session
	// guarded (docs/decisions/crew.md#sessions-and-the-guard,
	// docs/decisions/crew.md#the-session-context), in tx: a takeover committed meanwhile fails the
	// verb.
	Authenticate(tx crew.Tx, credential string) (crew.Session, error)
	// GuardHost passes when run's process still holds scope at epoch, and the scope is live
	// (docs/decisions/crew.md#sessions-and-the-guard), in tx: the user's sends from a window
	// (SendAsUser) are its host's.
	GuardHost(tx crew.Tx, scope, run string, epoch int64) error
	// Reachable reports whether profile's live session can be woken now (crew.list's reachable), in
	// tx.
	Reachable(tx crew.Tx, profile string) (bool, error)
	// Fire rings profile's live session's doorbell now and answers whether it fired for the mail
	// pending now (docs/decisions/crew.md#the-human-inbox), within ctx: a send that must be fired or
	// nothing.
	Fire(ctx context.Context, profile string) (bool, error)
	// RaisePending raises pending_gen for profile's live session, so the scheduler wakes it
	// (docs/decisions/crew.md#wakers-and-the-wake-scheduler).
	RaisePending(tx crew.Tx, profile string) error
	// Received is a receive's half of the wake, in the receive's write: the session has seen what is
	// pending (docs/decisions/crew.md#wakers-and-the-wake-scheduler).
	Received(tx crew.Tx, session string) error
	// Settled is a withdrawn send's half of the wake, in its write: nothing pending is due any more,
	// while the session's failures and backoff stay (docs/decisions/crew.md#the-human-inbox).
	Settled(tx crew.Tx, session string) error
	// Watch is a channel that hears every change mail.wait may be waiting for (crew's wake signal);
	// stop unregisters it. Registered before mail.wait's check.
	Watch() (ch <-chan struct{}, stop func())
}
