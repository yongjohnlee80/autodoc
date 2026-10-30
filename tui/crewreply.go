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

// crewReplyDialog is the dialog's id in the layout (main.qml's Reply { id: crewReply }); crewReplyText
// its text area's, as golib names an id inside a component's instance: the file's own id, "@", the
// use site's id (decl's component scope), so only this instance's text area is ever reached.
const (
	crewReplyDialog = "crewReply"
	crewReplyText   = "crewReplyText@" + crewReplyDialog
)

// crewRequest is an open request, as the dialog shows it.
type crewRequest struct{ id, from, subject, body string }

func (*crewInbox) replyState() map[string]any {
	return map[string]any{"App.replyTitle": "", "App.replyText": ""}
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
	// the text area is set at each opening, never through a binding: an empty draft after an empty
	// draft would change nothing, and the text typed for another request would stay in it
	x.c.SetText(crewReplyText, x.drafts[id])
	x.c.Open(crewReplyDialog)
}

// replySend sends body as the reply to the request the dialog is open on. A send that fails keeps
// body as the request's draft only while the request is open and this is its latest send: a later
// send's draft, or a request closed meanwhile, is never overwritten or brought back.
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
	x.attempt++
	attempt := x.attempt
	x.sending[id] = attempt
	key := replyKey(id, body)
	x.c.Go(func(ctx context.Context) any {
		_, err := x.c.Call(ctx, "inbox.reply", id, body, key)
		return err
	}, func(v any) {
		err, _ := v.(error)
		latest := x.sending[id] == attempt
		if latest {
			delete(x.sending, id)
		}
		_, open := x.requests[id]
		switch {
		case v == nil:
			delete(x.drafts, id)
			x.c.Notify(fmt.Sprintf("Replied to %s.", r.from))
		case x.answeredElsewhere(err):
			delete(x.drafts, id)
			x.c.Notify(fmt.Sprintf("%s's request was already answered, dismissed or expired: your reply was not sent.", r.from))
		case !open:
			x.c.Notify(fmt.Sprintf("Your reply to %s was not sent (%s), and the request has closed meanwhile.", r.from, wireMessage(err)))
		case !latest:
			x.c.Notify(fmt.Sprintf("An earlier reply to %s was not sent: %s.", r.from, wireMessage(err)))
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
