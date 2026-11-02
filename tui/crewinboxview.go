package tui

import (
	"context"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE INBOX (docs/decisions/crew.md#the-human-inbox): SPC a i, or :inbox, opens the user's mail in a
// dialog of its own, newest first, fifty at a time (inbox.list, paged by seq: More reads the next
// page). Its tabs are ways of reading it: All, Unread (reports not read yet), Open requests, and
// Undeliverable (inbox.list's filter: the notices nobody could receive). The message under the
// cursor shows below the list, as Markdown; Open (Enter) opens it, and opening a report marks it
// read (inbox.ack). A request is never marked read or answered by opening it: it stays open until
// Reply (the reply dialog, crewreply.go) or Dismiss (asked first) answers it. Mark read marks any
// report read; Sender shows the agent that sent it, here or in the window that shows it.
//
// The dialog holds no long-poll of its own: the window's crew inbox (crewinbox.go) keeps its one
// inbox.watch, tells the dialog how many reports and requests are open (the title's count), and
// tells it when the open set may have changed. Then the dialog reads its first page again and merges
// it by seq: the pages read before stay, so a reader scrolled down is never thrown back to the top.

// inboxPage is how many messages one inbox.list reads.
const inboxPage = 50

// The inbox's tabs.
const (
	inboxAll = iota
	inboxUnread
	inboxRequests
	inboxUndeliverable
	inboxTabs
)

// inboxCore is what the inbox needs of the program.
type inboxCore interface {
	Call(ctx context.Context, verb string, params ...any) (any, error)
	Go(fn func(context.Context) any, then func(any))
	Set(name string, v any)
	Open(id string)
	Ask(q Question) Asked
	Notify(msg string)
	Tr(id string) string
	Trf(id string, args ...any) string
}

// inboxReplies is what the inbox asks of the window's crew inbox: its reply dialog on a request, and
// the agent that sent a message shown.
type inboxReplies interface {
	openReply(id string)
	raiseAttention(session, codename string)
}

// inboxItem is one message, as inbox.list answers it.
type inboxItem struct {
	message, kind, from, scope, session, subject, body, replyTo, state, created, status, why string
	seq                                                                                      int64
}

// inboxView is the dialog. Its fields are the loop's.
type inboxView struct {
	c       inboxCore
	replies inboxReplies
	rows    *rows[inboxItem]
	now     func() time.Time
}

func newInboxView(c inboxCore, replies inboxReplies) *inboxView {
	v := &inboxView{c: c, replies: replies, now: time.Now}
	v.rows = newRows(tuidecl.NewListModel("key", "mark", "kind", "from", "subject", "state", "when"), v.rowOf)
	return v
}

func (*inboxView) Name() string { return "inbox" }

func (v *inboxView) State() map[string]any {
	return map[string]any{"App.inbox": v.rows.Model(), "App.inboxTitle": "", "App.inboxDetail": "", "App.inboxStatus": ""}
}

func (v *inboxView) Commands() map[string]Command { return map[string]Command{} }

// rowOf is a message as the list shows it.
func (v *inboxView) rowOf(_ int, it inboxItem) rowOf { return rowOf{"key": it.message} }

// open lists the first page of the tab shown and opens the dialog.
func (v *inboxView) open() {}

// openSet is the crew inbox's count of what is open: its reports and requests.
func (v *inboxView) openSet(reports, requests int) {}

// changed is the crew inbox's word that the open set may have changed.
func (v *inboxView) changed() {}

// closed is the dialog dismissed: a change read no more.
func (v *inboxView) closed() {}

func (v *inboxView) tab(t int)      {}
func (v *inboxView) more()          {}
func (v *inboxView) shown(i int)    {}
func (v *inboxView) openItem(i int) {}
func (v *inboxView) markRead(i int) {}
func (v *inboxView) reply(i int)    {}
func (v *inboxView) dismiss(i int)  {}
func (v *inboxView) sender(i int)   {}
