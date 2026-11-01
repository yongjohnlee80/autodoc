package mail

import "github.com/yongjohnlee80/golib/errs"

// The mail.* verbs' public codes, in the module's range (core/module codes.go),
// docs/decisions/crew.md#three-modules-and-one-protocol.
const (
	CodeNoRecipient int64 = -32110 // an address that resolves to no profile or live session
	CodeKeyReused   int64 = -32111 // an idempotency key already names another message
	CodeTooLarge    int64 = -32112 // a body past MaxBody
	CodeLeaseLost   int64 = -32113 // the caller no longer holds the delivery's lease
	CodeAmbiguous   int64 = -32114 // a bare name that two or more scopes' profiles have, none the sender's
)

var (
	// ErrNoRecipient is an address that resolves to nothing: no active profile, or no live session.
	ErrNoRecipient = errs.Sentinel(errs.ErrInvalidArgument, "mail: no such recipient")

	// ErrAmbiguous is a bare name the sender's scope has no profile of, that two or more other
	// scopes do: the message names each one as name@scope, the address that reaches it.
	ErrAmbiguous = errs.Sentinel(errs.ErrInvalidArgument, "mail: the name is ambiguous")

	// ErrUnreachable is a send to fire (Send.Fire) whose recipient's doorbell didn't ring: nothing
	// was stored, or its delivery was withdrawn (or handed over: ErrHandedOver).
	ErrUnreachable = errs.Sentinel(errs.ErrPrecondition, "mail: the recipient's doorbell didn't ring")

	// ErrHandedOver is a send to fire whose doorbell didn't ring, handed to its recipient's host as a
	// fallback job (docs/decisions/crew.md#the-fallback-exactly-once): the host delivers it, so the
	// caller must not send it any other way. It is an ErrUnreachable too.
	ErrHandedOver = errs.Sentinel(ErrUnreachable, "its host carries it as a fallback job")

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
