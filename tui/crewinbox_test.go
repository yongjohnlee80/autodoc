package tui

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/inbox"
)

// inboxWindow is a window's crewInboxCore over a scripted daemon: each inbox.watch takes the next
// answer; work waits until step runs it, so the long-poll loop runs one turn at a time.
type inboxWindow struct {
	marked  bool    // Tr and Trf answer the marked test language (markedText), else the English catalog's words
	answers []any   // map[string]any or error, one per inbox.watch, in order
	calls   [][]any // each inbox.watch's params
	pending []inboxWork
	// block, when set, holds each inbox.watch until its context ends, telling block the context
	// first: a long-poll the daemon holds
	block  chan context.Context
	timers []func()
	delays []time.Duration // each timer's wait
	notes  []string
	shown  map[string]string // the ongoing toasts, by id
	clicks map[string]func()
	ended  map[string]string // the ended toasts' last messages, by id
	raised []string          // the sessions raise was asked for
	here   map[string]bool   // the sessions whose agents are this window's
	set    map[string]any    // the state set
	prefs  prefs             // the preferences, as SetPref leaves them
	texts  map[string]string // each text area's text, as SetText leaves it
	bells  int               // the rings
	opened []string          // the dialogs opened, in order
	closed []string          // the dialogs closed, in order
	// verbs is each other verb called, its name then its params; verbAnswers the answers they take,
	// by verb, in order (an error, or nil for success); none left is success
	verbs       [][]any
	verbAnswers map[string][]error
}

func newInboxWindow() *inboxWindow {
	return &inboxWindow{shown: map[string]string{}, clicks: map[string]func(){}, ended: map[string]string{}, here: map[string]bool{},
		set: map[string]any{}, verbAnswers: map[string][]error{}, texts: map[string]string{}}
}

// Call answers inbox.watch from the script. A next page's answer echoes the cursor it was asked
// at, as the daemon's does (inbox.Manager.snapshotPage), whatever the script says.
func (w *inboxWindow) Call(ctx context.Context, verb string, p ...any) (any, error) {
	if verb != "inbox.watch" {
		w.verbs = append(w.verbs, append([]any{verb}, p...))
		if as := w.verbAnswers[verb]; len(as) > 0 {
			w.verbAnswers[verb] = as[1:]
			if as[0] != nil {
				return nil, as[0]
			}
		}
		return map[string]any{}, nil
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
func (w *inboxWindow) After(d time.Duration, fn func()) {
	w.timers, w.delays = append(w.timers, fn), append(w.delays, d)
}
func (w *inboxWindow) Notify(msg string) { w.notes = append(w.notes, msg) }
func (w *inboxWindow) NotifyOngoingClick(id, msg string, onClick func()) {
	w.shown[id], w.clicks[id] = msg, onClick
}
func (w *inboxWindow) NotifyDone(id, msg string) { delete(w.shown, id); w.ended[id] = msg }
func (w *inboxWindow) Set(key string, v any)     { w.set[key] = v }
func (w *inboxWindow) Prefs() prefs              { return w.prefs }
func (w *inboxWindow) SetText(id, text string)   { w.texts[id] = text }
func (w *inboxWindow) Bell()                     { w.bells++ }
func (w *inboxWindow) SetPref(name, value string) {
	if name == prefAgentBell {
		w.prefs.agentBell = value == "true"
	}
}
func (w *inboxWindow) Open(id string)  { w.opened = append(w.opened, id) }
func (w *inboxWindow) Close(id string) { w.closed = append(w.closed, id) }
func (w *inboxWindow) raise(session string) bool {
	w.raised = append(w.raised, session)
	return w.here[session]
}

// stepNth answers the i-th work in flight (0 the oldest), out of order: a late answer.
func (w *inboxWindow) stepNth(t *testing.T, i int) {
	t.Helper()
	if i >= len(w.pending) {
		t.Fatalf("no work %d in flight (%d)", i, len(w.pending))
	}
	f := w.pending[i]
	w.pending = append(w.pending[:i:i], w.pending[i+1:]...)
	f.then(f.fn(context.Background()))
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
	return map[string]any{"message": id, "kind": "request", "from": from, "subject": subject, "session": session, "state": "open",
		"body": "the body of " + subject}
}

func report(id, from, subject string) map[string]any {
	return map[string]any{"message": id, "kind": "message", "from": from, "subject": subject, "state": "open"}
}

// TestTheToastsFollowTheOpenSet (docs/decisions/crew.md#the-human-inbox): a snapshot's open request
// and raised attention show as ongoing toasts and a report as one passing toast; a change that
// alters the set reads the snapshot again, and a request no longer open ends its toast, the report
// never announced twice.
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

// TestAnAttentionClickShowsItsAgent: an attention's click raises its agent when it is this
// window's, and else asks the window that shows it to (inbox.focus), saying so; a focus change
// raises the agent it names.
func TestAnAttentionClickShowsItsAgent(t *testing.T) {
	w := newInboxWindow()
	w.here["s1"] = true
	here := map[string]any{"session": "s1", "codename": "gold", "kind": "permission", "text": "edit?"}
	elsewhere := map[string]any{"session": "s2", "codename": "jarvis", "kind": "permission", "text": "run?"}
	w.answers = []any{snapshot(3, "", nil, []any{here, elsewhere}), changes(4, "focus")}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	w.clicks["crew-attention:s1"]()
	w.clicks["crew-attention:s2"]()
	if !slices.Equal(w.raised, []string{"s1", "s2"}) || len(w.pending) != 2 {
		t.Fatalf("clicks: raised %v, %d in flight; want both asked here, one focus call for s2", w.raised, len(w.pending))
	}
	w.step(t) // the next watch
	w.step(t) // the focus call
	if len(w.verbs) != 1 || w.verbs[0][0] != "inbox.focus" || w.verbs[0][1] != "s2" {
		t.Fatalf("calls %v; want inbox.focus for s2 alone", w.verbs)
	}
	if len(w.notes) != 1 || !strings.Contains(w.notes[0], "another window") {
		t.Fatalf("notes %v; want one saying jarvis's window shows it", w.notes)
	}
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

// TestTheWatchBacksOffAndStopsOnARefusal: failed watches in a row wait 5 s, doubling up to 60 s,
// and an answer starts the waits over; a watch the daemon refuses (its params, or its gate) is not
// asked again until the next connection, and the window says so once, however often it is refused.
func TestTheWatchBacksOffAndStopsOnARefusal(t *testing.T) {
	w := newInboxWindow()
	down := errors.New("the connection dropped")
	w.answers = []any{down, down, down, down, down, down, snapshot(1, "", nil, nil), down}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	for range 6 {
		w.step(t)
		w.timers[len(w.timers)-1]()
	}
	w.step(t) // the answer
	w.step(t) // a failure after it
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute, 5 * time.Second}
	if !slices.Equal(w.delays, want) {
		t.Fatalf("the waits %v, want %v", w.delays, want)
	}
	if len(w.notes) != 0 {
		t.Fatalf("a failing watch said %v; it retries quietly", w.notes)
	}

	r := newInboxWindow()
	refused := &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: "inbox.watch: bad params"}
	r.answers = []any{refused, &golibrpc.Error{Code: golibrpc.CodeAccessDenied, Message: "denied"}}
	y := newCrewInbox(r, r.raise)
	y.Connected()
	r.step(t)
	if len(r.timers) != 0 || len(r.pending) != 0 || len(r.notes) != 1 {
		t.Fatalf("a refused watch: %d timers, %d in flight, notes %v; want none, none, one", len(r.timers), len(r.pending), r.notes)
	}
	y.Connected() // the next connection asks again, and is refused again: nothing more is said
	r.step(t)
	if len(r.calls) != 2 || len(r.timers) != 0 || len(r.notes) != 1 {
		t.Fatalf("refused again: %d watches, %d timers, notes %v; want two, none, still one", len(r.calls), len(r.timers), r.notes)
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

// openOn shows a snapshot holding request r1 (gold's) and clicks its toast: the reply dialog opens
// on it.
func openOn(t *testing.T) (*inboxWindow, *crewInbox) {
	t.Helper()
	w := newInboxWindow()
	w.answers = []any{snapshot(1, "", []any{request("r1", "gold", "which db?", "s1")}, nil)}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	w.clicks["crew:r1"]()
	if !slices.Equal(w.opened, []string{crewReplyDialog}) || w.set["App.replyTitle"] != "gold asks: which db?" ||
		w.set["App.replyText"] != "the body of which db?" {
		t.Fatalf("the click: opened %v, state %v; want the reply dialog on gold's request", w.opened, w.set)
	}
	return w, x
}

// TestARequestsClickOpensItsReply (docs/decisions/crew.md#the-human-inbox): a request's toast opens
// the reply dialog on it, wherever its agent runs; Send is inbox.reply with the request, the body
// and a key of the two, and says it was sent.
func TestARequestsClickOpensItsReply(t *testing.T) {
	w, x := openOn(t)
	x.replySend("postgres")
	w.step(t) // the next watch
	w.step(t) // the reply
	want := []any{"inbox.reply", "r1", "postgres", replyKey("r1", "postgres")}
	if len(w.verbs) != 1 || !slices.Equal(w.verbs[0], want) {
		t.Fatalf("calls %v, want %v", w.verbs, want)
	}
	if len(w.notes) != 1 || !strings.Contains(w.notes[0], "Replied to gold") {
		t.Fatalf("notes %v", w.notes)
	}
	if replyKey("r1", "postgres") == replyKey("r1", "postgres ") || replyKey("r1", "x") == replyKey("r2", "x") {
		t.Fatal("a key does not follow its request and body")
	}
}

// TestALostReplyIsSentAgainAsTheSame: a reply whose answer was lost is kept as the request's draft;
// the dialog opens on it again, and sending it again sends the same key, which the daemon answers
// as the first reply.
func TestALostReplyIsSentAgainAsTheSame(t *testing.T) {
	w, x := openOn(t)
	w.verbAnswers["inbox.reply"] = []error{io.ErrUnexpectedEOF}
	x.replySend("postgres")
	w.step(t)
	w.step(t)
	if len(w.notes) != 1 || !strings.Contains(w.notes[0], "was not sent") {
		t.Fatalf("notes %v", w.notes)
	}
	w.clicks["crew:r1"]()
	if w.texts[crewReplyText] != "postgres" {
		t.Fatalf("the dialog opened again on %q, want the unsent reply", w.texts[crewReplyText])
	}
	x.replySend("postgres")
	w.step(t)
	if len(w.verbs) != 2 || w.verbs[1][3] != w.verbs[0][3] {
		t.Fatalf("calls %v; want the same key sent again", w.verbs)
	}
	if len(x.drafts) != 0 {
		t.Fatalf("drafts %v after the reply was sent", x.drafts)
	}
}

// TestAReplyToAClosedRequestIsNotKept: a request answered, dismissed or expired first is said so,
// and its reply not kept as a draft; Dismiss is inbox.dismiss.
func TestAReplyToAClosedRequestIsNotKept(t *testing.T) {
	w, x := openOn(t)
	w.verbAnswers["inbox.reply"] = []error{&golibrpc.Error{Code: inbox.CodeAlreadyAnswered, Message: "answered"}}
	x.replySend("postgres")
	w.step(t)
	w.step(t)
	if len(w.notes) != 1 || !strings.Contains(w.notes[0], "already answered") || len(x.drafts) != 0 {
		t.Fatalf("notes %v, drafts %v", w.notes, x.drafts)
	}
	w.clicks["crew:r1"]()
	x.replyDismiss()
	w.step(t)
	if len(w.verbs) != 2 || !slices.Equal(w.verbs[1], []any{"inbox.dismiss", "r1"}) {
		t.Fatalf("calls %v, want the dismiss", w.verbs)
	}
}

// TestTheReplyClosesWithItsRequest: the dialog open on a request that leaves the open set (answered
// in another window) closes and says so, and its draft goes.
func TestTheReplyClosesWithItsRequest(t *testing.T) {
	w, x := openOn(t)
	x.drafts["r1"] = "half a reply"
	w.answers = append(w.answers, changes(2, "answered"), snapshot(2, "", nil, nil))
	w.step(t) // the change
	w.step(t) // the snapshot again
	if !slices.Equal(w.closed, []string{crewReplyDialog}) || x.replying != "" || len(x.drafts) != 0 {
		t.Fatalf("closed %v, replying %q, drafts %v; want the dialog closed and the draft gone", w.closed, x.replying, x.drafts)
	}
	if len(w.notes) != 1 || !strings.Contains(w.notes[0], "was answered") {
		t.Fatalf("notes %v", w.notes)
	}
}

// TestTheBellRingsOnceForWhatOpens (docs/decisions/crew.md#the-human-inbox): with the bell on, a
// request or a permission prompt newly open rings, one ring for all that open at once and none
// within 10 s of the last; a report, a blocked agent, or what was already open rings nothing; with
// it off, nothing rings.
func TestTheBellRingsOnceForWhatOpens(t *testing.T) {
	w := newInboxWindow()
	w.prefs.agentBell = true
	permission := map[string]any{"session": "s9", "codename": "tifa", "kind": "permission", "text": "edit?"}
	blocked := map[string]any{"session": "s8", "codename": "aerith", "kind": "blocked", "text": "stuck"}
	r1, r2, r3 := request("r1", "gold", "one", "s1"), request("r2", "gold", "two", "s1"), request("r3", "gold", "three", "s1")
	w.answers = []any{
		snapshot(1, "", []any{r1, report("m1", "lector", "done")}, []any{permission}),
		changes(2, "request"), snapshot(2, "", []any{r1, r2}, []any{permission}),
		changes(3, "request"), snapshot(3, "", []any{r1, r2, r3}, []any{permission}),
		changes(4, "attention"), snapshot(4, "", []any{r1, r2, r3}, []any{permission, blocked}),
	}
	x := newCrewInbox(w, w.raise)
	at := time.Unix(1_000_000, 0)
	x.now = func() time.Time { return at }
	x.Connected()
	w.step(t)
	if w.bells != 1 {
		t.Fatalf("a request, a permission and a report opening at once rang %d times, want once", w.bells)
	}
	at = at.Add(5 * time.Second)
	w.step(t)
	w.step(t)
	if w.bells != 1 {
		t.Fatalf("a request 5 s after the last ring rang: %d rings", w.bells)
	}
	at = at.Add(6 * time.Second)
	w.step(t)
	w.step(t)
	if w.bells != 2 {
		t.Fatalf("a request 11 s after the last ring: %d rings, want 2", w.bells)
	}
	at = at.Add(time.Minute)
	w.step(t)
	w.step(t)
	if w.bells != 2 {
		t.Fatalf("a blocked agent alone, nothing else new, rang: %d rings", w.bells)
	}

	off := newInboxWindow()
	off.answers = []any{snapshot(1, "", []any{r1}, []any{permission})}
	q := newCrewInbox(off, off.raise)
	q.Connected()
	off.step(t)
	if off.bells != 0 {
		t.Fatalf("the bell off rang %d times", off.bells)
	}
}

// TestTheAgentBellToggles: :agent-bell turns the bell on, then off, and says which.
func TestTheAgentBellToggles(t *testing.T) {
	w := newInboxWindow()
	x := newCrewInbox(w, w.raise)
	cmds := x.Catalog()
	if len(cmds) != 1 || cmds[0].Verb != "agent-bell" {
		t.Fatalf("the catalog %+v, want :agent-bell", cmds)
	}
	_ = cmds[0].Run(Invocation{})
	if !w.prefs.agentBell || len(w.notes) != 1 || !strings.Contains(w.notes[0], "is on") {
		t.Fatalf("after one toggle: on %v, notes %v", w.prefs.agentBell, w.notes)
	}
	_ = cmds[0].Run(Invocation{})
	if w.prefs.agentBell || !strings.Contains(w.notes[1], "is off") {
		t.Fatalf("after two toggles: on %v, notes %v", w.prefs.agentBell, w.notes)
	}
}

// TestTheBellPlayerFallsBack: the window's sound is canberra-gtk-play's by the sound's name, else
// pw-play's or paplay's on the sound's file when it is there, else none.
func TestTheBellPlayerFallsBack(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			if slices.Contains(names, n) {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		}
	}
	yes, no := func(string) bool { return true }, func(string) bool { return false }
	for name, c := range map[string]struct {
		look   func(string) (string, error)
		exists func(string) bool
		want   string
	}{
		"canberra first":         {has("canberra-gtk-play", "pw-play", "paplay"), yes, "/usr/bin/canberra-gtk-play"},
		"pw-play next":           {has("pw-play", "paplay"), yes, "/usr/bin/pw-play"},
		"paplay last":            {has("paplay"), yes, "/usr/bin/paplay"},
		"no sound file":          {has("pw-play", "paplay"), no, ""},
		"canberra without files": {has("canberra-gtk-play"), no, "/usr/bin/canberra-gtk-play"},
		"none":                   {has(), yes, ""},
	} {
		path, args, ok := bellPlayer(c.look, c.exists)
		if path != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q %v %v, want %q", name, path, args, ok, c.want)
		}
	}
}

// TestEachOpeningSetsTheAnswerAfresh: the reply's text area is set at each
// opening, to that request's draft or empty, so what was typed for one request is never sent for
// another.
func TestEachOpeningSetsTheAnswerAfresh(t *testing.T) {
	w := newInboxWindow()
	w.answers = []any{snapshot(1, "", []any{request("a", "gold", "first", "s1"), request("b", "jarvis", "second", "s2")}, nil)}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	w.texts[crewReplyText] = "typed for a, then cancelled" // the user's typing, which the host never sees
	w.clicks["crew:b"]()
	if got, ok := w.texts[crewReplyText]; !ok || got != "" {
		t.Fatalf("b opened on %q (set %v); want its own empty answer", got, ok)
	}
}

// TestALateFailureNeverBringsADraftBack: a send that fails after its request has
// closed keeps no draft, and an earlier send failing after a later one never overwrites the later
// one's draft.
func TestALateFailureNeverBringsADraftBack(t *testing.T) {
	t.Run("the request closed first", func(t *testing.T) {
		w, x := openOn(t)
		w.verbAnswers["inbox.reply"] = []error{io.ErrUnexpectedEOF}
		w.answers = append(w.answers, changes(2, "expired"), snapshot(2, "", nil, nil))
		x.replySend("postgres") // in flight: [the next watch, the reply]
		w.step(t)               // the change
		w.stepNth(t, 1)         // the snapshot read again: r1 is gone
		w.step(t)               // the reply's late failure
		if len(x.drafts) != 0 || !strings.Contains(w.notes[len(w.notes)-1], "closed meanwhile") {
			t.Fatalf("drafts %v, notes %v; want none, and the closing said", x.drafts, w.notes)
		}
	})
	t.Run("an earlier send fails last", func(t *testing.T) {
		w, x := openOn(t)
		w.verbAnswers["inbox.reply"] = []error{io.ErrUnexpectedEOF, io.ErrUnexpectedEOF}
		x.replySend("first try")
		w.clicks["crew:r1"]()
		x.replySend("second try") // in flight: [the next watch, the first, the second]
		w.stepNth(t, 2)           // the second fails first
		w.stepNth(t, 1)           // then the first
		if x.drafts["r1"] != "second try" {
			t.Fatalf("draft %q; want the later send's", x.drafts["r1"])
		}
	})
}

// TestAWaitThatBecomesAPermissionRings: an agent shown as blocked that then waits
// on a permission prompt (the same session, its attention raised again) rings.
func TestAWaitThatBecomesAPermissionRings(t *testing.T) {
	w := newInboxWindow()
	w.prefs.agentBell = true
	blocked := map[string]any{"session": "s9", "codename": "tifa", "kind": "blocked", "text": "stuck"}
	permission := map[string]any{"session": "s9", "codename": "tifa", "kind": "permission", "text": "edit?"}
	w.answers = []any{snapshot(1, "", nil, []any{blocked}), changes(2, "attention"), snapshot(2, "", nil, []any{permission})}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	if w.bells != 0 {
		t.Fatalf("a blocked agent rang %d times", w.bells)
	}
	w.step(t)
	w.step(t)
	if w.bells != 1 {
		t.Fatalf("the same agent now on a permission prompt: %d rings, want 1", w.bells)
	}
}

func (w *inboxWindow) Tr(id string) string { return w.Trf(id) }
func (w *inboxWindow) Trf(id string, args ...any) string {
	if w.marked {
		return markedText(id, args...)
	}
	return fill(englishText(id), args...)
}
