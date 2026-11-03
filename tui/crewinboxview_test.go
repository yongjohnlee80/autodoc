package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// mailWindow is the inbox dialog's program: inbox.list answers from its pages, by filter and before
// (newest first, at most the limit; undeliverable, as the daemon does, its newest only, before
// ignored), every call is recorded, questions are kept for the cell to answer. With hold set, a
// call is answered at once, as the daemon read it then, but its answer reaches the loop only when
// the cell releases it, in the order the cell picks: the real core.Go's goroutines finish in any
// order.
type mailWindow struct {
	hold                   bool
	held                   []func()
	history, undeliverable []map[string]any // newest first
	calls                  [][]any          // each call: its verb, then its params
	state                  map[string]any
	opened                 []string
	focused                []string
	more                   *bool // inbox.list's more, when the daemon says it
	refuse                 bool  // inbox.ack and inbox.dismiss answer an error
	asked                  []Question
	notices                []string
}

func (w *mailWindow) Call(_ context.Context, verb string, p ...any) (any, error) {
	w.calls = append(w.calls, append([]any{verb}, p...))
	switch verb {
	case "inbox.list":
		src := w.history
		if p[0] == "undeliverable" {
			src = w.undeliverable
		}
		before, limit := p[1].(int64), int(p[2].(int64))
		if p[0] == "undeliverable" {
			before = 0 // FailedNotices(limit): the newest only
		}
		var page []any
		for _, m := range src {
			if before == 0 || m["seq"].(int64) < before {
				page = append(page, m)
			}
			if len(page) == limit {
				break
			}
		}
		answer := map[string]any{"items": page}
		if w.more != nil { // since protocol 53 the daemon says whether an older page is there
			answer["more"] = *w.more
		}
		return answer, nil
	case "inbox.ack", "inbox.dismiss":
		if w.refuse {
			return nil, errors.New("the daemon refused it")
		}
		return map[string]any{}, nil
	}
	return nil, errors.New("unknown method: " + verb)
}
func (w *mailWindow) Go(fn func(context.Context) any, then func(any)) {
	res := fn(context.Background())
	if w.hold {
		w.held = append(w.held, func() { then(res) })
		return
	}
	then(res)
}

// release delivers the held answer i (in the order held) to the loop.
func (w *mailWindow) release(i int) {
	f := w.held[i]
	w.held[i] = func() {}
	f()
}
func (w *mailWindow) Set(name string, v any)            { w.state[name] = v }
func (w *mailWindow) Open(id string)                    { w.opened = append(w.opened, id) }
func (w *mailWindow) Focus(id string)                   { w.focused = append(w.focused, id) }
func (w *mailWindow) Ask(q Question) Asked              { w.asked = append(w.asked, q); return Asked{} }
func (w *mailWindow) Notify(msg string)                 { w.notices = append(w.notices, msg) }
func (w *mailWindow) Tr(id string) string               { return w.Trf(id) }
func (w *mailWindow) Trf(id string, args ...any) string { return fill(englishText(id), args...) }

// verbs is how many times the window called verb.
func (w *mailWindow) verbs(verb string) [][]any {
	var out [][]any
	for _, c := range w.calls {
		if c[0] == verb {
			out = append(out, c[1:])
		}
	}
	return out
}

// inboxReplyRec is the crew inbox as the dialog asks it: the requests it opened the reply dialog on,
// and the agents it showed.
type inboxReplyRec struct{ replied, raised []string }

func (r *inboxReplyRec) openReply(id string) { r.replied = append(r.replied, id) }
func (r *inboxReplyRec) raiseAttention(session, codename string) {
	r.raised = append(r.raised, session+"/"+codename)
}

// mailItem is a message as inbox.list answers it, at seq.
func mailItem(seq int64, kind, state string) map[string]any {
	id := fmt.Sprintf("m%03d", seq)
	return map[string]any{"message": id, "kind": kind, "from": "gold", "scope": "autodoc:w", "session": "s-gold",
		"subject": "subject " + id, "body": "body of " + id, "reply_to": "", "state": state,
		"created": time.Date(2026, 11, 2, 12, 0, int(seq), 0, time.UTC).Format(time.RFC3339), "delivery": "d-" + id, "seq": seq,
		"status": "queued", "why": ""}
}

// mailRun is n reports, seq from down to from-n+1: newest first.
func mailRun(from int64, n int) []map[string]any {
	var out []map[string]any
	for i := 0; i < n; i++ {
		out = append(out, mailItem(from-int64(i), "message", "open"))
	}
	return out
}

func newInboxCell(w *mailWindow) (*inboxView, *inboxReplyRec) {
	w.state = map[string]any{}
	r := &inboxReplyRec{}
	return newInboxView(w, r), r
}

// listed is the messages the dialog lists, by id, in its order.
func listed(v *inboxView) []string {
	var out []string
	for _, it := range v.rows.All() {
		out = append(out, it.message)
	}
	return out
}

// The inbox lists the user's mail newest first, fifty at a time: opening reads the first page
// (inbox.list "", before 0, limit 50) and opens the dialog; More reads the next, below the oldest
// seq listed, and adds it under the first.
func TestTheInboxListsNewestFirstAndPagesBySeq(t *testing.T) {
	w := &mailWindow{history: mailRun(120, 120)}
	v, _ := newInboxCell(w)
	v.open()
	if got := w.verbs("inbox.list"); len(got) != 1 || got[0][0] != "" || got[0][1] != int64(0) || got[0][2] != int64(inboxPage) {
		t.Fatalf("the first read: %v, want inbox.list(\"\", 0, %d)", got, inboxPage)
	}
	if !slices.Contains(w.opened, "inbox") {
		t.Fatalf("opened %v, want the inbox dialog", w.opened)
	}
	got := listed(v)
	if len(got) != inboxPage || got[0] != "m120" || got[inboxPage-1] != "m071" {
		t.Fatalf("listed %d: %v…, want m120 down to m071", len(got), got[:min(len(got), 3)])
	}
	v.more()
	if reads := w.verbs("inbox.list"); len(reads) != 2 || reads[1][1] != int64(71) {
		t.Fatalf("More: %v, want inbox.list below seq 71", reads)
	}
	if got := listed(v); len(got) != 2*inboxPage || got[inboxPage] != "m070" {
		t.Fatalf("after More: %d listed, the 51st %q; want 100, the 51st m070", len(got), got[min(inboxPage, len(got)-1)])
	}
}

// Opening a report marks it read (inbox.ack, once); opening a request never acks, answers or
// dismisses it: it stays open, for Reply or Dismiss (the mutant: every kind acked on opening).
func TestOpeningAReportMarksItReadButARequestStaysOpen(t *testing.T) {
	w := &mailWindow{history: []map[string]any{mailItem(3, "request", "open"), mailItem(2, "message", "open"), mailItem(1, "message", "read")}}
	v, _ := newInboxCell(w)
	v.open()
	v.openItem(1) // the open report
	if acks := w.verbs("inbox.ack"); len(acks) != 1 || acks[0][0] != "m002" {
		t.Fatalf("opening the report: acks %v, want m002 marked read", acks)
	}
	if it, _ := v.rows.At(1); it.state != "read" {
		t.Fatalf("the report after opening: %q, want read", it.state)
	}
	v.openItem(2) // a report already read
	v.openItem(0) // the request
	if acks, other := w.verbs("inbox.ack"), len(w.verbs("inbox.dismiss"))+len(w.verbs("inbox.reply")); len(acks) != 1 || other != 0 {
		t.Fatalf("opening a read report and a request: acks %v, %d answers; want no more of either", acks, other)
	}
	if it, _ := v.rows.At(0); it.state != "open" {
		t.Fatalf("the request after opening: %q, want it open", it.state)
	}
	if d, _ := w.state["App.inboxDetail"].(string); !strings.Contains(d, "body of m003") || !strings.Contains(d, "gold") {
		t.Fatalf("the detail: %q, want the request's body and sender", d)
	}
}

// A change the crew inbox reports reads the first page again, and that page is the list: the pages
// More read go. So more than a page arriving between two changes leaves no gap (the row under the new
// page is More's next), and a row read before (m001, read in another window meanwhile) is never listed
// as it was (the mutant: the first page merged over the pages read).
func TestAChangeReadsPageOneAgainAndDropsThePagesRead(t *testing.T) {
	w := &mailWindow{history: mailRun(100, 100)}
	v, _ := newInboxCell(w)
	v.open()
	v.more() // 100 listed: m100 down to m001
	if got := listed(v); len(got) != 100 {
		t.Fatalf("after More: %d listed, want 100", len(got))
	}
	w.history = mailRun(151, 151) // 51 more arrived
	w.history[150] = mailItem(1, "message", "read")
	v.changed()
	got := listed(v)
	if len(got) != inboxPage || got[0] != "m151" || got[inboxPage-1] != "m102" {
		t.Fatalf("after the change: %d listed (%v … %v), want the first page alone, m151 down to m102", len(got), got[:min(2, len(got))], got[max(0, len(got)-1):])
	}
	if s, _ := w.state["App.inboxStatus"].(string); !strings.Contains(s, "More") {
		t.Fatalf("the status %q: want More offered", s)
	}
	v.more()
	if got := listed(v); len(got) != 2*inboxPage || got[inboxPage] != "m101" {
		t.Fatalf("More after the change: %d listed, the 51st %q; want m101 next, no gap", len(got), got[min(inboxPage, len(got)-1)])
	}
	v.more() // m051 down to m002
	v.more() // m001
	v.more() // nothing: More ends
	if it, ok := v.rows.At(150); !ok || it.message != "m001" || it.state != "read" {
		t.Fatalf("the oldest after paging: %+v, want m001 as it is now, read", it)
	}
	if s, _ := w.state["App.inboxStatus"].(string); strings.Contains(s, "More") {
		t.Fatalf("the status after the last page: %q, want no More", s)
	}
	v.closed()
	reads := len(w.verbs("inbox.list"))
	v.changed()
	if len(w.verbs("inbox.list")) != reads {
		t.Fatal("a change with the dialog closed read the inbox")
	}
}

// A read's answer is dropped when a later read has started since: two re-reads finishing in reverse
// order (the daemon's goroutines finish in any order) leave the later's state, and a More overtaken
// by a change never adds its page under the change's (the mutant: every answer applied).
func TestAnOlderReadNeverUndoesANewerOne(t *testing.T) {
	w := &mailWindow{history: mailRun(100, 100)}
	v, _ := newInboxCell(w)
	v.open()
	w.hold = true
	v.changed() // A reads m100 open
	w.history[0] = mailItem(100, "message", "read")
	v.changed() // B reads it read
	w.release(1)
	w.release(0)
	if it, _ := v.rows.At(0); it.message != "m100" || it.state != "read" {
		t.Fatalf("after B then A: %+v, want m100 read, as the later read had it", it)
	}
	v.more() // a More, overtaken by a change before its page lands
	v.changed()
	w.release(3)
	w.release(2)
	if got := listed(v); len(got) != inboxPage {
		t.Fatalf("a More overtaken by a change: %d listed, want the change's first page alone", len(got))
	}
}

// The user's ack or dismiss wins over a read that started before it landed: the read's older
// answer would list the message as it was (the mutant: an ack's landing takes no generation).
func TestAnAckWinsOverAReadInFlight(t *testing.T) {
	w := &mailWindow{history: []map[string]any{mailItem(3, "request", "open"), mailItem(2, "message", "open"), mailItem(1, "message", "read")}}
	v, _ := newInboxCell(w)
	v.open()
	w.hold = true
	v.changed()   // a read in flight: m002 open
	v.openItem(1) // the user opens m002: its ack lands first
	w.release(1)
	w.release(0)
	if it, _ := v.rows.At(1); it.message != "m002" || it.state != "read" {
		t.Fatalf("an ack, then the older read: %+v, want m002 read", it)
	}
	v.changed()
	v.dismiss(0)
	w.asked[0].OnYes()
	w.release(3) // the dismiss lands
	w.release(2) // then the read from before it
	if it, _ := v.rows.At(0); it.message != "m003" || it.state != "dismissed" {
		t.Fatalf("a dismiss, then the older read: %+v, want m003 dismissed", it)
	}
}

// A change while History shows marks Undeliverable to be read again: switching to it lists the new
// notice (the mutant: the hidden source kept as it was). Undeliverable lists the newest notices with
// no More: the daemon answers its newest only, and the status says so.
func TestUndeliverableIsReadAgainAfterAChangeAndOffersNoMore(t *testing.T) {
	w := &mailWindow{history: mailRun(3, 3), undeliverable: mailRun(60, 60)}
	v, _ := newInboxCell(w)
	v.open()
	v.tab(inboxUndeliverable)
	if got := listed(v); len(got) != inboxPage || got[0] != "m060" {
		t.Fatalf("Undeliverable: %d listed, from %v; want the newest 50", len(got), got[:min(1, len(got))])
	}
	if s, _ := w.state["App.inboxStatus"].(string); !strings.Contains(s, "newest 50") {
		t.Fatalf("Undeliverable's status %q: want it said these are the newest 50", s)
	}
	reads := len(w.verbs("inbox.list"))
	v.more()
	if len(w.verbs("inbox.list")) != reads {
		t.Fatal("More on Undeliverable read again: its daemon side has no older page")
	}
	v.tab(inboxAll)
	w.undeliverable = append(mailRun(61, 1), w.undeliverable...)
	v.changed()
	v.tab(inboxUndeliverable)
	if got := listed(v); len(got) == 0 || got[0] != "m061" {
		t.Fatalf("Undeliverable after a change seen on All: %v…, want m061 first", got[:min(1, len(got))])
	}
}

// A read of the hidden tab's source, in flight from before a change, is dropped when it lands
// after it: the change marked that source to be read again, and switching to it does, in both
// directions (the mutant: the change leaves the hidden source's generation as it was, so the held
// read lists its rows from before as read).
func TestAHiddenReadHeldAcrossAChangeIsDropped(t *testing.T) {
	t.Run("Undeliverable held while All shows", func(t *testing.T) {
		w := &mailWindow{history: mailRun(3, 3), undeliverable: mailRun(5, 5)}
		v, _ := newInboxCell(w)
		v.open()
		w.hold = true
		v.tab(inboxUndeliverable) // its read, held: m005 first
		v.tab(inboxAll)
		w.undeliverable = mailRun(6, 6) // a new notice
		v.changed()                     // History read again, held; Undeliverable marked
		w.release(0)                    // the read from before the change lands late
		w.release(1)
		v.tab(inboxUndeliverable)
		w.release(2)
		if got := listed(v); len(got) == 0 || got[0] != "m006" {
			t.Fatalf("Undeliverable after the change: %v, want the new notice m006 first", got)
		}
	})
	t.Run("History held while Undeliverable shows", func(t *testing.T) {
		w := &mailWindow{history: mailRun(3, 3), undeliverable: mailRun(5, 5)}
		v, _ := newInboxCell(w)
		v.open()
		v.tab(inboxUndeliverable)
		v.tab(inboxAll)
		w.hold = true
		v.changed() // History's read, held: m003 first
		v.tab(inboxUndeliverable)
		w.history = mailRun(4, 4) // a new message
		v.changed()               // Undeliverable read again; History marked
		w.release(0)              // History's read from before the change lands late
		w.release(1)
		w.release(2)
		v.tab(inboxAll)
		w.release(3)
		if got := listed(v); len(got) == 0 || got[0] != "m004" {
			t.Fatalf("All after the change: %v, want the new message m004 first", got)
		}
	})
}

// A page shorter than fifty is not the end (a page is bounded by bytes too): More is offered while
// the last page held some, and an empty one ends it.
func TestMoreIsOfferedUntilAPageComesBackEmpty(t *testing.T) {
	w := &mailWindow{history: mailRun(30, 30)}
	v, _ := newInboxCell(w)
	v.open()
	if s, _ := w.state["App.inboxStatus"].(string); !strings.Contains(s, "More") {
		t.Fatalf("after a short first page: %q, want More offered", s)
	}
	v.more()
	if s, _ := w.state["App.inboxStatus"].(string); strings.Contains(s, "More") || len(listed(v)) != 30 {
		t.Fatalf("after an empty More: %q, %d listed; want 30, no More", s, len(listed(v)))
	}
}

// The cursor stays on its message when the list is read again with newer ones above it.
func TestTheCursorStaysOnItsMessage(t *testing.T) {
	w := &mailWindow{history: mailRun(100, 100)}
	v, _ := newInboxCell(w)
	v.open()
	v.shown(2) // m098
	w.state["App.inboxIndex"] = "untouched"
	w.history = mailRun(102, 102)
	v.changed()
	if v.cursor != 4 || v.key != "m098" {
		t.Fatalf("the cursor after two arrived: %d on %q, want 4, still m098", v.cursor, v.key)
	}
	if got := w.state["App.inboxIndex"]; got != "untouched" {
		t.Fatalf("the table's index set to %v: the table follows its row's key itself, told nothing", got)
	}
	if d, _ := w.state["App.inboxDetail"].(string); !strings.Contains(d, "body of m098") {
		t.Fatalf("the detail: %q, want m098's", d)
	}
}

// Mark read marks any report read (inbox.ack); a request can't be: it is answered or dismissed, and
// the user is told so, nothing sent.
func TestMarkReadMarksAReportAndRefusesARequest(t *testing.T) {
	w := &mailWindow{history: []map[string]any{mailItem(2, "request", "open"), mailItem(1, "response", "open")}}
	v, _ := newInboxCell(w)
	v.open()
	v.markRead(1)
	v.markRead(0)
	if acks := w.verbs("inbox.ack"); len(acks) != 1 || acks[0][0] != "m001" {
		t.Fatalf("mark read: acks %v, want the response's alone", acks)
	}
	if len(w.notices) != 1 || !strings.Contains(w.notices[0], "reply") {
		t.Fatalf("notices %q: want one saying a request is replied to or dismissed", w.notices)
	}
}

// The title counts what is open, from the crew inbox's own watch: the dialog never watches the
// inbox itself (no inbox.watch: a connection holds at most two long-polls).
func TestTheInboxCountsWhatIsOpenWithoutAWatchOfItsOwn(t *testing.T) {
	w := &mailWindow{history: mailRun(3, 3)}
	v, _ := newInboxCell(w)
	v.openSet(2, 1)
	v.open()
	if title, _ := w.state["App.inboxTitle"].(string); !strings.Contains(title, "2") || !strings.Contains(title, "1") {
		t.Fatalf("the title %q: want 2 unread and 1 open request", title)
	}
	v.openSet(0, 0)
	if title, _ := w.state["App.inboxTitle"].(string); strings.ContainsAny(title, "12") {
		t.Fatalf("the title %q, with nothing open: want no count", title)
	}
	if watches := w.verbs("inbox.watch"); len(watches) != 0 {
		t.Fatalf("the dialog watched the inbox itself: %v", watches)
	}
}

// The tabs: Unread lists the reports not read, Open requests the requests open, from what was read;
// Undeliverable reads inbox.list's own filter.
func TestTheInboxTabsReadTheirMessages(t *testing.T) {
	w := &mailWindow{history: []map[string]any{mailItem(4, "request", "open"), mailItem(3, "message", "open"),
		mailItem(2, "request", "answered"), mailItem(1, "message", "read")},
		undeliverable: []map[string]any{mailItem(9, "message", "open")}}
	v, _ := newInboxCell(w)
	v.open()
	v.tab(inboxUnread)
	if got := listed(v); !slices.Equal(got, []string{"m003"}) {
		t.Fatalf("Unread: %v, want m003", got)
	}
	v.tab(inboxRequests)
	if got := listed(v); !slices.Equal(got, []string{"m004"}) {
		t.Fatalf("Open requests: %v, want m004", got)
	}
	v.tab(inboxUndeliverable)
	if reads := w.verbs("inbox.list"); reads[len(reads)-1][0] != "undeliverable" {
		t.Fatalf("Undeliverable: %v, want inbox.list's undeliverable filter", reads)
	}
	if got := listed(v); !slices.Equal(got, []string{"m009"}) {
		t.Fatalf("Undeliverable: %v, want m009", got)
	}
	v.tab(inboxAll)
	if got := listed(v); len(got) != 4 {
		t.Fatalf("All: %v, want the four", got)
	}
}

// Reply opens the crew inbox's reply dialog on a request; Dismiss asks first, and dismisses on Yes;
// Sender shows the agent that sent the message. On a message that isn't a request, Reply and
// Dismiss say so and send nothing.
func TestReplyDismissAndSenderGoThroughTheCrewInbox(t *testing.T) {
	w := &mailWindow{history: []map[string]any{mailItem(2, "request", "open"), mailItem(1, "message", "open")}}
	v, r := newInboxCell(w)
	v.open()
	v.reply(0)
	if !slices.Equal(r.replied, []string{"m002"}) {
		t.Fatalf("Reply: %v, want the reply dialog on m002", r.replied)
	}
	v.dismiss(0)
	if len(w.asked) != 1 || len(w.verbs("inbox.dismiss")) != 0 {
		t.Fatalf("Dismiss: asked %d, dismissed %d; want it asked first, nothing sent yet", len(w.asked), len(w.verbs("inbox.dismiss")))
	}
	w.asked[0].OnYes()
	if d := w.verbs("inbox.dismiss"); len(d) != 1 || d[0][0] != "m002" {
		t.Fatalf("Dismiss on Yes: %v, want m002 dismissed", d)
	}
	v.sender(1)
	if !slices.Equal(r.raised, []string{"s-gold/gold"}) {
		t.Fatalf("Sender: %v, want gold's session shown", r.raised)
	}
	v.reply(1)
	v.dismiss(1)
	if len(r.replied) != 1 || len(w.asked) != 1 || len(w.notices) != 2 {
		t.Fatalf("Reply and Dismiss on a report: replied %v, asked %d, notices %q; want neither, the user told twice", r.replied, len(w.asked), w.notices)
	}
}

// SPC a i opens the inbox: the agents' card's i, which no other key of the card takes, in the
// keymap every build reads (the registry refuses a key taken twice; this file builds in the GUI
// build too); the inbox command is :inbox.
func TestSPCaiOpensTheInbox(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	fs := h.features()
	if _, err := newRegistry(fs...); err != nil {
		t.Fatal(err)
	}
	var on []Key
	for _, k := range keyCatalog(fs).on(agentsCard) {
		if k.Seq == "i" {
			on = append(on, k)
		}
	}
	if len(on) != 1 || on[0].Run != "App.openInbox()" {
		t.Fatalf("SPC a i: %+v, want one key, opening the inbox", on)
	}
	var verb string
	for _, f := range fs {
		if c, ok := f.(interface{ Catalog() []Entry }); ok {
			for _, e := range c.Catalog() {
				if e.ID == "crew.inbox" {
					verb = e.Verb
				}
			}
		}
	}
	if verb != "inbox" {
		t.Fatalf("the inbox's command: %q, want :inbox", verb)
	}
}

// More follows what the daemon says (protocol 53): a full page it says ends the mail offers no More
// (none read below it), and a short one it says has more under it does.
func TestMoreFollowsWhatTheDaemonSays(t *testing.T) {
	no, yes := false, true
	w := &mailWindow{history: mailRun(50, 50), more: &no}
	v, _ := newInboxCell(w)
	v.open()
	v.more()
	if reads := w.verbs("inbox.list"); len(reads) != 1 {
		t.Fatalf("More past a page the daemon says is the last: %d reads, want none more", len(reads)-1)
	}
	if s, _ := w.state["App.inboxStatus"].(string); strings.Contains(s, "More") {
		t.Fatalf("the status %q offers More the daemon says isn't there", s)
	}
	w2 := &mailWindow{history: mailRun(30, 30), more: &yes}
	v2, _ := newInboxCell(w2)
	v2.open()
	if s, _ := w2.state["App.inboxStatus"].(string); !strings.Contains(s, "More") {
		t.Fatalf("a short page the daemon says has more under it: status %q, want More offered", s)
	}
}

// A Mark read and a Dismiss show on the row at once, before the daemon answers; refused, the row is
// as it was again, and the user is told (the mutants: the row waiting on the answer; a refusal left
// showing as done).
func TestMarkReadAndDismissShowAtOnceAndAreUndoneWhenRefused(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		w := &mailWindow{history: []map[string]any{mailItem(2, "request", "open"), mailItem(1, "message", "open")}}
		v, _ := newInboxCell(w)
		v.open()
		w.hold, w.refuse = true, refuse
		v.markRead(1)
		v.dismiss(0)
		w.asked[0].OnYes()
		state := func(i int) string { it, _ := v.rows.At(i); return it.state }
		if state(1) != "read" || state(0) != "dismissed" {
			t.Fatalf("before the daemon answered: %q and %q, want read and dismissed at once", state(1), state(0))
		}
		w.release(0)
		w.release(1)
		want1, want0 := "read", "dismissed"
		if refuse {
			want1, want0 = "open", "open"
		}
		if state(1) != want1 || state(0) != want0 {
			t.Fatalf("refused %v: %q and %q after the answers, want %q and %q", refuse, state(1), state(0), want1, want0)
		}
		if refuse && len(w.notices) != 2 {
			t.Fatalf("refused: notices %q, want each refusal said", w.notices)
		}
	}
}

// The Inbox's tabs' and buttons' letters are one each: no two take the same key, among the tabs, the
// buttons and Close's q (the language check holds every translation to English's letter, so English
// is every language's).
func TestTheInboxLettersAreOneEach(t *testing.T) {
	seen := map[rune]string{}
	for _, id := range []string{"all", "unread", "requests", "undeliverable", "open", "mark_read", "reply", "dismiss", "sender", "more", "close_q"} {
		_, key, _ := tuicore.ParseMnemonic(englishText("autodoc.dialog.inbox." + id))
		if key == 0 {
			t.Fatalf("%s marks no letter", id)
		}
		key = unicode.ToLower(key)
		if other, dup := seen[key]; dup {
			t.Fatalf("%s and %s both take %q", other, id, key)
		}
		seen[key] = id
	}
}

// Each tab and button says what it does when it has focus: its tooltip, set at the opening in the
// UI's language (a tooltip takes a string, not a catalog message).
func TestEachInboxButtonSaysWhatItDoes(t *testing.T) {
	w := &mailWindow{history: mailRun(2, 2)}
	v, _ := newInboxCell(w)
	v.open()
	for _, tip := range inboxTips {
		if got, _ := w.state[tip.state].(string); got == "" || got != englishText(tip.id) {
			t.Errorf("%s: %q, want %s's words", tip.state, got, tip.id)
		}
	}
}
