package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// mailWindow is the inbox dialog's program: inbox.list answers from its pages, by filter and before
// (newest first, at most the limit), every call is recorded, questions are kept for the cell to
// answer.
type mailWindow struct {
	history, undeliverable []map[string]any // newest first
	calls                  [][]any          // each call: its verb, then its params
	state                  map[string]any
	opened                 []string
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
		var page []any
		for _, m := range src {
			if before == 0 || m["seq"].(int64) < before {
				page = append(page, m)
			}
			if len(page) == limit {
				break
			}
		}
		return map[string]any{"items": page}, nil
	case "inbox.ack", "inbox.dismiss":
		return map[string]any{}, nil
	}
	return nil, errors.New("unknown method: " + verb)
}
func (w *mailWindow) Go(fn func(context.Context) any, then func(any)) { then(fn(context.Background())) }
func (w *mailWindow) Set(name string, v any)                          { w.state[name] = v }
func (w *mailWindow) Open(id string)                                  { w.opened = append(w.opened, id) }
func (w *mailWindow) Ask(q Question) Asked                            { w.asked = append(w.asked, q); return Asked{} }
func (w *mailWindow) Notify(msg string)                               { w.notices = append(w.notices, msg) }
func (w *mailWindow) Tr(id string) string                             { return w.Trf(id) }
func (w *mailWindow) Trf(id string, args ...any) string               { return fill(englishText(id), args...) }

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

// A change the crew inbox reports reads the first page again, merged by seq: the pages read before
// stay, no message is listed twice, and the newer ones head the list (the mutants: the list
// replaced by page 1; merged without the seq check).
func TestAChangeReadsPageOneAgainAndKeepsThePagesRead(t *testing.T) {
	w := &mailWindow{history: mailRun(100, 100)}
	v, _ := newInboxCell(w)
	v.open()
	v.more() // 100 listed: m100 down to m001
	w.history = append(mailRun(103, 3), mailRun(100, 100)...)
	w.history[3] = mailItem(100, "message", "read") // read in another window
	v.changed()
	reads := w.verbs("inbox.list")
	if len(reads) != 3 || reads[2][1] != int64(0) {
		t.Fatalf("after the change: %v, want one more read, of the first page", reads)
	}
	got := listed(v)
	if len(got) != 103 || got[0] != "m103" || got[3] != "m100" || got[102] != "m001" {
		t.Fatalf("listed %d (%v … %v): want m103 down to m001, each once", len(got), got[:min(4, len(got))], got[max(0, len(got)-1):])
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("%s listed twice", id)
		}
		seen[id] = true
	}
	if it, _ := v.rows.At(3); it.state != "read" {
		t.Fatalf("m100 after the change: %q, want the state page 1 read", it.state)
	}
	v.closed()
	v.changed()
	if reads := w.verbs("inbox.list"); len(reads) != 3 {
		t.Fatalf("a change with the dialog closed read %d more", len(reads)-3)
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
