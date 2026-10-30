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
	c       crewInboxCore
	raise   func(session string) bool // raises this window's agent of session; false when it isn't here
	gen     uint64                    // bumped by each connection: an earlier connection's loop stops
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
// was away is in it.
func (x *crewInbox) Connected() {
	x.gen++
	x.cursor = 0
	x.watch(x.gen, "")
}

// watchAnswer is one inbox.watch's answer, or why there is none.
type watchAnswer struct {
	m   map[string]any
	err error
}

// watch asks the daemon for what changed after the cursor, or for a snapshot (cursor 0, or page,
// the snapshot's next part), and goes on from its answer while gen is the connection's.
func (x *crewInbox) watch(gen uint64, page string) {
	cursor := x.cursor
	x.c.Go(func(ctx context.Context) any {
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
		x.answered(gen, page, r.m)
	})
}

// snapshotParts gathers a snapshot's pages until its last.
type snapshotParts struct {
	items, attention []any
}

// answered applies one watch's answer and asks the next.
func (x *crewInbox) answered(gen uint64, page string, m map[string]any) {
	if snap, ok := m["snapshot"].(map[string]any); ok {
		if page == "" {
			x.parts = snapshotParts{}
		}
		x.parts.items = append(x.parts.items, asList(snap["items"])...)
		x.parts.attention = append(x.parts.attention, asList(snap["attention"])...)
		if next := str(snap, "next"); next != "" {
			x.watch(gen, next) // the snapshot's next part, at the same cursor
			return
		}
		x.cursor = num(m, "cursor")
		x.show(x.parts)
		x.parts = snapshotParts{}
		x.watch(gen, "")
		return
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
		m := asMap(it)
		id, from, subject := str(m, "message"), str(m, "from"), str(m, "subject")
		switch str(m, "kind") {
		case "request":
			now["crew:"+id] = crewOpen{text: fmt.Sprintf("%s asks: %s", from, subject), session: str(m, "session"),
				done: fmt.Sprintf("%s's request is closed: %s", from, subject)}
		case "message":
			reports[id] = true
			if !x.reports[id] {
				x.c.Notify(fmt.Sprintf("%s reports: %s", from, subject))
			}
		}
	}
	x.reports = reports // one read, so out of the set, is never back: forgotten
	for _, a := range p.attention {
		m := asMap(a)
		session := str(m, "session")
		now["crew-attention:"+session] = crewOpen{text: fmt.Sprintf("%s waits on you (%s): %s", str(m, "codename"), str(m, "kind"), str(m, "text")),
			session: session, done: fmt.Sprintf("%s no longer waits on you", str(m, "codename"))}
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
