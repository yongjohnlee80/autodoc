package tui

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// THE WINDOW'S ASSIGNMENT FEED (docs/decisions/crew.md#an-assignment-reaches-a-window-agent).
//
// A task assigned to one of the window's agents, autodoc:<codename>, from outside the window (a bare
// `autodoc --call todos.add` or `todos.assign`, an agent's, a script's) reaches that agent: the
// window reads the daemon's change log (todos.changes) as its own consumer, autodoc-window:<scope>,
// and mails each such assignment to the agent through the crew. Neovim's notifier skips autodoc:
// assignees, so the window is the one that delivers them.
//
//   - Only its own agents: a crew profile in the window's scope by that codename. Another window's
//     agent is its window's.
//   - Not the panel's own: A in the todos panel assigns with the window's scope and hands the task
//     over itself (todos_assign.go), so an assignment made in the window's own scope is skipped
//     here. One made in another window's scope is not: that window's panel may have no crew
//     profile of this agent's to deliver to, and the feed only ever skips what its own panel made.
//   - A notice, never a task the user typed: mail from the user, kind message, subject "[todos] task
//     assigned to you: <title>", the task and the assigner's reason in its body. A peer may have
//     made it, and a request from the user headed Task: is the user's own instruction.
//   - Queued, not fired: the daemon wakes the agent as for any mail, and a stopped agent finds it
//     when it starts. Keyed by the change (todo-<store>-<seq>, as Neovim keys its own), so asking
//     again after a lost answer never mails it twice.
//
// The consumer is the scope's, which the window keeps across restarts (crewhost.go), so the daemon's
// cursor catches up on what was assigned while it was closed. A store this scope has never read
// would replay its whole retained log: there, assignments from before this process started are
// skipped, and the stores read are kept in <agentDir>/.crew-todos/<scope>.
//
// It reads at each claim (a start, a reconnect) and at each todos.changed event. An assignment whose
// send has no outcome yet (a lost answer, a dropped connection) leaves its change unacked, and the
// feed reads again todoFeedRetry later.

const (
	// todoFeedPage is how many changes a read asks for at a time.
	todoFeedPage = 200
	// todoFeedSkew is how far before this process's start a change still counts as news: the
	// daemon's clock may run behind the window's, and the wire's times are whole seconds.
	todoFeedSkew = 5 * time.Second
	// NOTE: can't announce: a send with no outcome says nothing about when the daemon is back, so
	// the feed reads again after a fixed gap (a claim or a change reads sooner).
	todoFeedRetry = 30 * time.Second
)

// todoFeed is the window's consumer of the todo change log. Its fields are the loop's.
type todoFeed struct {
	h        *crewHost
	dir      string    // <agentDir>/.crew-todos: the stores each scope's consumer has read
	start    time.Time // this process's: a store never read skips what was assigned before it
	polling  bool      // a read is running
	again    bool      // something asked for a read while one ran
	retrying bool      // a read again is set for todoFeedRetry
}

// todoFeedRead is what a read left for the loop: whether a send had no outcome.
type todoFeedRead struct{ retry bool }

// poll reads every store's changes, one read at a time: asked during a read, it reads once more
// after it. A window holding no scope reads nothing.
func (f *todoFeed) poll() {
	if f.polling {
		f.again = true
		return
	}
	scope, epoch, ok := f.h.held()
	if !ok {
		return
	}
	f.polling = true
	run, start := f.h.run, f.start
	f.h.c.Go(func(ctx context.Context) any { return f.read(ctx, scope, run, epoch, start) }, func(v any) {
		f.polling = false
		if r, _ := v.(todoFeedRead); r.retry && !f.retrying {
			f.retrying = true
			f.h.c.After(todoFeedRetry, func() { f.retrying = false; f.poll() })
		}
		if f.again {
			f.again = false
			f.poll()
		}
	})
}

// todoFeedOutcome is what became of one assignment: done (sent, or not the window's to send),
// or no outcome yet (its change stays unacked).
type todoFeedOutcome int

const (
	todoDone todoFeedOutcome = iota
	todoRetry
)

// todoFeedStore is one store a read goes through. Off the loop.
type todoFeedStore struct {
	id, path string
	at       map[string]any
	news     bool // never read by this scope: what was assigned before start is history
}

// read is poll's work, off the loop: every store the daemon knows, its changes after the scope's
// cursor delivered and acked.
func (f *todoFeed) read(ctx context.Context, scope, run string, epoch int64, start time.Time) todoFeedRead {
	v, err := f.h.c.Call(ctx, "todos.stores")
	if err != nil {
		return todoFeedRead{} // a daemon without todos, or one away: the next claim or change reads again
	}
	d := &todoDelivery{f: f, scope: scope, run: run, epoch: epoch}
	seenFile := filepath.Join(f.dir, scope)
	seen := readTodoSeen(seenFile)
	var out todoFeedRead
	added := false
	for _, e := range asList(v) {
		m := asMap(e)
		roots := asList(m["roots"])
		if len(roots) == 0 {
			continue
		}
		root, _ := roots[0].(string)
		s := todoFeedStore{id: str(m, "id"), path: str(m, "path"), at: map[string]any{"root": root}}
		if s.id == "" || root == "" {
			continue
		}
		s.news = !seen[s.id]
		outcome, ok := d.store(ctx, s, start)
		if outcome == todoRetry {
			out.retry = true
		}
		if ok && !seen[s.id] {
			seen[s.id], added = true, true
		}
	}
	if added {
		writeTodoSeen(seenFile, seen) // unwritten, the next process skips that store's history again: nothing is sent twice
	}
	return out
}

// todoDelivery is one read's sending: the window's scope, run and epoch, and its own agents, asked
// of the crew once.
type todoDelivery struct {
	f     *todoFeed
	scope string
	run   string
	epoch int64
	own   map[string]bool // the codenames of the crew profiles in the scope; nil until asked
}

// store reads one store's changes after the consumer's cursor and acks what it handled; ok is
// whether it reached the log's head.
func (d *todoDelivery) store(ctx context.Context, s todoFeedStore, start time.Time) (todoFeedOutcome, bool) {
	consumer := "autodoc-window:" + d.scope
	call := d.f.h.c.Call
	ack := func(seq int64) bool {
		_, err := call(ctx, "todos.ack", s.at, consumer, seq)
		return err == nil
	}
	for {
		v, err := call(ctx, "todos.changes", s.at, consumer, int64(todoFeedPage), true)
		if err != nil {
			return todoDone, false
		}
		page := asMap(v)
		cursor := num(page, "cursor")
		if resync, _ := page["resync"].(bool); resync { // away past the daemon's silence: what is assigned now, once
			for _, r := range asList(page["snapshot"]) {
				row := asMap(r)
				key := fmt.Sprintf("todo-%s-r%d-%s", s.id, cursor, str(row, "id"))
				if d.deliver(ctx, s, str(row, "assignee"), str(row, "scope"), str(row, "id"), "", key,
					"Sent again: the window was away too long to know what changed.") == todoRetry {
					return todoRetry, false
				}
			}
			return todoDone, ack(cursor)
		}
		changes := asList(page["changes"])
		acked := int64(-1)
		for _, e := range changes {
			c := asMap(e)
			seq := num(c, "seq")
			if str(c, "kind") == "assign" && !(s.news && beforeStart(str(c, "at"), start)) {
				key := fmt.Sprintf("todo-%s-%d", s.id, seq)
				if d.deliver(ctx, s, str(c, "to"), str(c, "scope"), str(c, "id"), str(c, "reason"), key, "") == todoRetry {
					if acked >= 0 {
						ack(acked)
					}
					return todoRetry, false
				}
			}
			acked = seq
		}
		if acked >= 0 && !ack(acked) {
			return todoDone, false
		}
		if len(changes) < todoFeedPage {
			return todoDone, true
		}
	}
}

// beforeStart reports whether a change made at at (the wire's RFC 3339) came before start, less the
// skew: one that says no time is history too.
func beforeStart(at string, start time.Time) bool {
	t, err := time.Parse(time.RFC3339, at)
	return err != nil || t.Before(start.Add(-todoFeedSkew))
}

// deliver mails one assignment to its assignee when it is one of the window's own agents and no
// window's own panel made it; otherwise it is done with, unsent.
func (d *todoDelivery) deliver(ctx context.Context, s todoFeedStore, assignee, scope, id, reason, key, note string) todoFeedOutcome {
	codename, ok := strings.CutPrefix(assignee, assigneePrefix)
	if !ok || codename == "" || scope == d.scope { // not a window's agent, or this window's panel handed it over
		return todoDone
	}
	if d.own == nil {
		own, err := d.ownAgents(ctx)
		if err != nil {
			return todoRetry
		}
		d.own = own
	}
	if !d.own[codename] {
		return todoDone
	}
	title, file := "", ""
	if v, err := d.f.h.c.Call(ctx, "todos.get", s.at, id); err == nil {
		t := asMap(v)
		title = str(t, "title")
		if p := str(t, "path"); p != "" && s.path != "" {
			file = filepath.Join(s.path, filepath.FromSlash(p))
		}
	}
	subject := "[todos] task assigned to you: " + cmp.Or(title, id)
	_, err := d.f.h.c.Call(ctx, "inbox.send", d.scope, d.run, d.epoch, []any{codename}, todoAssignedBody(id, title, file, reason, note),
		key, MessagePlain, subject, false)
	switch {
	case err == nil:
		return todoDone
	case assignmentRefused(err): // nothing can be sent: an agent gone, a body too long, one sent before with other words
		return todoDone
	}
	return todoRetry
}

// assignmentRefused reports whether the daemon refused an assignment's mail for good.
func assignmentRefused(err error) bool {
	switch code(err) {
	case mail.CodeNoRecipient, mail.CodeTooLarge, inbox.CodeTooLarge, inbox.CodeKeyReused,
		golibrpc.CodeInvalidParams, golibrpc.CodeMethodNotFound:
		return true
	}
	return false
}

// ownAgents are the codenames of the crew profiles in the window's scope (crew.list).
func (d *todoDelivery) ownAgents(ctx context.Context) (map[string]bool, error) {
	v, err := d.f.h.c.Call(ctx, "crew.list")
	if err != nil {
		return nil, err
	}
	own := map[string]bool{}
	for _, e := range asList(v) {
		if m := asMap(e); str(m, "scope") == d.scope {
			own[str(m, "codename")] = true
		}
	}
	return own, nil
}

// todoAssignedBody is the assignment's mail, as Neovim's notifier words its own: the task, then
// the note and the assigner's reason.
func todoAssignedBody(id, title, file, reason, note string) string {
	var b strings.Builder
	b.WriteString("You have been assigned a task.\n\n")
	fmt.Fprintf(&b, "  id        : %s\n", id)
	fmt.Fprintf(&b, "  title     : %s\n", cmp.Or(title, "(untitled)"))
	fmt.Fprintf(&b, "  file_path : %s", cmp.Or(file, "(unknown)"))
	if note != "" {
		b.WriteString("\n\n" + note)
	}
	if reason != "" {
		b.WriteString("\n\nReason: " + reason)
	}
	return b.String()
}

// readTodoSeen is the stores a scope's consumer has read, from its file; none when it can't be read.
func readTodoSeen(file string) map[string]bool {
	seen := map[string]bool{}
	b, err := os.ReadFile(file)
	if err != nil {
		return seen
	}
	var ids []string
	if json.Unmarshal(b, &ids) == nil {
		for _, id := range ids {
			seen[id] = true
		}
	}
	return seen
}

// writeTodoSeen keeps the stores a scope's consumer has read.
func writeTodoSeen(file string, seen map[string]bool) {
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	b, _ := json.Marshal(ids)
	if os.MkdirAll(filepath.Dir(file), 0o700) == nil {
		_ = writeAtomic(file, b, 0o600)
	}
}
