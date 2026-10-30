package mail_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/mail/mailtest"
)

// guard is the crew port as the mail module sees it: a live session passes; every raised wake is
// counted by profile.
type guard struct {
	mu     sync.Mutex
	raised map[string]int
}

func (g *guard) GuardSession(tx crew.Tx, id string) (crew.Session, error) {
	s, ok, err := tx.Session(id)
	if err != nil {
		return crew.Session{}, err
	}
	if !ok || s.Ended != nil {
		return crew.Session{}, crew.ErrBadCredential
	}
	return s, nil
}

func (g *guard) RaisePending(_ crew.Tx, profile string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.raised[profile]++
	return nil
}

func (g *guard) woken(profile string) int { g.mu.Lock(); defer g.mu.Unlock(); return g.raised[profile] }

type rig struct {
	t     *testing.T
	ctx   context.Context
	clock *crewtest.Clock
	mem   *mailtest.Mem
	g     *guard
	m     *mail.Manager
	n     int
}

// newRig has the scope autovim:p with profiles jarvis (session s-jarvis) and gold (s-gold), and the
// human (global/user).
func newRig(t *testing.T) *rig {
	r := &rig{t: t, ctx: context.Background(), clock: crewtest.NewClock(), g: &guard{raised: map[string]int{}}}
	r.mem = mailtest.NewMem(r.clock.Now)
	r.m = mail.New(r.mem, r.g, mail.WithIDs(func() string { r.n++; return fmt.Sprintf("id%03d", r.n) }))
	r.profile("p-jarvis", "autovim:p", "jarvis", "s-jarvis")
	r.profile("p-gold", "autovim:p", "gold", "s-gold")
	r.profile("p-user", mail.GlobalScope, mail.UserCodename, "")
	r.profile(mail.DaemonProfile, mail.GlobalScope, "autodoc", "")
	return r
}

func (r *rig) profile(id, scope, codename, session string) {
	r.t.Helper()
	r.write(func(tx mail.Tx) error {
		if err := tx.PutProfile(crew.Profile{ID: id, Scope: scope, Codename: codename, Status: crew.ProfileActive}); err != nil {
			return err
		}
		if session == "" {
			return nil
		}
		return tx.PutSession(crew.Session{ID: session, Profile: id, Host: scope})
	})
}

func (r *rig) write(fn func(mail.Tx) error) {
	r.t.Helper()
	if err := r.mem.Write(r.ctx, fn); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) send(from string, p mail.Send) mail.Sent {
	r.t.Helper()
	out, err := r.m.Send(r.ctx, from, p)
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *rig) receive(session string) mail.Received {
	r.t.Helper()
	out, err := r.m.Receive(r.ctx, session)
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *rig) delivery(id string) mail.Delivery {
	r.t.Helper()
	for _, d := range r.mem.Deliveries() {
		if d.ID == id {
			return d
		}
	}
	r.t.Fatalf("no delivery %s", id)
	return mail.Delivery{}
}

// events is every event the daemon sent to profile, as "why:replyTo".
func (r *rig) events(profile string) []string {
	var out []string
	byID := map[string]mail.Message{}
	for _, msg := range r.mem.Messages() {
		byID[msg.ID] = msg
	}
	for _, d := range r.mem.Deliveries() {
		if msg := byID[d.Message]; msg.Kind == mail.KindEvent && d.Recipient == profile {
			out = append(out, msg.Body+":"+msg.ReplyTo)
		}
	}
	return out
}

// A send stores one message and a delivery per recipient in its seq; a receive leases it, wakes
// nothing more, and an ack under the lease ends it. Replaying the ack answers ok; another lease is
// lost; a second receive has nothing.
func TestASendIsReceivedLeasedAndAcked(t *testing.T) {
	r := newRig(t)
	sent := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Subject: "s", Body: "b", Key: "k1"})
	if len(sent.Deliveries) != 1 || sent.Deliveries[0].Recipient != "p-gold" || sent.Deliveries[0].Seq != 1 || sent.Message.Sender != "p-jarvis" {
		t.Fatalf("the send: %+v", sent)
	}
	if r.g.woken("p-gold") != 1 {
		t.Fatalf("gold woken %d times, want 1", r.g.woken("p-gold"))
	}
	got := r.receive("s-gold")
	if len(got.Items) != 1 || got.More || got.Items[0].Message.Body != "b" || got.Items[0].Delivery.State != mail.Leased {
		t.Fatalf("the receive: %+v", got)
	}
	d := got.Items[0].Delivery
	if err := r.m.Ack(r.ctx, "s-gold", d.ID, "another"); !errors.Is(err, mail.ErrLeaseLost) {
		t.Fatalf("an ack under another lease: %v", err)
	}
	if err := r.m.Ack(r.ctx, "s-jarvis", d.ID, d.Lease); !errors.Is(err, mail.ErrLeaseLost) {
		t.Fatalf("an ack by a session that is not the recipient's: %v", err)
	}
	for range 2 {
		if err := r.m.Ack(r.ctx, "s-gold", d.ID, d.Lease); err != nil {
			t.Fatalf("an ack, then its replay: %v", err)
		}
	}
	if st := r.delivery(d.ID).State; st != mail.Acked {
		t.Fatalf("the delivery is %s", st)
	}
	if again := r.receive("s-gold"); len(again.Items) != 0 {
		t.Fatalf("a second receive: %+v", again)
	}
}

// One sender's key makes one message: the same content answers the first send, other content is
// ErrKeyReused, and neither adds a delivery.
func TestASendsKeyMakesOneMessage(t *testing.T) {
	r := newRig(t)
	p := mail.Send{To: []string{"gold"}, Body: "b", Key: "k"}
	first := r.send("s-jarvis", p)
	if again := r.send("s-jarvis", p); again.Message.ID != first.Message.ID || len(again.Deliveries) != 1 {
		t.Fatalf("the replay: %+v, want %+v", again, first)
	}
	p.Body = "other"
	if _, err := r.m.Send(r.ctx, "s-jarvis", p); !errors.Is(err, mail.ErrKeyReused) {
		t.Fatalf("the key with other content: %v", err)
	}
	if n := len(r.mem.Deliveries()); n != 1 {
		t.Fatalf("%d deliveries, want 1", n)
	}
}

// Addresses: a name in the sender's scope, user, a live session (bound to it), name@scope. One that
// resolves to nothing, or to the daemon, is ErrNoRecipient, and the send stores nothing.
func TestASendsAddresses(t *testing.T) {
	r := newRig(t)
	r.profile("p-zen", "autovim:other", "zen", "s-zen")
	sent := r.send("s-jarvis", mail.Send{To: []string{"user", "session:s-gold", "zen@other", "gold"}, Body: "b", Key: "k"})
	var got []string
	for _, d := range sent.Deliveries {
		got = append(got, d.Recipient+"/"+d.Session)
	}
	if want := "p-user/ p-gold/s-gold p-zen/ p-gold/"; strings.Join(got, " ") != want {
		t.Fatalf("the deliveries: %v, want %s", got, want)
	}
	if ch := r.mem.Changes(); len(ch) != 1 || ch[0] != (mailtest.Change{Kind: mail.ChangeReport, Ref: sent.Message.ID}) {
		t.Fatalf("the human's feed: %+v", ch)
	}
	for _, to := range []string{"nobody", "autodoc@global", "session:s-gone", "gold@nowhere"} {
		before := len(r.mem.Messages())
		if _, err := r.m.Send(r.ctx, "s-jarvis", mail.Send{To: []string{"gold", to}, Body: "b", Key: "k-" + to}); !errors.Is(err, mail.ErrNoRecipient) {
			t.Errorf("to %s: %v", to, err)
		}
		if len(r.mem.Messages()) != before {
			t.Errorf("to %s stored a message", to)
		}
	}
	if _, err := r.m.Send(r.ctx, "s-nobody", mail.Send{To: []string{"gold"}, Body: "b", Key: "k"}); !errors.Is(err, crew.ErrBadCredential) {
		t.Fatalf("a send from no session: %v", err)
	}
	if _, err := r.m.Send(r.ctx, "s-jarvis", mail.Send{To: []string{"gold"}, Body: strings.Repeat("x", mail.MaxBody+1), Key: "big"}); !errors.Is(err, mail.ErrTooLarge) {
		t.Fatalf("a body past the limit: %v", err)
	}
}

// A lapsed lease is queued again, an attempt counted and the recipient woken; after MaxAttempts it
// fails, and its sender gets one event.
func TestALapsedLeaseIsRedeliveredThenFails(t *testing.T) {
	r := newRig(t)
	sent := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k"})
	id := sent.Deliveries[0].ID
	for i := 1; i < mail.MaxAttempts; i++ {
		if got := r.receive("s-gold"); len(got.Items) != 1 {
			t.Fatalf("receive %d: %+v", i, got)
		}
		r.clock.Advance(mail.LeaseFor + time.Second)
		if err := r.m.Renew(r.ctx, "s-gold", id, r.delivery(id).Lease); !errors.Is(err, mail.ErrLeaseLost) {
			t.Fatalf("a renew after the lease lapsed: %v", err)
		}
		if d := r.delivery(id); d.State != mail.Queued || d.Attempts != int64(i) || d.Lease != "" {
			t.Fatalf("after lapse %d: %+v", i, d)
		}
	}
	if r.g.woken("p-gold") != mail.MaxAttempts {
		t.Fatalf("gold woken %d times, want %d", r.g.woken("p-gold"), mail.MaxAttempts)
	}
	r.receive("s-gold")
	r.clock.Advance(mail.LeaseFor + time.Second)
	r.receive("s-gold")
	if d := r.delivery(id); d.State != mail.Failed || d.Why != mail.WhyAttempts || d.DoneAt == nil {
		t.Fatalf("after %d lapses: %+v", mail.MaxAttempts, d)
	}
	if ev := r.events("p-jarvis"); len(ev) != 1 || ev[0] != mail.WhyAttempts+":"+sent.Message.ID {
		t.Fatalf("the sender's events: %v", ev)
	}
}

// A renew moves the lease on, without an attempt.
func TestARenewKeepsTheLease(t *testing.T) {
	r := newRig(t)
	r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k"})
	d := r.receive("s-gold").Items[0].Delivery
	r.clock.Advance(mail.LeaseFor - time.Second)
	if err := r.m.Renew(r.ctx, "s-gold", d.ID, d.Lease); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(2 * time.Second)
	if err := r.m.Ack(r.ctx, "s-gold", d.ID, d.Lease); err != nil {
		t.Fatalf("an ack after the renew: %v", err)
	}
	if got := r.delivery(d.ID); got.Attempts != 0 {
		t.Fatalf("attempts %d after a renew", got.Attempts)
	}
}

// A message past its expiry is never received; its delivery expires and its sender is told. A
// request to the human that expires leaves the feed.
func TestAnExpiredMessageEnds(t *testing.T) {
	r := newRig(t)
	toGold := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k1", TTL: time.Minute})
	toUser := r.send("s-jarvis", mail.Send{To: []string{"user"}, Kind: mail.KindRequest, Body: "q", Key: "k2", TTL: time.Minute})
	r.clock.Advance(time.Minute)
	if in, err := r.m.Preview(r.ctx, "s-gold", 10); err != nil || in.Unread != 0 {
		t.Fatalf("a preview past expiry: %+v %v", in, err)
	}
	if got := r.receive("s-gold"); len(got.Items) != 0 {
		t.Fatalf("received past expiry: %+v", got)
	}
	for _, s := range []mail.Sent{toGold, toUser} {
		if d := r.delivery(s.Deliveries[0].ID); d.State != mail.Expired || d.Why != mail.WhyExpired {
			t.Fatalf("the delivery: %+v", d)
		}
	}
	if ev := r.events("p-jarvis"); len(ev) != 2 {
		t.Fatalf("the sender's events: %v", ev)
	}
	ch := r.mem.Changes()
	if last := ch[len(ch)-1]; last != (mailtest.Change{Kind: mail.ChangeExpired, Ref: toUser.Message.ID}) {
		t.Fatalf("the feed: %+v", ch)
	}
}

// A reply acks the request's delivery and sends one response to the requester. A retry with the
// same content answers it even after the lease is gone; other content is ErrKeyReused. Replying to
// a message that is not a request is refused.
func TestAReplyAnswersARequestOnce(t *testing.T) {
	r := newRig(t)
	req := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Kind: mail.KindRequest, Body: "q?", Key: "k"})
	d := r.receive("s-gold").Items[0].Delivery
	p := mail.Reply{Body: "a", Key: "r"}
	resp, err := r.m.Reply(r.ctx, "s-gold", d.ID, d.Lease, p)
	if err != nil || resp.ReplyTo != req.Message.ID || resp.Kind != mail.KindResponse {
		t.Fatalf("the reply: %+v %v", resp, err)
	}
	if r.delivery(d.ID).State != mail.Acked {
		t.Fatal("the request's delivery is not acked")
	}
	if again, err := r.m.Reply(r.ctx, "s-gold", d.ID, "stale", p); err != nil || again.ID != resp.ID {
		t.Fatalf("the retry: %+v %v", again, err)
	}
	p.Body = "changed"
	if _, err := r.m.Reply(r.ctx, "s-gold", d.ID, d.Lease, p); !errors.Is(err, mail.ErrKeyReused) {
		t.Fatalf("a second, different reply: %v", err)
	}
	got := r.receive("s-jarvis")
	if len(got.Items) != 1 || got.Items[0].Message.ID != resp.ID {
		t.Fatalf("the requester's receive: %+v", got)
	}
	m := got.Items[0].Delivery
	if _, err := r.m.Reply(r.ctx, "s-jarvis", m.ID, m.Lease, mail.Reply{Body: "x", Key: "r2"}); !errors.Is(err, mail.ErrInvalid) {
		t.Fatalf("a reply to a response: %v", err)
	}
}

// A delivery to a merged profile goes to its survivor; past mergeHops links it fails
// (recipient_gone) on the original id, and the sender is told; the send still commits.
func TestMergedProfilesForwardToTheirSurvivor(t *testing.T) {
	r := newRig(t)
	merge := func(id, into string) {
		r.write(func(tx mail.Tx) error {
			return tx.PutProfile(crew.Profile{ID: id, Scope: "autovim:old", Codename: id, Status: crew.ProfileMerged, MergedInto: into})
		})
	}
	merge("p-old", "p-gold")
	req := r.send("s-jarvis", mail.Send{To: []string{"session:s-gold"}, Body: "b", Key: "k0"})
	d := r.receive("s-gold").Items[0].Delivery
	if err := r.m.Ack(r.ctx, "s-gold", d.ID, d.Lease); err != nil {
		t.Fatal(err)
	}
	_ = req
	// a reply's sender lookup goes through resolveRecipient: build a chain from p-c0 → … → p-gold
	for i := 0; i < 9; i++ {
		merge(fmt.Sprintf("p-c%d", i), fmt.Sprintf("p-c%d", i+1))
	}
	merge("p-c9", "p-gold")
	r.write(func(tx mail.Tx) error {
		return tx.PutSession(crew.Session{ID: "s-c0", Profile: "p-c0", Host: "autovim:old"})
	})
	q := r.send("s-c0", mail.Send{To: []string{"gold"}, Kind: mail.KindRequest, Body: "q", Key: "k1"})
	gd := r.receive("s-gold").Items[0].Delivery
	resp, err := r.m.Reply(r.ctx, "s-gold", gd.ID, gd.Lease, mail.Reply{Body: "a", Key: "r"})
	if err != nil {
		t.Fatalf("a reply to a sender past the chain's limit still commits: %v", err)
	}
	var to mail.Delivery
	for _, x := range r.mem.Deliveries() {
		if x.Message == resp.ID {
			to = x
		}
	}
	if to.State != mail.Failed || to.Why != mail.WhyRecipientGone || to.Recipient != "p-c0" {
		t.Fatalf("the response's delivery: %+v (request %s)", to, q.Message.ID)
	}
	if ev := r.events("p-gold"); len(ev) != 1 || ev[0] != mail.WhyRecipientGone+":"+resp.ID {
		t.Fatalf("the replier's events: %v", ev)
	}

	// within the limit it reaches the survivor
	merge("p-short", "p-gold")
	r.write(func(tx mail.Tx) error {
		return tx.PutSession(crew.Session{ID: "s-short", Profile: "p-short", Host: "autovim:old"})
	})
	q2 := r.send("s-short", mail.Send{To: []string{"gold"}, Kind: mail.KindRequest, Body: "q", Key: "k2"})
	gd2 := r.receive("s-gold").Items[0].Delivery
	resp2, err := r.m.Reply(r.ctx, "s-gold", gd2.ID, gd2.Lease, mail.Reply{Body: "a", Key: "r2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range r.mem.Deliveries() {
		if x.Message == resp2.ID && (x.Recipient != "p-gold" || x.State != mail.Queued) {
			t.Fatalf("the response to a merged requester (request %s): %+v", q2.Message.ID, x)
		}
	}
}

// A session's end fails its bound deliveries (session_ended) and tells each sender; a notice that
// cannot be delivered never bounces: it shows in the human's history.
func TestASessionsEndFailsItsBoundMail(t *testing.T) {
	r := newRig(t)
	bound := r.send("s-jarvis", mail.Send{To: []string{"session:s-gold"}, Body: "b", Key: "k1"})
	free := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k2"})
	r.write(func(tx mail.Tx) error {
		s, _, _ := tx.Session("s-gold")
		return r.m.EndSessionDeliveries(tx, s, crew.EndedExit)
	})
	if d := r.delivery(bound.Deliveries[0].ID); d.State != mail.Failed || d.Why != mail.WhySessionEnded {
		t.Fatalf("the bound delivery: %+v", d)
	}
	if d := r.delivery(free.Deliveries[0].ID); d.State != mail.Queued {
		t.Fatalf("the profile's delivery: %+v", d)
	}
	ev := r.events("p-jarvis")
	if len(ev) != 1 || ev[0] != mail.WhySessionEnded+":"+bound.Message.ID {
		t.Fatalf("the sender's events: %v", ev)
	}
	// the event itself to a bound session that ends: no notice about a notice
	var evDelivery mail.Delivery
	for _, d := range r.mem.Deliveries() {
		if d.Recipient == "p-jarvis" {
			evDelivery = d
		}
	}
	r.write(func(tx mail.Tx) error {
		evDelivery.Session = "s-jarvis"
		return tx.PutDelivery(evDelivery)
	})
	r.write(func(tx mail.Tx) error {
		s, _, _ := tx.Session("s-jarvis")
		return r.m.EndSessionDeliveries(tx, s, crew.EndedExit)
	})
	if n := len(r.events(mail.DaemonProfile)); n != 0 {
		t.Fatalf("a notice bounced: %d events to the daemon", n)
	}
	ch := r.mem.Changes()
	if last := ch[len(ch)-1]; last.Kind != mail.ChangeUndeliverable {
		t.Fatalf("the feed: %+v", ch)
	}
}

// Adoption: a delivery into lacks moves, with into's next seq; one into already has merges; one
// into's copy had failed is reopened (its sender told); a session-bound one fails. Survivors are
// queued, unbound, and into is woken.
func TestAdoptionNormalizesOrMergesEveryUnhandledDelivery(t *testing.T) {
	r := newRig(t)
	r.profile("p-old", "autodoc:w", "gold", "s-old")
	moves := r.send("s-jarvis", mail.Send{To: []string{"gold@autodoc:w"}, Body: "m", Key: "k1"})
	both := r.send("s-jarvis", mail.Send{To: []string{"gold@autodoc:w", "gold"}, Body: "b", Key: "k2"})
	reopen := r.send("s-jarvis", mail.Send{To: []string{"gold@autodoc:w", "gold"}, Body: "r", Key: "k3"})
	bound := r.send("s-jarvis", mail.Send{To: []string{"session:s-old"}, Body: "s", Key: "k4"})
	var failedCopy mail.Delivery
	for _, d := range reopen.Deliveries {
		if d.Recipient == "p-gold" {
			failedCopy = d
		}
	}
	r.write(func(tx mail.Tx) error {
		failedCopy.State, failedCopy.Why = mail.Failed, mail.WhyAttempts
		return tx.PutDelivery(failedCopy)
	})
	r.receive("s-old") // a lease on the old profile's mail: adoption clears it
	before := r.g.woken("p-gold")
	r.write(func(tx mail.Tx) error { return r.m.MergeDeliveries(tx, "p-old", "p-gold") })

	of := func(s mail.Sent, recipient string) mail.Delivery {
		for _, d := range s.Deliveries {
			if d.Recipient == recipient {
				return r.delivery(d.ID)
			}
		}
		t.Fatalf("no delivery of %s to %s", s.Message.ID, recipient)
		return mail.Delivery{}
	}
	if d := of(moves, "p-old"); d.Recipient != "p-gold" || d.State != mail.Queued || d.Lease != "" || d.Session != "" {
		t.Fatalf("the moved delivery: %+v", d)
	}
	if d := of(both, "p-old"); d.State != mail.Merged {
		t.Fatalf("the duplicate: %+v", d)
	}
	if d := r.delivery(failedCopy.ID); d.State != mail.Queued || d.Why != "" {
		t.Fatalf("into's failed copy: %+v", d)
	}
	if d := of(bound, "p-old"); d.State != mail.Failed || d.Why != mail.WhySessionEnded {
		t.Fatalf("the bound delivery: %+v", d)
	}
	if r.g.woken("p-gold") != before+1 {
		t.Fatal("into was not woken")
	}
	seqs := map[int64]bool{}
	for _, d := range r.mem.Deliveries() {
		if d.Recipient == "p-gold" {
			if seqs[d.Seq] {
				t.Fatalf("seq %d reused for gold", d.Seq)
			}
			seqs[d.Seq] = true
		}
	}
	ev := strings.Join(r.events("p-jarvis"), " ")
	if !strings.Contains(ev, mail.WhyReopened+":"+reopen.Message.ID) || !strings.Contains(ev, mail.WhySessionEnded+":"+bound.Message.ID) {
		t.Fatalf("the sender's events: %s", ev)
	}
}

// Discard fails every unhandled delivery of the profile, each sender told.
func TestDiscardFailsEveryUnhandledDelivery(t *testing.T) {
	r := newRig(t)
	a := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "a", Key: "k1"})
	b := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k2"})
	r.receive("s-gold")
	r.write(func(tx mail.Tx) error { return r.m.DiscardDeliveries(tx, "p-gold") })
	for _, s := range []mail.Sent{a, b} {
		if d := r.delivery(s.Deliveries[0].ID); d.State != mail.Failed || d.Why != mail.WhyDiscarded || d.Lease != "" {
			t.Fatalf("after discard: %+v", d)
		}
	}
	if n := len(r.events("p-jarvis")); n != 2 {
		t.Fatalf("%d events to the sender, want 2", n)
	}
}

// A receive leases at most ReceiveMax, in seq order, and says more remains; a preview leases
// nothing.
func TestAReceiveIsBoundedAndAPreviewLeasesNothing(t *testing.T) {
	r := newRig(t)
	for i := range mail.ReceiveMax + 2 {
		r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: fmt.Sprint(i), Key: fmt.Sprint("k", i)})
	}
	in, err := r.m.Preview(r.ctx, "s-gold", 3)
	if err != nil || in.Unread != mail.ReceiveMax+2 || len(in.Items) != 3 {
		t.Fatalf("the preview: %+v %v", in, err)
	}
	got := r.receive("s-gold")
	if len(got.Items) != mail.ReceiveMax || !got.More {
		t.Fatalf("the first receive: %d items, more %v", len(got.Items), got.More)
	}
	for i, it := range got.Items {
		if it.Delivery.Seq != int64(i+1) {
			t.Fatalf("item %d has seq %d", i, it.Delivery.Seq)
		}
	}
	if rest := r.receive("s-gold"); len(rest.Items) != 2 || rest.More {
		t.Fatalf("the second receive: %+v", rest)
	}
}

// A crew effect on a transaction that is not mail's is an error, never a panic.
func TestTheEffectsRefuseAnotherTransaction(t *testing.T) {
	r := newRig(t)
	if err := r.m.DiscardDeliveries(nil, "p-gold"); err == nil {
		t.Fatal("a nil transaction was taken")
	}
}
