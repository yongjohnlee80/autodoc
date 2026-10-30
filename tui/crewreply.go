package tui

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/yongjohnlee80/autodoc/core/inbox"
)

// THE REPLY DIALOG (ADR 1793271500 §4.8): a request's toast opens it, in any window, wherever the
// asking agent runs: the request's subject and body, a text area for the answer, and Send, Dismiss
// or Cancel. Send is inbox.reply, keyed by the request and the body, so a reply sent again after a
// lost answer is the same reply; Dismiss is inbox.dismiss, the sender told. A reply that could not
// be sent is kept as the request's draft: the dialog opens on it again. An attention's toast raises
// its agent here, or asks the window that shows it to (inbox.focus).

// crewReplyDialog is the dialog's id in the layout (Reply.qml).
const crewReplyDialog = "crewReply"

// crewRequest is an open request, as the dialog shows it.
type crewRequest struct{ id, from, subject, body string }

func (*crewInbox) replyState() map[string]any {
	return map[string]any{"App.replyTitle": "", "App.replyText": "", "App.replyDraft": ""}
}

func (x *crewInbox) Commands() map[string]Command {
	return map[string]Command{
		"App.replySend":       oneString("the reply", x.replySend),
		"App.replyDismiss":    none(x.replyDismiss),
		"App.replyClosed":     none(func() { x.replying = "" }),
		"App.toggleAgentBell": none(x.toggleBell),
	}
}

// replyKey is a reply's idempotency key: the request and the body's digest, so the same reply sent
// again after a lost answer is answered as the first, and another body is another reply.
func replyKey(message, body string) string {
	h := sha256.New()
	for _, s := range []string{message, body} {
		_ = binary.Write(h, binary.BigEndian, uint64(len(s)))
		h.Write([]byte(s))
	}
	return "tui:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// openReply opens the dialog on request id, while it is open: on its draft, when a send failed.
func (x *crewInbox) openReply(id string) {
	r, ok := x.requests[id]
	if !ok {
		x.c.Notify("That request is closed.")
		return
	}
	x.replying = id
	x.c.Set("App.replyTitle", fmt.Sprintf("%s asks: %s", r.from, r.subject))
	x.c.Set("App.replyText", r.body)
	x.c.Set("App.replyDraft", x.drafts[id])
	x.c.Open(crewReplyDialog)
}

// replySend sends body as the reply to the request the dialog is open on.
func (x *crewInbox) replySend(body string) {
	id := x.replying
	x.replying = ""
	r, ok := x.requests[id]
	if id == "" {
		return
	}
	if !ok {
		r = crewRequest{id: id, from: "the agent"}
	}
	key := replyKey(id, body)
	x.c.Go(func(ctx context.Context) any {
		_, err := x.c.Call(ctx, "inbox.reply", id, body, key)
		return err
	}, func(v any) {
		err, _ := v.(error)
		switch {
		case v == nil:
			delete(x.drafts, id)
			x.c.Notify(fmt.Sprintf("Replied to %s.", r.from))
		case x.answeredElsewhere(err):
			delete(x.drafts, id)
			x.c.Notify(fmt.Sprintf("%s's request was already answered, dismissed or expired: your reply was not sent.", r.from))
		default:
			x.drafts[id] = body // the dialog opens on it again
			x.c.Notify(fmt.Sprintf("Your reply to %s was not sent: %s. Open the request again to send it.", r.from, wireMessage(err)))
		}
	})
}

// replyDismiss declines the request the dialog is open on: its sender is told.
func (x *crewInbox) replyDismiss() {
	id := x.replying
	x.replying = ""
	if id == "" {
		return
	}
	r := x.requests[id]
	x.c.Go(func(ctx context.Context) any {
		_, err := x.c.Call(ctx, "inbox.dismiss", id)
		return err
	}, func(v any) {
		err, _ := v.(error)
		switch {
		case v == nil:
			delete(x.drafts, id)
		case x.answeredElsewhere(err):
			delete(x.drafts, id)
			x.c.Notify(fmt.Sprintf("%s's request was already answered, dismissed or expired.", r.from))
		default:
			x.c.Notify(fmt.Sprintf("%s's request was not dismissed: %s.", r.from, wireMessage(err)))
		}
	})
}

// answeredElsewhere reports whether err says the request is closed: answered or dismissed first
// (another window, another reply), expired, or none to answer. Sending again would change nothing.
func (*crewInbox) answeredElsewhere(err error) bool {
	switch code(err) {
	case inbox.CodeAlreadyAnswered, inbox.CodeExpired, inbox.CodeNotARequest:
		return true
	}
	return false
}

// raiseAttention shows the agent waiting on the human: here when it is this window's, else the
// window that shows it is asked to (inbox.focus).
func (x *crewInbox) raiseAttention(session, codename string) {
	if x.raise(session) {
		return
	}
	x.c.Go(func(ctx context.Context) any {
		_, err := x.c.Call(ctx, "inbox.focus", session)
		return err
	}, func(v any) {
		if err, _ := v.(error); v != nil {
			x.c.Notify(fmt.Sprintf("%s could not be shown: %s.", codename, wireMessage(err)))
			return
		}
		x.c.Notify(fmt.Sprintf("%s runs in another window: that window shows it.", codename))
	})
}
