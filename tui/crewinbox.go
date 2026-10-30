package tui

import (
	"context"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// THE HUMAN'S CREW INBOX IN A WINDOW (ADR 1793271500 §4.8).
//
// Each window holds one inbox.watch long-poll, and every window shows the same thing: a toast for
// each open decision request and each raised attention (an agent waiting on a permission prompt),
// which ends in every window when the request is answered, dismissed or expires, or the attention
// clears; and one passing toast for each new report. The window keeps the open set, read from the
// daemon's snapshot, and reads the snapshot again whenever a change can alter it: the toasts follow
// the set, so a lost change or a reconnect never leaves a stale toast.

// crewInboxWait is how long one inbox.watch waits for a change: the daemon's most.
const crewInboxWait = 25 * time.Second

// crewInboxRetry is the wait after a failed watch, before the next.
const crewInboxRetry = 5 * time.Second

// crewInboxCore is what the window's inbox needs of the program.
type crewInboxCore interface {
	Call(ctx context.Context, verb string, params ...any) (any, error)
	Go(fn func(context.Context) any, then func(any))
	After(d time.Duration, fn func())
	Notify(msg string)
	Tr(id string) string
	Trf(id string, args ...any) string
	NotifyOngoingClick(id, msg string, onClick func())
	NotifyDone(id, msg string)
	Set(key string, v any)
	Open(id string)
	Close(id string)
	Prefs() prefs
	SetPref(name, value string)
	Bell()
	// SetText sets the text of the layout's text area id (the reply's, at each opening)
	SetText(id, text string)
}

// crewOpen is one entry of the open set: what its toast says, and what its click does.
type crewOpen struct {
	text  string
	rings bool // a request or a permission prompt: newly open, it rings the bell (crewbell.go)
	click func()
	done  string // what the toast says once it leaves the set
}

// crewInbox is the window's view of the human's inbox. Its fields are the loop's.
type crewInbox struct {
	c     crewInboxCore
	raise func(session string) bool // raises this window's agent of session; false when it isn't here
	gen   uint64                    // bumped by each connection: an earlier connection's loop stops
	// genCtx is the connection's: cancelled by the next connection or the window's stop, so an
	// earlier connection's watch, in flight or not yet sent, holds no long-poll on the new one
	genCtx  context.Context
	stopGen context.CancelFunc
	cursor  int64
	open    map[string]crewOpen // the toasts shown, by toast id
	reports map[string]bool     // the open reports a passing toast has announced, by message
	parts   snapshotParts       // a snapshot's pages read so far
	// requests is the open requests, by message; replying the one the reply dialog is open on, ""
	// when it is closed; drafts the replies not sent, by request (crewreply.go)
	requests map[string]crewRequest
	replying string
	drafts   map[string]string
	// sending is each request's latest send, by attempt (attempt counts them): only its failure
	// keeps a draft
	sending map[string]uint64
	attempt uint64
	now     func() time.Time // the bell's clock
	rang    time.Time        // the last ring
}

func newCrewInbox(c crewInboxCore, raise func(string) bool) *crewInbox {
	return &crewInbox{c: c, raise: raise, open: map[string]crewOpen{}, reports: map[string]bool{},
		requests: map[string]crewRequest{}, drafts: map[string]string{}, sending: map[string]uint64{}, now: time.Now}
}

func (*crewInbox) Name() string            { return "crewInbox" }
func (x *crewInbox) State() map[string]any { return x.replyState() }

// Connected starts the window's watch on each connection, from a snapshot: what changed while it
// was away is in it. The earlier connection's watch is cancelled, and its snapshot's pages read so
// far dropped.
func (x *crewInbox) Connected() {
	x.endGen()
	x.gen++
	x.genCtx, x.stopGen = context.WithCancel(context.Background())
	x.cursor, x.parts = 0, snapshotParts{}
	x.watch(x.gen, "")
}

// Stopped cancels the watch: the window is closing.
func (x *crewInbox) Stopped() { x.endGen() }

func (x *crewInbox) endGen() {
	if x.stopGen != nil {
		x.stopGen()
	}
}

// watchAnswer is one inbox.watch's answer, or why there is none.
type watchAnswer struct {
	m   map[string]any
	err error
}

// watch asks the daemon for what changed after the cursor, or for a snapshot (cursor 0, or page,
// the snapshot's next part), and goes on from its answer while gen is the connection's.
func (x *crewInbox) watch(gen uint64, page string) {
	cursor, genCtx := x.cursor, x.genCtx
	x.c.Go(func(ctx context.Context) any {
		if genCtx.Err() != nil {
			return watchAnswer{err: genCtx.Err()} // a newer connection's loop runs: nothing is sent
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(genCtx, cancel)()
		v, err := x.c.Call(ctx, "inbox.watch", cursor, crewInboxWait.Milliseconds(), page)
		m, _ := v.(map[string]any)
		return watchAnswer{m, err}
	}, func(v any) {
		if gen != x.gen {
			return // a newer connection's loop runs
		}
		r, _ := v.(watchAnswer)
		if r.err != nil || r.m == nil {
			if code(r.err) == golibrpc.CodeMethodNotFound {
				return // a daemon before crew: the next connection asks again
			}
			x.c.After(crewInboxRetry, func() {
				if gen == x.gen {
					if page != "" { // a snapshot's part, its page perhaps gone: the snapshot afresh
						x.cursor = 0
					}
					x.watch(gen, "")
				}
			})
			return
		}
		x.answered(gen, cursor, page, r.m)
	})
}

// snapshotParts gathers a snapshot's pages until its last, each entry cut to what its toast (or a
// request's reply dialog) says: a request's body is kept, for the dialog; a report's never is.
type snapshotParts struct {
	items     []crewItem
	attention []crewAttention
}

// crewItem is an open request or report, as its toast says it.
type crewItem struct{ id, kind, from, subject, session, body string } // body: a request's, for its reply

// crewAttention is a raised attention, as its toast says it.
type crewAttention struct{ session, codename, kind, text string }

// add cuts snap's entries to their toasts' fields and keeps them: requests and reports only.
func (p *snapshotParts) add(snap map[string]any) {
	for _, it := range asList(snap["items"]) {
		m := asMap(it)
		if k := str(m, "kind"); k == "request" || k == "message" {
			it := crewItem{id: str(m, "message"), kind: k, from: str(m, "from"), subject: str(m, "subject"), session: str(m, "session")}
			if k == "request" {
				it.body = str(m, "body") // the reply dialog shows it; a report's is never kept
			}
			p.items = append(p.items, it)
		}
	}
	for _, a := range asList(snap["attention"]) {
		m := asMap(a)
		p.attention = append(p.attention, crewAttention{session: str(m, "session"), codename: str(m, "codename"),
			kind: str(m, "kind"), text: str(m, "text")})
	}
}

// answered applies one watch's answer to a watch at cursor asked, and asks the next. A snapshot's
// cursor is its first page's, the journal's high-water then; a next page echoes the cursor it was
// asked at, so the pages are asked at that one and it is kept through them. A watch at cursor 0
// answered with no snapshot found the journal empty: nothing is open.
func (x *crewInbox) answered(gen uint64, asked int64, page string, m map[string]any) {
	if snap, ok := m["snapshot"].(map[string]any); ok {
		if page == "" {
			x.parts = snapshotParts{}
			x.cursor = num(m, "cursor")
		}
		x.parts.add(snap)
		if next := str(snap, "next"); next != "" {
			x.watch(gen, next) // the snapshot's next part, at its first page's cursor
			return
		}
		x.show(x.parts)
		x.parts = snapshotParts{}
		x.watch(gen, "")
		return
	}
	if asked == 0 && page == "" {
		x.show(snapshotParts{}) // an empty journal: what was shown, from before, is closed
	}
	x.cursor = num(m, "cursor")
	again := false
	for _, ch := range asList(m["changes"]) {
		c := asMap(ch)
		switch str(c, "kind") {
		case "request", "report", "answered", "dismissed", "read", "expired", "attention", "cleared":
			again = true // the open set may differ: read it again
		case "focus":
			x.raise(str(c, "ref")) // the human asked for this agent: raised when it is this window's
		}
	}
	if again {
		x.cursor = 0
	}
	x.watch(gen, "")
}

// show makes the toasts follow the open set p: a toast for each open request and raised attention
// not shown yet, the end of each shown one no longer open, and a passing toast for each new report.
// A request's click opens the reply dialog on it; an attention's shows its agent. The dialog open
// on a request that has left the set closes.
func (x *crewInbox) show(p snapshotParts) {
	now, reports, requests := map[string]crewOpen{}, map[string]bool{}, map[string]crewRequest{}
	for _, it := range p.items {
		switch it.kind {
		case "request":
			requests[it.id] = crewRequest{id: it.id, from: it.from, subject: it.subject, body: it.body}
			id := it.id
			now["crew:"+it.id] = crewOpen{text: x.c.Trf("autodoc.crew.asks", it.from, it.subject), rings: true, click: func() { x.openReply(id) },
				done: x.c.Trf("autodoc.crew.request_closed", it.from, it.subject)}
		case "message":
			reports[it.id] = true
			if !x.reports[it.id] {
				x.c.Notify(x.c.Trf("autodoc.crew.reports", it.from, it.subject))
			}
		}
	}
	x.reports = reports // one read, so out of the set, is never back: forgotten
	for _, a := range p.attention {
		session, codename := a.session, a.codename
		now["crew-attention:"+session] = crewOpen{text: x.waitText(a), rings: a.kind == "permission",
			click: func() { x.raiseAttention(session, codename) }, done: x.c.Trf("autodoc.crew.no_longer_waits", codename)}
	}
	x.requests = requests
	for id := range x.drafts {
		if _, open := requests[id]; !open {
			delete(x.drafts, id) // answered, dismissed or expired: a draft for it can't be sent
		}
	}
	if _, open := requests[x.replying]; x.replying != "" && !open {
		x.replying = ""
		x.c.Close(crewReplyDialog)
		x.c.Notify(x.c.Tr("autodoc.crew.answering_closed"))
	}
	for id, o := range x.open {
		if _, still := now[id]; !still {
			x.c.NotifyDone(id, o.done)
			delete(x.open, id)
		}
	}
	fresh := false
	for id, o := range now {
		prev, shown := x.open[id]
		fresh = fresh || (o.rings && !(shown && prev.rings)) // newly open, or newly asking (blocked, then a permission)
		if shown && prev.text == o.text {
			continue
		}
		x.c.NotifyOngoingClick(id, o.text, o.click)
		x.open[id] = o
	}
	if fresh {
		x.ring() // one ring for all that opened at once
	}
}

// waitText is an attention's toast: a permission prompt's and a blocked agent's in their own words,
// any other kind as the daemon names it.
func (x *crewInbox) waitText(a crewAttention) string {
	switch a.kind {
	case "permission":
		return x.c.Trf("autodoc.crew.waits_permission", a.codename, a.text)
	case "blocked":
		return x.c.Trf("autodoc.crew.waits_blocked", a.codename, a.text)
	}
	return x.c.Trf("autodoc.crew.waits", a.codename, a.kind, a.text)
}
