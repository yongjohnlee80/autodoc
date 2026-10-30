// Package inbox is the human's side of the crew and its mail
// (docs/decisions/crew.md#the-human-inbox): one feed every window and Neovim watch (inbox.watch),
// the history (inbox.list), and the human's answers (inbox.reply, inbox.dismiss, inbox.ack) and
// focus requests (inbox.focus). Its verbs take no credential: they run under the same-UID trust the
// daemon's socket already has (docs/decisions/crew.md#the-human-inbox). The answers are mail's
// state transitions (mail.Manager's Answer, Dismiss and Read); this package serves them, and reads
// the feed.
package inbox

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// Change is one row of the human feed's journal (inbox_change): what changed, and the message or
// session it changed.
type Change struct {
	Seq  int64
	Kind string // request | report | answered | dismissed | read | expired | attention | cleared | reachability | focus | undeliverable
	Ref  string // a message id, or a session id
	At   int64
}

// ChangeFocus is the feed's kind for inbox.focus: the session's owner raises it.
const ChangeFocus = "focus"

// Tx is one transaction over the crew, mail and feed tables. Every engine's one adapter implements
// it beside crew.Tx and mail.Tx.
type Tx interface {
	mail.Tx

	// Changes is the journal's rows after seq, in seq order, at most limit.
	Changes(after int64, limit int) ([]Change, error)
	// ChangeBounds is the oldest and newest retained seq: 0, 0 when the journal never had a row.
	// Retention never prunes the newest row, so a journal that had one is never empty again.
	ChangeBounds() (oldest, newest int64, err error)
	// OpenToUser is user's deliveries whose message is an open request or an unread report (any
	// kind but a generated event), with seq past after, in seq order, at most limit (0: all): a
	// snapshot's page.
	OpenToUser(user string, after int64, limit int) ([]mail.Item, error)
	// Raised is the attention rows not yet cleared, by session, past after, at most limit (0: all).
	Raised(after string, limit int) ([]crew.Attention, error)
	// ToUser is user's deliveries with their messages, newest first: below seq before (0: from the
	// newest), at most limit.
	ToUser(user string, before int64, limit int) ([]mail.Item, error)
	// FailedNotices is the failed deliveries of generated notices (events), newest done_at first,
	// at most limit: the notices nobody could receive (docs/decisions/crew.md#durable-mail).
	FailedNotices(limit int) ([]mail.Item, error)
}

// Store runs transactions over the tables, each one atomic.
type Store interface {
	Write(ctx context.Context, fn func(Tx) error) error
	Read(ctx context.Context, fn func(Tx) error) error
}

// Mail is the human's transitions, mail's own (mail.Manager).
type Mail interface {
	Answer(ctx context.Context, message, body, key string) (mail.Message, error)
	Dismiss(ctx context.Context, message string) error
	Read(ctx context.Context, message string) error
}
