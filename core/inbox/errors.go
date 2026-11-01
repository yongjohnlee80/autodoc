package inbox

import (
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// The inbox.* verbs' public codes, in the module's range (core/module codes.go),
// docs/decisions/crew.md#three-modules-and-one-protocol.
const (
	CodeAlreadyAnswered int64 = -32130 // the request was answered or dismissed first
	CodeNotARequest     int64 = -32131 // no request to the user
	CodeExpired         int64 = -32132 // the message expired first
	CodeKeyReused       int64 = -32133 // the reply key is bound to another request or body
	CodeTooLarge        int64 = -32134 // a body past mail.MaxBody
	CodeUnreachable     int64 = -32135 // inbox.send's fire: the agent's doorbell didn't ring, and nothing was left
	CodeHandedOver      int64 = -32136 // inbox.send's fire: the agent's doorbell didn't ring; its host carries it as a fallback job
	// codeNotFound is the daemon's shared code (rpc's CodeNotFound).
	codeNotFound int64 = -32061
)

var (
	// ErrNotFound is a focus of a session that has ended, or never was.
	ErrNotFound = errs.Sentinel(errs.ErrPrecondition, "inbox: no such live session")

	// ErrInvalid is a param the inbox verbs refuse; the message names it. It answers as invalid
	// params.
	ErrInvalid = errs.Sentinel(errs.ErrInvalidArgument, "inbox: invalid")
)

// Name is the module's name: its verbs are inbox.*.
func (m *Manager) Name() string { return "inbox" }

// Errors are what the inbox verbs may answer besides invalid params: mail's sentinels for the
// human's transitions, on this module's own codes.
func (m *Manager) Errors() []module.PublicError {
	return []module.PublicError{
		{Err: mail.ErrAlreadyAnswered, Code: CodeAlreadyAnswered, Name: "inbox.already_answered",
			Message: "the request was already answered or dismissed", Next: "inbox.list shows who answered, and when"},
		{Err: mail.ErrNotARequest, Code: CodeNotARequest, Name: "inbox.not_a_request",
			Message: "that message is no request to the user", Next: "inbox.ack a report"},
		{Err: mail.ErrExpired, Code: CodeExpired, Name: "inbox.expired", Message: "the message expired first",
			Next: "its sender was told; nothing waits on it"},
		{Err: mail.ErrKeyReused, Code: CodeKeyReused, Name: "inbox.key_reused",
			Message: "the key is bound to another request or reply", Next: "retry the same reply with the same key, or use a new key"},
		{Err: mail.ErrTooLarge, Code: CodeTooLarge, Name: "inbox.too_large", Message: "the body is past 64 KiB"},
		// before inbox.unreachable: a handed-over send is an unreachable one too, and must answer as itself
		{Err: mail.ErrHandedOver, Code: CodeHandedOver, Name: "inbox.handed_over",
			Message: "the agent's doorbell didn't ring: its host carries the message as a fallback job",
			Next:    "send it no other way: the job mail.fallbacks brings the host delivers it"},
		{Err: mail.ErrUnreachable, Code: CodeUnreachable, Name: "inbox.unreachable",
			Message: "the agent's doorbell didn't ring: nothing was left for it", Next: "type the text into its prompt, or send without fire to leave it queued"},
		{Err: ErrNotFound, Code: codeNotFound, Name: "inbox.not_found", Message: "no such live session",
			Next: "the agent exited: nothing to raise"},
		{Err: crew.ErrLongPollsBusy, Code: crew.CodeLongPollsBusy, Name: "inbox.long_polls_busy",
			Message: "a connection holds at most two long-polls", Next: "finish one first, or open another connection"},
	}
}
