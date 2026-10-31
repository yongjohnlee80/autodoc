package mail

import "github.com/yongjohnlee80/golib/errs"

// The mail.* verbs' public codes, in the module's range (core/module codes.go),
// docs/decisions/crew.md#three-modules-and-one-protocol.
const (
	CodeNoRecipient int64 = -32110 // an address that resolves to no profile or live session
	CodeKeyReused   int64 = -32111 // an idempotency key already names another message
	CodeTooLarge    int64 = -32112 // a body past MaxBody
	CodeLeaseLost   int64 = -32113 // the caller no longer holds the delivery's lease
)

var (
	// ErrNoRecipient is an address that resolves to nothing: no active profile, or no live session.
	ErrNoRecipient = errs.Sentinel(errs.ErrInvalidArgument, "mail: no such recipient")

	// ErrUnreachable is a send to fire (Send.Fire) whose recipient's doorbell didn't ring: nothing
	// was stored, or its delivery was withdrawn.
	ErrUnreachable = errs.Sentinel(errs.ErrPrecondition, "mail: the recipient's doorbell didn't ring")

	// ErrKeyReused is a send or reply whose key a different message already holds.
	ErrKeyReused = errs.Sentinel(errs.ErrPrecondition, "mail: the key names another message")

	// ErrTooLarge is a body past MaxBody.
	ErrTooLarge = errs.Sentinel(errs.ErrInvalidArgument, "mail: the body is too large")

	// ErrLeaseLost is an ack, renew or reply under a lease the caller no longer holds.
	ErrLeaseLost = errs.Sentinel(errs.ErrPrecondition, "mail: the lease is lost")

	// ErrInvalid is a param the mail verbs refuse; the message names it. It answers as invalid
	// params.
	ErrInvalid = errs.Sentinel(errs.ErrInvalidArgument, "mail: invalid")

	// errRecipientGone is a disposition, never a verb's failure
	// (docs/decisions/crew.md#durable-mail): the delivery is inserted failed, why=recipient_gone,
	// and the operation that made it still commits.
	errRecipientGone = errs.Sentinel(errs.ErrPrecondition, "mail: the recipient is gone")
)
