package mail

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// The rows the mail module reads and writes (ADR 1793271500 §4.2). Times are Unix nanoseconds; a
// nil *int64 is SQL NULL. Each engine's adapter maps them onto its tables.
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

	MessageOpen     = "open"
	MessageAnswered = "answered"
	MessageExpired  = "expired"

	Queued  = "queued"
	Leased  = "leased"
	Acked   = "acked"
	Failed  = "failed"
	Expired = "expired"
	Merged  = "merged"

	WhyAttempts      = "attempts"
	WhyExpired       = "expired"
	WhySessionEnded  = "session_ended"
	WhyDiscarded     = "discarded"
	WhyRecipientGone = "recipient_gone"
	WhyReopened      = "reopened_by_adoption"
)

// The reserved profiles: the daemon, which sends every generated notice and is never a
// recipient, and the human, codename "user" in the global scope.
const (
	DaemonProfile = "crew:autodoc"
	GlobalScope   = "global"
	UserCodename  = "user"
)

// The human feed's change kinds this module appends (§4.8).
const (
	ChangeRequest       = "request"
	ChangeReport        = "report"
	ChangeExpired       = "expired"
	ChangeUndeliverable = "undeliverable"
)

// Tx is one transaction over the crew and mail tables together: a crew verb's mail effect and a
// mail verb's crew guard commit in the same write (§4.4). Every engine's one adapter implements it.
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
	// Eligible is recipient's queued deliveries for session (unbound, or bound to it), in seq order.
	Eligible(recipient, session string, limit int) ([]Delivery, error)
	// Unhandled is every queued or leased delivery of recipient, in seq order.
	Unhandled(recipient string) ([]Delivery, error)
	// BoundTo is every queued or leased delivery bound to session.
	BoundTo(session string) ([]Delivery, error)
	// LeasesDue is the leased deliveries whose lease_until is at or before now.
	LeasesDue(now int64, limit int) ([]Delivery, error)
	// Expiring is the queued or leased deliveries whose message expires at or before now.
	Expiring(now int64, limit int) ([]Delivery, error)

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
	// guarded (§4.4, §4.5), in tx: a takeover committed meanwhile fails the verb.
	Authenticate(tx crew.Tx, credential string) (crew.Session, error)
	// RaisePending raises pending_gen for profile's live session, so the scheduler wakes it (§4.7).
	RaisePending(tx crew.Tx, profile string) error
}
