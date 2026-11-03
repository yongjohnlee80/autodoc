package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE INBOX (docs/decisions/crew.md#the-human-inbox): SPC a i, or :inbox, opens the user's mail in a
// dialog of its own, newest first, fifty at a time (inbox.list, paged by seq: More reads the next
// page). Its tabs are ways of reading it: All, Unread (reports not read yet), Open requests, and
// Undeliverable (inbox.list's filter: the notices nobody could receive; the daemon answers its
// newest only, so that tab lists them with no More). The message under the
// cursor shows below the list, as Markdown; Open (Enter) opens it, and opening a report marks it
// read (inbox.ack). A request is never marked read or answered by opening it: it stays open until
// Reply (the reply dialog, crewreply.go) or Dismiss (asked first) answers it. Mark read marks any
// report read; Sender shows the agent that sent it, here or in the window that shows it.
//
// The dialog holds no long-poll of its own: the window's crew inbox (crewinbox.go) keeps its one
// inbox.watch, tells the dialog how many reports and requests are open (the title's count), and
// tells it when its mail may have changed. Then the dialog reads its first page again, and that page
// is the list: the pages More read before go, so what is listed is always one consistent read, newest
// first (a page read before a change can hold rows the change moved past, and more than a page
// arriving leaves a gap no More reaches); deeper is More again. Every read takes its source's next
// generation, and so does each ack or dismiss that lands: an answer of an older one is dropped, so a
// read that started before never undoes a newer read or what the user just did. The cursor stays on
// the message it was on.

// inboxPage is how many messages one inbox.list reads.
const inboxPage = 50

// inboxDialog is the dialog's id in the layout (main.qml's Inbox { id: inbox }).
const inboxDialog = "inbox"

// The inbox's tabs.
const (
	inboxAll = iota
	inboxUnread
	inboxRequests
	inboxUndeliverable
	inboxTabs
)

// inboxTabNames are the tabs' names in the title, by tab: words, without the buttons' mnemonics.
var inboxTabNames = [inboxTabs]string{"autodoc.dialog.inbox.tab_all", "autodoc.dialog.inbox.tab_unread",
	"autodoc.dialog.inbox.tab_requests", "autodoc.dialog.inbox.tab_undeliverable"}

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

// unread is a report not read yet: the Unread tab's, and what Open marks read.
func (it inboxItem) unread() bool { return it.kind != "request" && it.state == "open" }

// openRequest is a request not answered, dismissed or expired: Reply's and Dismiss's.
func (it inboxItem) openRequest() bool { return it.kind == "request" && it.state == "open" }

// inboxList is one source's messages read so far, newest first, and whether an older page may be
// there.
type inboxList struct {
	items []inboxItem
	read  bool   // its first page was read, since the last change
	more  bool   // More may read an older page: the last page read held some
	gen   uint64 // its latest read's, or the latest ack or dismiss's: an older read's answer is dropped
	busy  bool   // a read is in flight: More waits for it
}

// inboxView is the dialog. Its fields are the loop's.
type inboxView struct {
	c       inboxCore
	replies inboxReplies
	rows    *rows[inboxItem]
	now     func() time.Time
	isOpen  bool   // the dialog is open
	gen     uint64 // bumped by each open and close: an earlier opening's answers are dropped
	tabOf   int
	cursor  int
	key     string    // the message under the cursor: a re-read keeps the cursor on it
	history inboxList // inbox.list's history: the All, Unread and Open requests tabs read it
	undeliv inboxList // inbox.list's undeliverable
	// reports and requests are how many are open, as the crew inbox last said
	reports, requests int
}

func newInboxView(c inboxCore, replies inboxReplies) *inboxView {
	v := &inboxView{c: c, replies: replies, now: time.Now}
	v.rows = newRows(tuidecl.NewListModel("key", "mark", "kind", "from", "subject", "state", "when"), v.rowOf)
	return v
}

func (*inboxView) Name() string { return "inbox" }

func (v *inboxView) State() map[string]any {
	return map[string]any{"App.inbox": v.rows.Model(), "App.inboxIndex": -1, "App.inboxCanMore": false, "App.inboxTitle": "",
		"App.inboxDetail": "", "App.inboxStatus": ""}
}

func (v *inboxView) Commands() map[string]Command {
	return map[string]Command{
		"App.openInbox":     none(v.open),
		"App.inboxClosed":   none(v.closed),
		"App.inboxMore":     none(v.more),
		"App.inboxTab":      oneNumber("a tab", v.tab),
		"App.inboxShown":    oneNumber("a row", v.shown),
		"App.inboxOpen":     oneNumber("a row", v.openItem),
		"App.inboxMarkRead": oneNumber("a row", v.markRead),
		"App.inboxReply":    oneNumber("a row", v.reply),
		"App.inboxDismiss":  oneNumber("a row", v.dismiss),
		"App.inboxSender":   oneNumber("a row", v.sender),
	}
}

// Catalog is the inbox's user command, :inbox: it runs what App.openInbox runs.
func (v *inboxView) Catalog() []Entry {
	e := builtin("crew.inbox", "the inbox: your mail (SPC a i)", "App.openInbox()", v.open)
	e.Verb = "inbox"
	return []Entry{e}
}

// Keys: SPC a i, on the agents' card.
func (*inboxView) Keys() []Key {
	return []Key{{Card: agentsCard, Seq: "i", Show: "i", Does: "the inbox: your mail", Run: "App.openInbox()"}}
}

// Cards: none of its own; its key is on the agents' card.
func (*inboxView) Cards() []Card { return nil }

// open reads the first page of the tab shown, afresh, and opens the dialog.
func (v *inboxView) open() {
	v.gen++
	v.isOpen = true
	v.cursor, v.key = 0, ""
	v.history, v.undeliv = inboxList{gen: v.history.gen}, inboxList{gen: v.undeliv.gen}
	v.c.Set("App.inboxStatus", v.c.Tr("autodoc.text.inbox.reading"))
	v.c.Set("App.inboxDetail", "")
	v.title()
	v.render()
	v.read(true)
	v.c.Open(inboxDialog)
}

// closed is the dialog dismissed: a change read no more.
func (v *inboxView) closed() {
	v.isOpen = false
	v.gen++
}

// openSet is the crew inbox's count of what is open: its reports and requests.
func (v *inboxView) openSet(reports, requests int) {
	v.reports, v.requests = reports, requests
	v.title()
}

// changed is the crew inbox's word that the user's mail may have changed (a message's state, a new
// one, an undeliverable notice): the dialog, open, reads the first page of the tab shown again, and
// the other source's at its next showing.
func (v *inboxView) changed() {
	if v.isOpen {
		hidden := &v.undeliv
		if v.tabOf == inboxUndeliverable {
			hidden = &v.history
		}
		// read again at its next showing; a read of it in flight, from before the change, is dropped
		hidden.read, hidden.busy = false, false
		hidden.gen++
		v.read(true)
	}
}

// tab shows tab t; Undeliverable reads its first page the first time.
func (v *inboxView) tab(t int) {
	if t < 0 || t >= inboxTabs {
		return
	}
	v.tabOf, v.cursor, v.key = t, 0, ""
	v.title()
	if !v.list().read {
		v.read(true)
		return
	}
	v.render()
}

// more reads the page below the oldest message listed, while one may be there (never on
// Undeliverable, whose daemon side answers its newest only), and not while a read is in flight (what
// it pages below isn't known yet).
func (v *inboxView) more() {
	if l := v.list(); l.read && l.more && !l.busy {
		v.read(false)
	}
}

// list is the source the tab shown reads.
func (v *inboxView) list() *inboxList {
	if v.tabOf == inboxUndeliverable {
		return &v.undeliv
	}
	return &v.history
}

// read reads the tab's source: its first page, which becomes the list (first), or the page below the
// oldest listed, added under it. The read takes the source's next generation: its answer is dropped
// when a later read or an ack or dismiss has landed since, or the dialog closed.
func (v *inboxView) read(first bool) {
	gen, undeliv := v.gen, v.tabOf == inboxUndeliverable
	l := v.list()
	before := int64(0)
	if !first && len(l.items) > 0 {
		before = l.items[len(l.items)-1].seq
	}
	filter := ""
	if undeliv {
		filter = "undeliverable"
	}
	l.gen++
	l.busy = true
	read := l.gen
	v.c.Go(func(ctx context.Context) any {
		res, err := v.c.Call(ctx, "inbox.list", filter, before, int64(inboxPage))
		if err != nil {
			return err
		}
		var page []inboxItem
		for _, x := range asList(asMap(res)["items"]) {
			m := asMap(x)
			page = append(page, inboxItem{message: str(m, "message"), kind: str(m, "kind"), from: str(m, "from"),
				scope: str(m, "scope"), session: str(m, "session"), subject: str(m, "subject"), body: str(m, "body"),
				replyTo: str(m, "reply_to"), state: str(m, "state"), created: str(m, "created"), status: str(m, "status"),
				why: str(m, "why"), seq: num(m, "seq")})
		}
		return page
	}, func(res any) {
		if !v.isOpen || v.gen != gen || l.gen != read {
			return // an earlier opening's, or overtaken by a later read, ack or dismiss
		}
		l.busy = false
		if err, ok := res.(error); ok {
			v.c.Set("App.inboxStatus", v.c.Trf("autodoc.text.inbox.not_listed", wireMessage(err)))
			return
		}
		page, _ := res.([]inboxItem)
		if first {
			l.items, l.read = nil, true
		}
		l.items = mergeBySeq(l.items, page)
		// inbox.list says nothing of what is left (a page is bounded by bytes too, so a short page
		// isn't the end): a page that held some may have more under it, and an empty More ends it
		l.more = !undeliv && len(page) > 0
		v.render()
	})
}

// mergeBySeq is items with page under it: a message read again replaces its row, a new one is
// added, each once, newest first.
func mergeBySeq(items, page []inboxItem) []inboxItem {
	at := make(map[int64]int, len(items))
	out := slices.Clone(items)
	for i, it := range out {
		at[it.seq] = i
	}
	for _, it := range page {
		if i, ok := at[it.seq]; ok {
			out[i] = it
			continue
		}
		at[it.seq] = len(out)
		out = append(out, it)
	}
	slices.SortStableFunc(out, bySeq)
	return out
}

// bySeq orders messages newest first, by seq.
func bySeq(a, b inboxItem) int { return cmp.Compare(b.seq, a.seq) }

// visible is what the tab shown lists, of what was read.
func (v *inboxView) visible() []inboxItem {
	l := v.list()
	switch v.tabOf {
	case inboxUnread:
		return slices.DeleteFunc(slices.Clone(l.items), func(it inboxItem) bool { return !it.unread() })
	case inboxRequests:
		return slices.DeleteFunc(slices.Clone(l.items), func(it inboxItem) bool { return !it.openRequest() })
	}
	return l.items
}

// render lists the tab shown, the cursor on the message it was on (else the row it was at), and says
// how much is listed under it.
func (v *inboxView) render() {
	items := v.visible()
	v.rows.Reset(items)
	if i := slices.IndexFunc(items, func(it inboxItem) bool { return it.message == v.key }); v.key != "" && i >= 0 {
		v.cursor = i
	}
	v.cursor = min(max(v.cursor, 0), max(len(items)-1, 0))
	v.c.Set("App.inboxIndex", -1) // moved away first, so the same row twice still moves the table's cursor
	v.c.Set("App.inboxIndex", v.cursor)
	v.c.Set("App.inboxCanMore", v.list().read && v.list().more)
	switch l := v.list(); {
	case !l.read:
	case len(items) == 0:
		v.c.Set("App.inboxStatus", v.c.Tr("autodoc.text.inbox.none"))
	case v.tabOf == inboxUndeliverable:
		v.c.Set("App.inboxStatus", v.c.Trf("autodoc.text.inbox.count_newest", len(items), inboxPage))
	case l.more:
		v.c.Set("App.inboxStatus", v.c.Trf("autodoc.text.inbox.count_more", len(items)))
	default:
		v.c.Set("App.inboxStatus", v.c.Trf("autodoc.text.inbox.count", len(items)))
	}
	v.showDetail(v.cursor)
}

// title is the dialog's: the tab shown, and how much is open.
func (v *inboxView) title() {
	name := v.c.Tr(inboxTabNames[v.tabOf])
	if v.reports+v.requests > 0 {
		v.c.Set("App.inboxTitle", v.c.Trf("autodoc.dialog.inbox.title_open", name, v.reports, v.requests))
		return
	}
	v.c.Set("App.inboxTitle", v.c.Trf("autodoc.dialog.inbox.title", name))
}

// rowOf is a message as the list shows it: ● marks a report not read, ? an open request.
func (v *inboxView) rowOf(_ int, it inboxItem) rowOf {
	mark := ""
	switch {
	case it.unread():
		mark = "●"
	case it.openRequest():
		mark = "?"
	}
	subject := it.subject
	if subject == "" {
		subject, _, _ = strings.Cut(strings.TrimSpace(it.body), "\n")
	}
	return rowOf{"key": it.message, "mark": mark, "kind": it.kind, "from": it.from, "subject": subject,
		"state": it.state, "when": v.when(it.created)}
}

// when is a message's time as the list shows it: how long ago today, else its date.
func (v *inboxView) when(created string) string {
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return created
	}
	ago := v.now().Sub(t)
	switch {
	case ago < time.Minute:
		return v.c.Tr("autodoc.text.inbox.just_now")
	case ago < time.Hour:
		return v.c.Trf("autodoc.text.inbox.minutes_ago", int(ago/time.Minute))
	case ago < 24*time.Hour:
		return v.c.Trf("autodoc.text.inbox.hours_ago", int(ago/time.Hour))
	}
	return t.Local().Format("Jan 2 15:04")
}

// shown is the cursor on row i: its message below the list, nothing marked.
func (v *inboxView) shown(i int) {
	if it, ok := v.rows.At(i); ok {
		v.cursor, v.key = i, it.message
	}
	v.showDetail(i)
}

// showDetail shows row i's message as Markdown: who sent it, what it is, then its body.
func (v *inboxView) showDetail(i int) {
	it, ok := v.rows.At(i)
	if !ok {
		v.c.Set("App.inboxDetail", "")
		return
	}
	from := it.from
	if it.scope != "" {
		from += "@" + it.scope
	}
	var b strings.Builder
	b.WriteString(v.c.Trf("autodoc.text.inbox.detail_head", from, it.kind, it.state, it.created))
	if it.subject != "" {
		b.WriteString("\n\n# " + it.subject)
	}
	if it.replyTo != "" {
		b.WriteString("\n\n" + v.c.Trf("autodoc.text.inbox.detail_reply_to", it.replyTo))
	}
	if it.why != "" || v.tabOf == inboxUndeliverable {
		b.WriteString("\n\n" + v.c.Trf("autodoc.text.inbox.detail_undelivered", it.status, it.why))
	}
	b.WriteString("\n\n" + it.body + "\n")
	v.c.Set("App.inboxDetail", b.String())
}

// openItem opens row i: its message below the list and, a report not read yet, marked read. A
// request stays open: Reply or Dismiss answers it.
func (v *inboxView) openItem(i int) {
	it, ok := v.rows.At(i)
	if !ok {
		return
	}
	v.cursor, v.key = i, it.message
	v.showDetail(i)
	if it.unread() {
		v.ack(it)
	}
}

// markRead marks row i read: a report, never a request, which is answered or dismissed.
func (v *inboxView) markRead(i int) {
	it, ok := v.rows.At(i)
	switch {
	case !ok:
	case it.kind == "request":
		v.c.Notify(v.c.Tr("autodoc.text.inbox.request_not_read"))
	case it.unread():
		v.ack(it)
	}
}

// ack marks it read (inbox.ack), and lists it so once the daemon took it.
func (v *inboxView) ack(it inboxItem) {
	gen := v.gen
	v.c.Go(func(ctx context.Context) any {
		_, err := v.c.Call(ctx, "inbox.ack", it.message)
		return err
	}, func(res any) {
		if err, _ := res.(error); err != nil {
			v.c.Notify(v.c.Trf("autodoc.text.inbox.not_marked", it.subject, wireMessage(err)))
			return
		}
		if v.isOpen && v.gen == gen {
			v.setState(it.seq, "read")
		}
	})
}

// setState lists the message at seq in state, as the daemon now has it after the user's ack or
// dismiss: a read in flight since before it is dropped (its source's next generation), so its older
// answer never lists the message as it was; the change the daemon reports reads it again.
func (v *inboxView) setState(seq int64, state string) {
	for _, l := range []*inboxList{&v.history, &v.undeliv} {
		l.gen++
		l.busy = false
		for i := range l.items {
			if l.items[i].seq == seq {
				l.items[i].state = state
			}
		}
	}
	v.render()
}

// reply opens the reply dialog on row i, an open request.
func (v *inboxView) reply(i int) {
	it, ok := v.rows.At(i)
	switch {
	case !ok:
	case !it.openRequest():
		v.c.Notify(v.c.Tr("autodoc.text.inbox.not_a_request"))
	default:
		v.replies.openReply(it.message)
	}
}

// dismiss declines row i, an open request, once the user says so: its sender is told.
func (v *inboxView) dismiss(i int) {
	it, ok := v.rows.At(i)
	switch {
	case !ok:
		return
	case !it.openRequest():
		v.c.Notify(v.c.Tr("autodoc.text.inbox.not_a_request"))
		return
	}
	gen := v.gen
	v.c.Ask(Question{Title: v.c.Tr("autodoc.question.inbox.dismiss"), Text: v.c.Trf("autodoc.question.inbox.dismiss_text", it.from, it.subject),
		Yes: v.c.Tr("autodoc.question.inbox.dismiss_yes"), OnYes: func() {
			v.c.Go(func(ctx context.Context) any {
				_, err := v.c.Call(ctx, "inbox.dismiss", it.message)
				return err
			}, func(res any) {
				if err, _ := res.(error); err != nil {
					v.c.Notify(v.c.Trf("autodoc.text.inbox.not_dismissed", it.subject, wireMessage(err)))
					return
				}
				if v.isOpen && v.gen == gen {
					v.setState(it.seq, "dismissed")
				}
			})
		}})
}

// sender shows the agent that sent row i: here when it is this window's, else the window that shows
// it is asked to.
func (v *inboxView) sender(i int) {
	it, ok := v.rows.At(i)
	switch {
	case !ok:
	case it.session == "":
		v.c.Notify(v.c.Trf("autodoc.text.inbox.no_sender", it.from))
	default:
		v.replies.raiseAttention(it.session, it.from)
	}
}

// String is the inbox's state, for a failing cell.
func (v *inboxView) String() string {
	return fmt.Sprintf("inbox{tab %d, %d listed, shown %v}", v.tabOf, v.rows.Len(), v.isOpen)
}
