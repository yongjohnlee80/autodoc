package mailtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// The fallback's cells (docs/decisions/crew.md#the-fallback-exactly-once). fbRig is a window's
// scope autodoc:w held by host-run, with gold, a claude session, and its credential.
type fbRig struct {
	t          *testing.T
	ctx        context.Context
	clock      *crewtest.Clock
	st         Stores
	mm         *mail.Manager
	cm         *crew.Manager
	bell       *fireWaker
	run        string
	epoch      int64
	session    string
	credential string
	keys       int
}

func newFallbackRig(t *testing.T, h Harness) *fbRig {
	r := &fbRig{t: t, ctx: context.Background(), clock: crewtest.NewClock(), run: "host-run", bell: &fireWaker{}}
	r.st = h.New(t, r.clock.Now)
	port := &lazyCrew{}
	r.mm = mail.New(r.st.Mail, port)
	r.cm = crew.New(r.st.Crew, crew.WithSessionEnder(r.mm))
	port.m = r.cm
	if err := r.mm.EnsureReserved(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.cm.NewScheduler("daemon-run", map[string]crew.Waker{"claude": r.bell})
	var err error
	if r.epoch, err = r.cm.Claim(r.ctx, "autodoc:w", r.run, ""); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 40)
	reg, err := r.cm.Register(r.ctx, crew.Registration{Scope: "autodoc:w", Run: r.run, Epoch: r.epoch, Codename: "gold",
		Kind: "claude", Workspace: "w", Instance: "i1", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	r.session, r.credential = reg.Session, reg.Session+"."+secret
	return r
}

// reachable gives gold's session an endpoint: woken, not unreachable.
func (r *fbRig) reachable() {
	r.t.Helper()
	if err := r.st.Crew.Write(r.ctx, func(tx crew.Tx) error {
		return tx.PutEndpoint(crew.Endpoint{Session: r.session, Generation: 1, Kind: "claude"})
	}); err != nil {
		r.t.Fatal(err)
	}
}

// send queues one message from the user to gold, never fired.
func (r *fbRig) send() mail.Delivery {
	r.t.Helper()
	r.keys++
	sent, err := r.mm.SendAsUser(r.ctx, "autodoc:w", r.run, r.epoch, mail.Send{To: []string{"gold"}, Body: "b",
		Key: "k" + string(rune('a'+r.keys))})
	if err != nil || len(sent.Deliveries) != 1 {
		r.t.Fatalf("the send: %+v %v", sent, err)
	}
	return sent.Deliveries[0]
}

// poll is the host's mail.fallbacks, answering at once.
func (r *fbRig) poll() []mail.Offer {
	r.t.Helper()
	out, err := r.mm.Fallbacks(r.ctx, "autodoc:w", r.run, r.epoch, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

// sweep is the scheduler's expire, with no host polling.
func (r *fbRig) sweep() {
	r.t.Helper()
	if err := r.st.Crew.Write(r.ctx, func(tx crew.Tx) error { return r.mm.Expire(tx) }); err != nil {
		r.t.Fatal(err)
	}
}

func (r *fbRig) delivery(id string) mail.Delivery {
	r.t.Helper()
	for _, d := range r.st.Inspect.Deliveries() {
		if d.ID == id {
			return d
		}
	}
	r.t.Fatalf("no delivery %s", id)
	return mail.Delivery{}
}

func (r *fbRig) told(kind string) int {
	n := 0
	for _, c := range r.st.Inspect.Changes() {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

// A delivery falls back at 30 s after its send when its recipient's session is unreachable (no
// endpoint), not a nanosecond before; one whose session is reachable, at 2 min (the first ring
// unanswered). Falling back withdraws it (WhyFallback), opens one job, and tells the user once.
func testAFallbackIsDueAtItsBoundaries(t *testing.T, h Harness) {
	r := newFallbackRig(t, h)
	r.poll() // the host takes fallbacks
	d := r.send()
	r.clock.Advance(30*time.Second - time.Nanosecond)
	if got := r.poll(); len(got) != 0 || r.delivery(d.ID).State != mail.Queued {
		t.Fatalf("unreachable, at 30 s less 1 ns: %+v, the delivery %+v; want nothing yet", got, r.delivery(d.ID))
	}
	r.clock.Advance(time.Nanosecond)
	got := r.poll()
	if len(got) != 1 || got[0].Job.Delivery != d.ID || got[0].Job.Tries != 0 || got[0].Codename != "gold" || got[0].Message.Body != "b" || got[0].From != "user" {
		t.Fatalf("unreachable, at 30 s: %+v, want gold's one job", got)
	}
	if w := r.delivery(d.ID); w.State != mail.Withdrawn || w.Why != mail.WhyFallback {
		t.Fatalf("the delivery fallen back: %+v, want withdrawn (fallback)", w)
	}
	if r.told(mail.ChangeFallback) != 1 {
		t.Fatalf("the user was told %d times, want once", r.told(mail.ChangeFallback))
	}
	if err := r.mm.FallbackDone(r.ctx, "autodoc:w", r.run, r.epoch, got[0].Job.ID, got[0].Job.Lease, mail.FallbackDelivered); err != nil {
		t.Fatal(err)
	}

	r.reachable()
	d2 := r.send()
	r.clock.Advance(crew.WakeUnanswered - time.Nanosecond)
	if got := r.poll(); len(got) != 0 || r.delivery(d2.ID).State != mail.Queued {
		t.Fatalf("reachable, at 2 min less 1 ns: %+v; want nothing yet", got)
	}
	r.clock.Advance(time.Nanosecond)
	if got := r.poll(); len(got) != 1 || got[0].Job.Delivery != d2.ID {
		t.Fatalf("reachable, at 2 min: %+v, want its job", got)
	}
}

// A host that never polled for fallbacks keeps its agents' mail queued, woken as before, and
// received in the end; one that polled has it fall back; one restarted into a new epoch that hasn't
// polled yet keeps it queued again.
func testAHostTakesFallbacksOnlyOnceItPollsInItsEpoch(t *testing.T, h Harness) {
	r := newFallbackRig(t, h)
	d := r.send()
	r.clock.Advance(3 * time.Minute)
	r.sweep()
	if len(r.st.Inspect.Jobs()) != 0 || r.delivery(d.ID).State != mail.Queued {
		t.Fatalf("a host that never polled: jobs %+v, the delivery %+v; want it queued", r.st.Inspect.Jobs(), r.delivery(d.ID))
	}
	if got, err := r.mm.Receive(r.ctx, r.credential); err != nil || len(got.Items) != 1 || got.Items[0].Delivery.ID != d.ID {
		t.Fatalf("gold's receive: %+v %v, want the queued delivery", got, err)
	}

	r.poll()
	d2 := r.send()
	r.clock.Advance(3 * time.Minute)
	r.sweep()
	if jobs := r.st.Inspect.Jobs(); len(jobs) != 1 || jobs[0].Delivery != d2.ID {
		t.Fatalf("a host that polled: %+v, want the delivery's job", jobs)
	}

	// the takeover ends the old epoch's sessions: gold registers again under the new one, so its
	// mail has a live recipient, and only the host's part in fallbacks decides
	epoch, err := r.cm.Takeover(r.ctx, "autodoc:w", "host-run-2", "")
	if err != nil || epoch == r.epoch {
		t.Fatalf("the takeover: %d %v", epoch, err)
	}
	if _, err := r.cm.Register(r.ctx, crew.Registration{Scope: "autodoc:w", Run: "host-run-2", Epoch: epoch, Codename: "gold",
		Kind: "claude", Workspace: "w", Instance: "i2", Secret: strings.Repeat("t", 40)}); err != nil {
		t.Fatal(err)
	}
	d3 := r.send2(epoch)
	r.clock.Advance(3 * time.Minute)
	r.sweep()
	if jobs := r.st.Inspect.Jobs(); len(jobs) != 1 || r.delivery(d3.ID).State != mail.Queued {
		t.Fatalf("a new epoch not polled yet: jobs %+v, the delivery %+v; want it queued", jobs, r.delivery(d3.ID))
	}
}

// send2 is send from the host that took the scope over, at epoch.
func (r *fbRig) send2(epoch int64) mail.Delivery {
	r.t.Helper()
	sent, err := r.mm.SendAsUser(r.ctx, "autodoc:w", "host-run-2", epoch, mail.Send{To: []string{"gold"}, Body: "b", Key: "k-takeover"})
	if err != nil || len(sent.Deliveries) != 1 {
		r.t.Fatalf("the send: %+v %v", sent, err)
	}
	return sent.Deliveries[0]
}

// A receive and a fallback at the same instant leave exactly one outcome: the agent has the
// delivery, or its host has the job.
func testAReceiveRacingAFallbackLeavesOneOutcome(t *testing.T, h Harness) {
	for range 8 {
		r := newFallbackRig(t, h)
		r.poll()
		d := r.send()
		r.clock.Advance(30 * time.Second)
		var wg sync.WaitGroup
		var got mail.Received
		var offers []mail.Offer
		var rerr, ferr error
		wg.Add(2)
		go func() { defer wg.Done(); got, rerr = r.mm.Receive(r.ctx, r.credential) }()
		go func() { defer wg.Done(); offers, ferr = r.mm.Fallbacks(r.ctx, "autodoc:w", r.run, r.epoch, 0) }()
		wg.Wait()
		if rerr != nil || ferr != nil {
			t.Fatal(rerr, ferr)
		}
		if (len(got.Items) == 1) == (len(offers) == 1) || len(got.Items)+len(offers) != 1 {
			t.Fatalf("the race: received %+v, offered %+v; want exactly one of them (delivery %s)", got.Items, offers, d.ID)
		}
	}
}

// A delivery the agent ever leased never falls back, even after its lease lapsed (it may have read
// it); an ended recipient's mail waits for its next session, never falling back.
func testALeasedOrEndedRecipientsMailNeverFallsBack(t *testing.T, h Harness) {
	r := newFallbackRig(t, h)
	r.poll()
	d := r.send()
	if got, err := r.mm.Receive(r.ctx, r.credential); err != nil || len(got.Items) != 1 {
		t.Fatalf("gold's receive: %+v %v", got, err)
	}
	r.clock.Advance(mail.LeaseFor + 3*time.Minute)
	if got := r.poll(); len(got) != 0 || r.delivery(d.ID).State != mail.Queued || r.delivery(d.ID).Attempts != 1 {
		t.Fatalf("leased, then lapsed: %+v, the delivery %+v; want it queued again, never a job", got, r.delivery(d.ID))
	}

	r2 := newFallbackRig(t, h)
	r2.poll()
	d2 := r2.send()
	if err := r2.cm.Exit(r2.ctx, "autodoc:w", r2.run, r2.epoch, r2.session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	r2.clock.Advance(3 * time.Minute)
	if got := r2.poll(); len(got) != 0 || r2.delivery(d2.ID).State != mail.Queued {
		t.Fatalf("an ended recipient: %+v, the delivery %+v; want it waiting", got, r2.delivery(d2.ID))
	}
}

// A job is leased to its host once; while the host is away it waits open, the user told once.
// A lapsed lease is offered again with Tries one higher and a new lease; a report under the old
// lease is lost, under the new one taken once, a replay answering ok. An unknown outcome tells the
// user.
func testAJobIsLeasedReofferedAndReportedOnce(t *testing.T, h Harness) {
	r := newFallbackRig(t, h)
	r.poll()
	d := r.send()
	r.clock.Advance(30 * time.Second)
	r.sweep() // made with nobody polling: the host is away
	r.clock.Advance(10 * time.Minute)
	r.sweep()
	if jobs := r.st.Inspect.Jobs(); len(jobs) != 1 || jobs[0].State != mail.JobOpen || r.told(mail.ChangeFallback) != 1 {
		t.Fatalf("an away host's job: %+v, told %d; want it open, the user told once", jobs, r.told(mail.ChangeFallback))
	}
	first := r.poll()
	if len(first) != 1 || first[0].Job.Delivery != d.ID || first[0].Job.Tries != 0 || first[0].Job.Lease == "" {
		t.Fatalf("the first offer: %+v", first)
	}
	if again := r.poll(); len(again) != 0 {
		t.Fatalf("a leased job offered again at once: %+v", again)
	}
	r.clock.Advance(60 * time.Second)
	second := r.poll()
	if len(second) != 1 || second[0].Job.ID != first[0].Job.ID || second[0].Job.Tries != 1 || second[0].Job.Lease == first[0].Job.Lease {
		t.Fatalf("the lapsed job's offer: %+v, want it again with tries 1 and a new lease", second)
	}
	done := func(j mail.FallbackJob, o mail.FallbackOutcome) error {
		return r.mm.FallbackDone(r.ctx, "autodoc:w", r.run, r.epoch, j.ID, j.Lease, o)
	}
	if err := done(first[0].Job, mail.FallbackDelivered); !errors.Is(err, mail.ErrLeaseLost) {
		t.Fatalf("a report under the lapsed lease: %v, want lease lost", err)
	}
	for range 2 {
		if err := done(second[0].Job, mail.FallbackDelivered); err != nil {
			t.Fatalf("the report, and its replay: %v", err)
		}
	}
	if jobs := r.st.Inspect.Jobs(); jobs[0].State != mail.JobDone || jobs[0].Outcome != mail.FallbackDelivered || jobs[0].Tries != 2 {
		t.Fatalf("the reported job: %+v", jobs[0])
	}
	r.clock.Advance(5 * time.Minute)
	if got := r.poll(); len(got) != 0 {
		t.Fatalf("a done job offered again: %+v", got)
	}
	r.send()
	r.clock.Advance(30 * time.Second)
	third := r.poll()
	if len(third) != 1 || done(third[0].Job, mail.FallbackUnknown) != nil || r.told(mail.ChangeFallbackUnknown) != 1 {
		t.Fatalf("an unknown outcome: %+v, told %d; want the user told", third, r.told(mail.ChangeFallbackUnknown))
	}
	if err := done(third[0].Job, "maybe"); !errors.Is(err, mail.ErrInvalid) {
		t.Fatalf("an outcome no host reports: %v, want invalid", err)
	}
}

// The user's send to fire that didn't ring becomes its recipient's host's job when that host takes
// them, answering handed over (an unreachable), its replay too, so the window never types it. One the agent
// received while its doorbell was failing stands: received, never a job.
func testTheUsersUnrungSendIsAJobOfAHostThatTakesThem(t *testing.T, h Harness) {
	r := newFallbackRig(t, h)
	r.reachable()
	r.poll()
	r.bell.answer(errors.New("the socket refused"))
	sent, err := r.mm.SendAsUser(r.ctx, "autodoc:w", r.run, r.epoch, mail.Send{To: []string{"gold"}, Body: "b", Key: "kf", Fire: true})
	if !errors.Is(err, mail.ErrHandedOver) || !errors.Is(err, mail.ErrUnreachable) || len(sent.Deliveries) != 1 {
		t.Fatalf("an unrung send: %+v %v, want handed over (an unreachable)", sent, err)
	}
	if _, err := r.mm.SendAsUser(r.ctx, "autodoc:w", r.run, r.epoch, mail.Send{To: []string{"gold"}, Body: "b", Key: "kf", Fire: true}); !errors.Is(err, mail.ErrHandedOver) {
		t.Fatalf("its replay: %v, want handed over again, never plain unreachable", err)
	}
	d := r.delivery(sent.Deliveries[0].ID)
	jobs := r.st.Inspect.Jobs()
	if d.State != mail.Withdrawn || d.Why != mail.WhyFallback || len(jobs) != 1 || jobs[0].Delivery != d.ID {
		t.Fatalf("the unrung send: %+v, jobs %+v; want its host's one job", d, jobs)
	}

	// a fresh rig: the failed ring above leaves gold in its backoff, where no ring calls the waker
	r2 := newFallbackRig(t, h)
	r2.reachable()
	r2.poll()
	r2.bell.mu.Lock()
	r2.bell.err = errors.New("the socket refused")
	r2.bell.during = func() { _, _ = r2.mm.Receive(r2.ctx, r2.credential) }
	r2.bell.mu.Unlock()
	sent, err = r2.mm.SendAsUser(r2.ctx, "autodoc:w", r2.run, r2.epoch, mail.Send{To: []string{"gold"}, Body: "b2", Key: "kg", Fire: true})
	if err != nil || len(r2.st.Inspect.Jobs()) != 0 || r2.delivery(sent.Deliveries[0].ID).State != mail.Leased || r2.bell.answer(errors.New("x")) != 1 {
		t.Fatalf("a send received while its doorbell failed: %v, jobs %+v, the delivery %+v; want it standing, received, no job",
			err, r2.st.Inspect.Jobs(), r2.delivery(sent.Deliveries[0].ID))
	}
}
