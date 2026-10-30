package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// inboxWindow is a window's crewInboxCore over a scripted daemon: each inbox.watch takes the next
// answer; work waits until step runs it, so the long-poll loop runs one turn at a time.
type inboxWindow struct {
	answers []any   // map[string]any or error, one per inbox.watch, in order
	calls   [][]any // each inbox.watch's params
	pending []inboxWork
	// block, when set, holds each inbox.watch until its context ends, telling block the context
	// first: a long-poll the daemon holds
	block  chan context.Context
	timers []func()
	notes  []string
	shown  map[string]string // the ongoing toasts, by id
	clicks map[string]func()
	ended  map[string]string // the ended toasts' last messages, by id
	raised []string          // the sessions raise was asked for
	here   map[string]bool   // the sessions whose agents are this window's
}

func newInboxWindow() *inboxWindow {
	return &inboxWindow{shown: map[string]string{}, clicks: map[string]func(){}, ended: map[string]string{}, here: map[string]bool{}}
}

// Call answers inbox.watch from the script. A next page's answer echoes the cursor it was asked
// at, as the daemon's does (inbox.Manager.snapshotPage), whatever the script says.
func (w *inboxWindow) Call(ctx context.Context, verb string, p ...any) (any, error) {
	if verb != "inbox.watch" {
		return nil, errors.New("no verb " + verb)
	}
	w.calls = append(w.calls, p)
	if w.block != nil {
		w.block <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if len(w.answers) == 0 {
		return nil, errors.New("the script has no answer left")
	}
	a := w.answers[0]
	w.answers = w.answers[1:]
	if err, ok := a.(error); ok {
		return nil, err
	}
	if m, ok := a.(map[string]any); ok && p[2] != "" {
		m["cursor"] = p[0]
	}
	return a, nil
}

// inboxWork is one Go: its work, off the loop, and its answer, on it.
type inboxWork struct {
	fn   func(context.Context) any
	then func(any)
}

func (w *inboxWindow) Go(fn func(context.Context) any, then func(any)) {
	w.pending = append(w.pending, inboxWork{fn, then})
}
func (w *inboxWindow) After(_ time.Duration, fn func()) { w.timers = append(w.timers, fn) }
func (w *inboxWindow) Notify(msg string)                { w.notes = append(w.notes, msg) }
func (w *inboxWindow) NotifyOngoingClick(id, msg string, onClick func()) {
	w.shown[id], w.clicks[id] = msg, onClick
}
func (w *inboxWindow) NotifyDone(id, msg string) { delete(w.shown, id); w.ended[id] = msg }
func (w *inboxWindow) raise(session string) bool {
	w.raised = append(w.raised, session)
	return w.here[session]
}

// step answers the oldest watch in flight; false when none is.
func (w *inboxWindow) step(t *testing.T) {
	t.Helper()
	if len(w.pending) == 0 {
		t.Fatal("no watch in flight")
	}
	f := w.pending[0]
	w.pending = w.pending[1:]
	f.then(f.fn(context.Background()))
}

func snapshot(cursor int64, next string, items, attention []any) map[string]any {
	return map[string]any{"cursor": cursor, "snapshot": map[string]any{"items": items, "attention": attention, "next": next}}
}

func changes(cursor int64, kinds ...string) map[string]any {
	var cs []any
	for i, k := range kinds {
		cs = append(cs, map[string]any{"seq": cursor - int64(len(kinds)-1-i), "kind": k, "ref": "s1"})
	}
	return map[string]any{"cursor": cursor, "changes": cs}
}

func request(id, from, subject, session string) map[string]any {
	return map[string]any{"message": id, "kind": "request", "from": from, "subject": subject, "session": session, "state": "open"}
}

func report(id, from, subject string) map[string]any {
	return map[string]any{"message": id, "kind": "message", "from": from, "subject": subject, "state": "open"}
}

// TestTheToastsFollowTheOpenSet (§4.8): a snapshot's open request and raised attention show as
// ongoing toasts and a report as one passing toast; a change that alters the set reads the snapshot
// again, and a request no longer open ends its toast, the report never announced twice.
func TestTheToastsFollowTheOpenSet(t *testing.T) {
	w := newInboxWindow()
	attention := map[string]any{"session": "s9", "codename": "tifa", "kind": "permission", "text": "edit main.go?"}
	w.answers = []any{
		snapshot(5, "", []any{request("r1", "gold", "Decision: which db?", "s1"), report("m1", "lector", "Checkpoint: done")}, []any{attention}),
		changes(6, "answered"),
		snapshot(6, "", []any{report("m1", "lector", "Checkpoint: done")}, []any{attention}),
	}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	if !strings.Contains(w.shown["crew:r1"], "gold asks: Decision: which db?") || !strings.Contains(w.shown["crew-attention:s9"], "tifa waits on you") {
		t.Fatalf("toasts %v, want the request and the attention", w.shown)
	}
	if len(w.notes) != 1 || !strings.Contains(w.notes[0], "lector reports: Checkpoint: done") {
		t.Fatalf("notes %v, want one passing toast for the report", w.notes)
	}
	w.step(t) // the change
	w.step(t) // the snapshot read again
	if _, open := w.shown["crew:r1"]; open || w.ended["crew:r1"] == "" {
		t.Fatalf("the answered request's toast: shown %v, ended %v; want it ended", w.shown, w.ended)
	}
	if _, open := w.shown["crew-attention:s9"]; !open || len(w.notes) != 1 {
		t.Fatalf("after the change: shown %v, notes %v; want the attention kept and the report not announced again", w.shown, w.notes)
	}
	want := []int64{0, 5, 0} // a snapshot, the changes after it, the snapshot again
	for i, c := range w.calls {
		if c[0] != want[i] || c[1] != crewInboxWait.Milliseconds() {
			t.Errorf("watch %d: %v, want cursor %d and the daemon's longest wait", i, c, want[i])
		}
	}
}

// TestASnapshotIsReadToItsLastPage: a snapshot in parts is read through its next pages, at its first
// page's cursor (the journal's high-water, which a next page echoes), before any toast follows it;
// the watch then goes on from that cursor.
func TestASnapshotIsReadToItsLastPage(t *testing.T) {
	w := newInboxWindow()
	w.answers = []any{
		snapshot(9, "a:s1", []any{request("r1", "gold", "first", "s1")}, nil),
		snapshot(9, "", []any{request("r2", "jarvis", "second", "s2")}, nil),
	}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	if len(w.shown) != 0 {
		t.Fatalf("toasts %v before the snapshot's last page", w.shown)
	}
	w.step(t)
	if w.calls[1][0] != int64(9) || w.calls[1][2] != "a:s1" || len(w.shown) != 2 {
		t.Fatalf("the next page: %v, toasts %v; want page a:s1 at the first page's cursor, both requests shown", w.calls[1], w.shown)
	}
	w.step(t) // the watch after the snapshot: at its high-water, never a snapshot again
	if got := w.calls[2]; got[0] != int64(9) || got[2] != "" {
		t.Fatalf("the watch after the snapshot asked %v; want the changes after cursor 9", got)
	}
}

// TestAClickAndAFocusRaiseTheAgent: a toast's click raises the agent when it is this window's, and
// says where to answer when it isn't; a focus change raises the agent it names.
func TestAClickAndAFocusRaiseTheAgent(t *testing.T) {
	w := newInboxWindow()
	w.here["s1"] = true
	w.answers = []any{
		snapshot(3, "", []any{request("r1", "gold", "here", "s1"), request("r2", "jarvis", "elsewhere", "s2")}, nil),
		changes(4, "focus"),
	}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	w.clicks["crew:r1"]()
	w.clicks["crew:r2"]()
	if len(w.raised) != 2 || len(w.notes) != 1 || !strings.Contains(w.notes[0], "another window") {
		t.Fatalf("clicks: raised %v, notes %v; want both asked, one saying the agent is elsewhere", w.raised, w.notes)
	}
	w.step(t)
	if w.raised[len(w.raised)-1] != "s1" {
		t.Fatalf("a focus change raised %v, want s1", w.raised)
	}
}

// TestTheWatchRecoversAndKeepsQuiet: a failed watch is tried again after a wait; a daemon without
// the inbox (method not found) stops the watch quietly until the next connection; an answer for an
// earlier connection is dropped.
func TestTheWatchRecoversAndKeepsQuiet(t *testing.T) {
	w := newInboxWindow()
	w.answers = []any{errors.New("the connection dropped"), snapshot(1, "", nil, nil)}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	if len(w.timers) != 1 || len(w.pending) != 0 {
		t.Fatalf("after a failure: %d timers, %d in flight; want one retry waiting", len(w.timers), len(w.pending))
	}
	w.timers[0]()
	w.step(t)
	if len(w.calls) != 2 {
		t.Fatalf("the retry: %d watches", len(w.calls))
	}

	q := newInboxWindow()
	q.answers = []any{&golibrpc.Error{Code: golibrpc.CodeMethodNotFound, Message: "unknown method: inbox.watch"}}
	quiet := newCrewInbox(q, q.raise)
	quiet.Connected()
	q.step(t)
	if len(q.timers) != 0 || len(q.pending) != 0 || len(q.notes) != 0 {
		t.Fatalf("a daemon without the inbox: %d timers, %d in flight, notes %v; want nothing", len(q.timers), len(q.pending), q.notes)
	}

	s := newInboxWindow()
	s.answers = []any{snapshot(1, "", []any{request("r1", "gold", "stale", "s1")}, nil), snapshot(2, "", nil, nil)}
	stale := newCrewInbox(s, s.raise)
	stale.Connected()
	stale.Connected() // a reconnect before the first watch answered
	s.step(t)         // the first connection's answer: dropped
	if len(s.shown) != 0 {
		t.Fatalf("an earlier connection's answer showed %v", s.shown)
	}
}

// TestALostSnapshotPageReadsTheSnapshotAfresh: a snapshot's next part that fails is not asked for
// again (its page may be gone): the snapshot is read afresh from its first part, and only the set
// it gives shows.
func TestALostSnapshotPageReadsTheSnapshotAfresh(t *testing.T) {
	w := newInboxWindow()
	w.answers = []any{snapshot(5, "p2", []any{request("r1", "gold", "first", "s1")}, nil), errors.New("the connection dropped"),
		snapshot(6, "", []any{request("r2", "gold", "second", "s1")}, nil)}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t) // the first part
	w.step(t) // the next part fails
	if len(w.timers) != 1 {
		t.Fatalf("after the failed part: %d timers, want a retry waiting", len(w.timers))
	}
	w.timers[0]()
	w.step(t)
	if got := w.calls[2]; got[0] != int64(0) || got[2] != "" {
		t.Fatalf("the retry asked %v; want the snapshot afresh (cursor 0, no page)", got)
	}
	if len(w.shown) != 1 || w.shown["crew:r2"] == "" {
		t.Fatalf("shown %v; want the fresh snapshot's set alone", w.shown)
	}
}

// TestAReadReportIsForgotten: the reports announced are the open set's: one read leaves it, and is
// forgotten, so the window keeps no record of every report it ever saw.
func TestAReadReportIsForgotten(t *testing.T) {
	w := newInboxWindow()
	w.answers = []any{snapshot(1, "", []any{report("m1", "gold", "done")}, nil), changes(2, "read"), snapshot(3, "", nil, nil)}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	if len(x.reports) != 1 || len(w.notes) != 1 {
		t.Fatalf("the report: %v, notes %v", x.reports, w.notes)
	}
	w.step(t) // read
	w.step(t) // the snapshot again
	if len(x.reports) != 0 {
		t.Fatalf("a read report is still remembered: %v", x.reports)
	}
}

// TestAnEarlierConnectionsWatchHoldsNothing: a reconnect cancels the earlier connection's watch: one
// not sent yet sends nothing, and one the daemon holds is let go at once, so the window never holds
// a long-poll past its own (the daemon allows two a connection); the window's stop cancels its watch.
func TestAnEarlierConnectionsWatchHoldsNothing(t *testing.T) {
	w := newInboxWindow()
	x := newCrewInbox(w, w.raise)
	x.Connected()
	x.Connected() // before the first watch was sent
	w.answers = []any{snapshot(1, "", nil, nil)}
	w.step(t) // the first connection's: sends nothing
	if len(w.calls) != 0 {
		t.Fatalf("the earlier connection's watch sent %v", w.calls)
	}
	w.block = make(chan context.Context, 1)
	held := w.pending[0]
	w.pending = w.pending[1:]
	done := make(chan any, 1)
	go func() { done <- held.fn(context.Background()) }()
	select {
	case <-w.block:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch never reached the daemon")
	}
	x.Connected() // a reconnect while the daemon holds it
	select {
	case v := <-done:
		held.then(v)
	case <-time.After(5 * time.Second):
		t.Fatal("the earlier connection's watch still holds its long-poll")
	}
	w.block = nil
	x.Stopped()
	w.step(t) // the newest connection's, after the stop: sends nothing
	if len(w.calls) != 1 {
		t.Fatalf("calls %v; want the one held watch alone", w.calls)
	}
}

// TestAnEmptyJournalClosesWhatWasShown: a watch at cursor 0 answered with no snapshot (a new store,
// its journal empty) has nothing open: the toasts shown from before end.
func TestAnEmptyJournalClosesWhatWasShown(t *testing.T) {
	w := newInboxWindow()
	w.answers = []any{snapshot(4, "", []any{request("r1", "gold", "old", "s1")}, nil), map[string]any{"cursor": int64(0), "changes": []any{}}}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	if len(w.shown) != 1 {
		t.Fatalf("shown %v", w.shown)
	}
	x.Connected() // to a daemon whose store is new
	w.step(t)
	w.step(t)
	if len(w.shown) != 0 || w.ended["crew:r1"] == "" {
		t.Fatalf("after an empty journal: shown %v, ended %v; want the old request's toast ended", w.shown, w.ended)
	}
}
