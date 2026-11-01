package tui

import (
	"os"
	"slices"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// fallbackRig is messagingRig with the window taking fallback jobs: mail.fallbacks answers jobs
// from offer once, then nothing; every mail.fallback_done is kept in reported.
type fallbackRig struct {
	a        *agents
	c        *identityHost
	w        *crewWindow
	offer    []map[string]any
	reported [][]any
	polls    int
}

func newFallbackRig(t *testing.T, offer ...map[string]any) *fallbackRig {
	t.Helper()
	r := &fallbackRig{offer: offer}
	r.a, r.c, r.w = messagingRig(t)
	r.w.fallbacks = func([]any) (any, error) {
		r.polls++
		jobs := []any{}
		for _, j := range r.offer {
			jobs = append(jobs, j)
		}
		r.offer = nil
		return map[string]any{"jobs": jobs}, nil
	}
	r.w.fallbackDone = func(p []any) (any, error) { r.reported = append(r.reported, p); return map[string]any{}, nil }
	r.a.crew.deliverJob = r.a.deliverFallback
	return r
}

// start polls, as a claim does, and settles what it left.
func (r *fallbackRig) start() {
	r.a.crew.pollFallbacks(r.a.crew.gen)
	settleMessaging(r.c, r.w)
}

func job(id string, tries int64) map[string]any {
	return map[string]any{"id": id, "lease": "lease-" + id, "codename": "one", "from": "two", "tries": tries,
		"text": "Mail from two (by your host: the crew couldn't deliver it). Answer with autodoc mail send two.\nhello one"}
}

// outcome is what mail.fallback_done said of job id, "" when it wasn't told.
func (r *fallbackRig) outcome(id string) string {
	for _, p := range r.reported {
		if p[3] == id && p[4] == "lease-"+id {
			return p[5].(string)
		}
	}
	return ""
}

// A job is recorded, then typed once into its agent's terminal through the base (pasted, Enter
// where the agent's prompts are reported), and reported delivered under its lease; the record goes
// once the daemon took the report; the user is told it fell back (the mutants: typed before the
// record is written; silent on delivery).
func TestAFallbackJobIsTypedOnceAndReported(t *testing.T) {
	r := newFallbackRig(t, job("j1", 0))
	var atTyping string // the record as the job is handed to the base
	r.a.crew.deliverJob = func(codename, text string, done func(error)) {
		atTyping, _ = r.a.crew.fallbackRecord("j1")
		r.a.deliverFallback(codename, text, done)
	}
	r.start()
	if atTyping != fallbackTyping {
		t.Fatalf("the record when typing began: %q, want %q: written before anything is typed", atTyping, fallbackTyping)
	}
	if len(r.c.panel.pastes) != 1 || !strings.Contains(r.c.panel.pastes[0], "hello one") || !slices.Equal(r.c.panel.keys, []string{"\r"}) {
		t.Fatalf("pasted %q, pressed %q: want the job's text once, then Enter", r.c.panel.pastes, r.c.panel.keys)
	}
	if got := r.outcome("j1"); got != string(mail.FallbackDelivered) {
		t.Fatalf("reported %v, want j1 delivered", r.reported)
	}
	if _, ok := r.a.crew.fallbackRecord("j1"); ok {
		t.Fatal("the record outlived the report the daemon took")
	}
	if r.polls < 2 {
		t.Fatalf("%d polls: want the window asking again after the job", r.polls)
	}
	if !slices.ContainsFunc(r.w.notices, func(n string) bool { return strings.Contains(n, "one") && strings.Contains(n, "two's message") }) {
		t.Fatalf("notices %q: want the user told the fallback typed two's message into one's prompt", r.w.notices)
	}
}

// A window restarted between recording a job and typing it (the record says "typing") is offered
// the job again: it reports it delivered from the record, and types nothing (the mutant: the record
// unread, the job typed or reported unknown).
func TestARecordedJobOfferedAgainIsNeverTypedTwice(t *testing.T) {
	r := newFallbackRig(t, job("j2", 1))
	if err := r.a.crew.writeFallbackRecord("j2", fallbackTyping); err != nil {
		t.Fatal(err)
	}
	r.start()
	if len(r.c.panel.pastes) != 0 || len(r.c.panel.keys) != 0 {
		t.Fatalf("pasted %q, pressed %q: want nothing typed again", r.c.panel.pastes, r.c.panel.keys)
	}
	if got := r.outcome("j2"); got != string(mail.FallbackDelivered) {
		t.Fatalf("reported %v, want j2 delivered, from the record", r.reported)
	}
}

// A job offered again that the window has no record of went to another run, which may have typed
// it: it is reported unknown, typed nothing, and the user is given the text.
func TestAJobOfferedAgainWithNoRecordIsGivenToTheUser(t *testing.T) {
	r := newFallbackRig(t, job("j3", 1))
	r.start()
	if len(r.c.panel.pastes) != 0 {
		t.Fatalf("pasted %q: want nothing typed", r.c.panel.pastes)
	}
	if got := r.outcome("j3"); got != string(mail.FallbackUnknown) {
		t.Fatalf("reported %v, want j3 unknown", r.reported)
	}
	if !slices.ContainsFunc(r.w.notices, func(n string) bool { return strings.Contains(n, "hello one") }) {
		t.Fatalf("notices %q, want the user given the text", r.w.notices)
	}
}

// A report the daemon didn't take keeps the record: the job comes back when its lease lapses, and
// is answered from it.
func TestARecordStaysUntilTheDaemonTakesTheReport(t *testing.T) {
	r := newFallbackRig(t, job("j4", 0))
	r.w.fallbackDone = func(p []any) (any, error) {
		r.reported = append(r.reported, p)
		return nil, &golibrpc.Error{Code: mail.CodeLeaseLost, Message: "the lease is lost"}
	}
	r.start()
	if rec, ok := r.a.crew.fallbackRecord("j4"); !ok || rec != string(mail.FallbackDelivered) {
		t.Fatalf("the record after an untaken report: %q %v, want delivered", rec, ok)
	}
}

// A send the daemon handed over (inbox.handed_over: the window's fallback job carries it) is asked
// once, types nothing, and done hears nil: the job types it (the mutant: handed_over typed by the
// send, or asked again).
func TestAHandedOverSendTypesNothing(t *testing.T) {
	a, c, w := messagingRig(t)
	asks := 0
	w.inboxSend = func([]any) (any, error) {
		asks++
		return nil, &golibrpc.Error{Code: inbox.CodeHandedOver, Message: "its host carries it as a fallback job"}
	}
	var heard []error
	if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if asks != 1 || len(c.panel.pastes) != 0 || len(c.panel.keys) != 0 || len(heard) != 1 || heard[0] != nil {
		t.Fatalf("asks %d, pasted %q, pressed %q, heard %v: want one ask, nothing typed, done nil", asks, c.panel.pastes, c.panel.keys, heard)
	}
}

// A daemon before protocol 42 has no jobs: the window asks once and never again.
func TestAnOlderDaemonIsAskedForJobsOnce(t *testing.T) {
	a, c, w := messagingRig(t)
	a.crew.deliverJob = a.deliverFallback
	a.crew.pollFallbacks(a.crew.gen)
	settleMessaging(c, w)
	if n := strings.Count(strings.Join(w.calls, " "), "mail.fallbacks"); n != 1 {
		t.Fatalf("%d asks for jobs of an older daemon, want 1", n)
	}
	if _, err := os.Stat(a.crew.fallbackPath("x")); err == nil {
		t.Fatal("a record was written")
	}
}
