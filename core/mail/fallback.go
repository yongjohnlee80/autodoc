package mail

import "context"

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

// FallbackJob is one withdrawn delivery handed to its recipient's host.
type FallbackJob struct {
	ID        string
	Delivery  string
	Message   string
	Recipient string // profile id
	Host      string // the recipient's scope: a Host's ID is the scope it holds
	Tries     int64  // leases taken before this one: a host's retry knows it is one
}

// FallbackOutcome is what a host's delivery of a job came to.
type FallbackOutcome string

// The outcomes a host reports.
const (
	FallbackDelivered    FallbackOutcome = "delivered"      // in the agent's input and sent
	FallbackTypedNotSent FallbackOutcome = "typed_not_sent" // in its input, waiting on the user's Enter
	FallbackUnknown      FallbackOutcome = "unknown"        // the host can't tell: the user gets the text
)
