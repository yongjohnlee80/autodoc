package tui

import (
	"context"
	"fmt"
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
	NotifyOngoingClick(id, msg string, onClick func())
	NotifyDone(id, msg string)
}

// crewOpen is one entry of the open set: what its toast says, and the agent it is about.
type crewOpen struct {
	text    string
	session string // the agent's session, for a click to raise it
	done    string // what the toast says once it leaves the set
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
}

func newCrewInbox(c crewInboxCore, raise func(string) bool) *crewInbox {
	return &crewInbox{c: c, raise: raise, open: map[string]crewOpen{}, reports: map[string]bool{}}
}

func (*crewInbox) Name() string                 { return "crewInbox" }
func (*crewInbox) State() map[string]any        { return map[string]any{} }
func (*crewInbox) Commands() map[string]Command { return map[string]Command{} }

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

// snapshotParts gathers a snapshot's pages until its last, each entry cut to what its toast says:
// a page's bodies are never kept across the pages.
type snapshotParts struct {
	items     []crewItem
	attention []crewAttention
}

// crewItem is an open request or report, as its toast says it.
type crewItem struct{ id, kind, from, subject, session string }

// crewAttention is a raised attention, as its toast says it.
type crewAttention struct{ session, codename, kind, text string }

// add cuts snap's entries to their toasts' fields and keeps them: requests and reports only.
func (p *snapshotParts) add(snap map[string]any) {
	for _, it := range asList(snap["items"]) {
		m := asMap(it)
		if k := str(m, "kind"); k == "request" || k == "message" {
			p.items = append(p.items, crewItem{id: str(m, "message"), kind: k, from: str(m, "from"), subject: str(m, "subject"),
				session: str(m, "session")})
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
func (x *crewInbox) show(p snapshotParts) {
	now, reports := map[string]crewOpen{}, map[string]bool{}
	for _, it := range p.items {
		switch it.kind {
		case "request":
			now["crew:"+it.id] = crewOpen{text: fmt.Sprintf("%s asks: %s", it.from, it.subject), session: it.session,
				done: fmt.Sprintf("%s's request is closed: %s", it.from, it.subject)}
		case "message":
			reports[it.id] = true
			if !x.reports[it.id] {
				x.c.Notify(fmt.Sprintf("%s reports: %s", it.from, it.subject))
			}
		}
	}
	x.reports = reports // one read, so out of the set, is never back: forgotten
	for _, a := range p.attention {
		now["crew-attention:"+a.session] = crewOpen{text: fmt.Sprintf("%s waits on you (%s): %s", a.codename, a.kind, a.text),
			session: a.session, done: fmt.Sprintf("%s no longer waits on you", a.codename)}
	}
	for id, o := range x.open {
		if _, still := now[id]; !still {
			x.c.NotifyDone(id, o.done)
			delete(x.open, id)
		}
	}
	for id, o := range now {
		if prev, shown := x.open[id]; shown && prev.text == o.text {
			continue
		}
		session := o.session
		x.c.NotifyOngoingClick(id, o.text, func() {
			if !x.raise(session) {
				x.c.Notify("That agent runs in another window, or has exited: answer it there, or in the inbox.")
			}
		})
		x.open[id] = o
	}
}
