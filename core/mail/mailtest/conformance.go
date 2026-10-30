package mailtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// Stores is one store as each side sees it: the mail module's, the crew module's (the same
// transactions), and a read-only view of every row for the cells' assertions.
type Stores struct {
	Mail    mail.Store
	Crew    crew.Store
	Inspect Inspector
}

// Inspector reads every message, delivery and feed change, for the cells' assertions only.
type Inspector interface {
	Deliveries() []mail.Delivery
	Messages() []mail.Message
	Changes() []crewtest.InboxChange
}

// Harness makes the stores a conformance run tests: New returns empty ones whose transactions read
// the time from now.
type Harness struct {
	New func(t *testing.T, now func() time.Time) Stores
}

// Run is the mail module's conformance suite: every engine's adapter runs it, so the state
// machines are checked the same way on each.
func Run(t *testing.T, h Harness) {
	for _, c := range []struct {
		name string
		fn   func(*testing.T, Harness)
	}{
		{"ASendIsReceivedLeasedAndAcked", testASendIsReceivedLeasedAndAcked},
		{"ASendsKeyMakesOneMessage", testASendsKeyMakesOneMessage},
		{"ASendsAddresses", testASendsAddresses},
		{"ALapsedLeaseIsRedeliveredThenFails", testALapsedLeaseIsRedeliveredThenFails},
		{"ARenewKeepsTheLease", testARenewKeepsTheLease},
		{"AnExpiredMessageEnds", testAnExpiredMessageEnds},
		{"AReplyAnswersARequestOnce", testAReplyAnswersARequestOnce},
		{"MergedProfilesForwardToTheirSurvivor", testMergedProfilesForwardToTheirSurvivor},
		{"ASessionsEndFailsItsBoundMail", testASessionsEndFailsItsBoundMail},
		{"AdoptionNormalizesOrMergesEveryUnhandledDelivery", testAdoptionNormalizesOrMergesEveryUnhandledDelivery},
		{"DiscardFailsEveryUnhandledDelivery", testDiscardFailsEveryUnhandledDelivery},
		{"AGoneScopesProfilesReceiveNothing", testAGoneScopesProfilesReceiveNothing},
		{"AReceiveIsBoundedAndAPreviewLeasesNothing", testAReceiveIsBoundedAndAPreviewLeasesNothing},
		{"HasEligibleSeesOnlyWhatANewSessionMayReceive", testHasEligibleSeesOnlyWhatANewSessionMayReceive},
		{"APreviewSeesLiveMailBehindExpiredMail", testAPreviewSeesLiveMailBehindExpiredMail},
		{"AdoptionsNoticesFollowTheMergeThroughCrewsAdopt", testAdoptionsNoticesFollowTheMergeThroughCrewsAdopt},
		{"DiscardLeavesNothingWaitingThroughCrewsDiscard", testDiscardLeavesNothingWaitingThroughCrewsDiscard},
		{"NextDeadlineIsTheEarliestLive", testNextDeadlineIsTheEarliestLive},
		{"AnItemNamesItsSenderAsAnAddress", testAnItemNamesItsSenderAsAnAddress},
	} {
		t.Run(c.name, func(t *testing.T) { c.fn(t, h) })
	}
}

// guard is the crew port as the mail module sees it: a credential naming a live session passes
// (the rig's credentials are its session ids), and a wake is crew's own, in the transaction, so a
// write that rolls back raises none.
type guard struct{ crew *crew.Manager }

// Guard is the rig's crew port, for another module's suite over the same tables: a session's
// credential is its id, and wakes are crew's own.
func Guard() mail.Crew { return &guard{crew: crew.New(nil)} }

func (g *guard) Authenticate(tx crew.Tx, credential string) (crew.Session, error) {
	id, _, _ := strings.Cut(credential, ".") // the rig's credentials are the session ids
	s, ok, err := tx.Session(id)
	if err != nil {
		return crew.Session{}, err
	}
	if !ok || s.Ended != nil {
		return crew.Session{}, crew.ErrBadCredential
	}
	return s, nil
}

func (g *guard) RaisePending(tx crew.Tx, profile string) error {
	return g.crew.RaisePending(tx, profile)
}

func (g *guard) Received(tx crew.Tx, session string) error { return g.crew.Received(tx, session) }

func (g *guard) Watch() (<-chan struct{}, func()) { return g.crew.Watch() }

type rig struct {
	t     *testing.T
	ctx   context.Context
	clock *crewtest.Clock
	st    Stores
	g     *guard
	m     *mail.Manager
	n     int
}

// newRig has the scope autovim:p with profiles jarvis (session s-jarvis) and gold (s-gold), and the
// human (global/user).
func newRig(t *testing.T, h Harness) *rig {
	r := &rig{t: t, ctx: context.Background(), clock: crewtest.NewClock(), g: &guard{crew: crew.New(nil)}}
	r.st = h.New(t, r.clock.Now)
	r.m = mail.New(r.st.Mail, r.g, mail.WithIDs(func() string { r.n++; return fmt.Sprintf("id%03d", r.n) }))
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
		return tx.PutSession(crew.Session{ID: session, Profile: id, Host: scope, Instance: session})
	})
}

func (r *rig) write(fn func(mail.Tx) error) {
	r.t.Helper()
	if err := r.st.Mail.Write(r.ctx, fn); err != nil {
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

// woken is the pending generation of profile's live session's wake: how many times a committed
// write made mail eligible for it.
func (r *rig) woken(profile string) int {
	r.t.Helper()
	var n int64
	if err := r.st.Mail.Read(r.ctx, func(tx mail.Tx) error {
		s, ok, err := tx.LiveSession(profile)
		if err != nil || !ok {
			return err
		}
		w, _, err := tx.Wake(s.ID)
		n = w.PendingGen
		return err
	}); err != nil {
		r.t.Fatal(err)
	}
	return int(n)
}

func (r *rig) delivery(id string) mail.Delivery {
	r.t.Helper()
	for _, d := range r.st.Inspect.Deliveries() {
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
	for _, msg := range r.st.Inspect.Messages() {
		byID[msg.ID] = msg
	}
	for _, d := range r.st.Inspect.Deliveries() {
		if msg := byID[d.Message]; msg.Kind == mail.KindEvent && d.Recipient == profile {
			out = append(out, msg.Body+":"+msg.ReplyTo)
		}
	}
	return out
}

// A send stores one message and a delivery per recipient in its seq; a receive leases it, wakes
// nothing more, and an ack under the lease ends it. Replaying the ack answers ok; another lease is
// lost; a second receive has nothing.
func testASendIsReceivedLeasedAndAcked(t *testing.T, h Harness) {
	r := newRig(t, h)
	sent := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Subject: "s", Body: "b", Key: "k1"})
	if len(sent.Deliveries) != 1 || sent.Deliveries[0].Recipient != "p-gold" || sent.Deliveries[0].Seq != 1 || sent.Message.Sender != "p-jarvis" {
		t.Fatalf("the send: %+v", sent)
	}
	if r.woken("p-gold") != 1 {
		t.Fatalf("gold woken %d times, want 1", r.woken("p-gold"))
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
func testASendsKeyMakesOneMessage(t *testing.T, h Harness) {
	r := newRig(t, h)
	p := mail.Send{To: []string{"gold"}, Body: "b", Key: "k"}
	first := r.send("s-jarvis", p)
	if again := r.send("s-jarvis", p); again.Message.ID != first.Message.ID || len(again.Deliveries) != 1 {
		t.Fatalf("the replay: %+v, want %+v", again, first)
	}
	p.Body = "other"
	if _, err := r.m.Send(r.ctx, "s-jarvis", p); !errors.Is(err, mail.ErrKeyReused) {
		t.Fatalf("the key with other content: %v", err)
	}
	if n := len(r.st.Inspect.Deliveries()); n != 1 {
		t.Fatalf("%d deliveries, want 1", n)
	}
}

// Addresses: a name in the sender's scope, user, a live session (bound to it), name@scope. One that
// resolves to nothing, or to the daemon, is ErrNoRecipient, and the send stores nothing.
func testASendsAddresses(t *testing.T, h Harness) {
	r := newRig(t, h)
	r.profile("p-zen", "autovim:other", "zen", "s-zen")
	sent := r.send("s-jarvis", mail.Send{To: []string{"user", "session:s-gold", "zen@other", "user"}, Body: "b", Key: "k"})
	var got []string
	for _, d := range sent.Deliveries {
		got = append(got, d.Recipient+"/"+d.Session)
	}
	if want := "p-user/ p-gold/s-gold p-zen/"; strings.Join(got, " ") != want {
		t.Fatalf("the deliveries: %v, want %s (one per recipient)", got, want)
	}
	// one profile bound two ways is refused, never narrowed or widened
	if _, err := r.m.Send(r.ctx, "s-jarvis", mail.Send{To: []string{"gold", "session:s-gold"}, Body: "b", Key: "k-two"}); !errors.Is(err, mail.ErrInvalid) {
		t.Fatalf("gold and one of its sessions in one send: %v", err)
	}
	if ch := r.st.Inspect.Changes(); len(ch) != 1 || ch[0] != (crewtest.InboxChange{Kind: mail.ChangeReport, Ref: sent.Message.ID}) {
		t.Fatalf("the human's feed: %+v", ch)
	}
	for _, to := range []string{"nobody", "autodoc@global", "session:s-gone", "gold@nowhere"} {
		before := len(r.st.Inspect.Messages())
		if _, err := r.m.Send(r.ctx, "s-jarvis", mail.Send{To: []string{"gold", to}, Body: "b", Key: "k-" + to}); !errors.Is(err, mail.ErrNoRecipient) {
			t.Errorf("to %s: %v", to, err)
		}
		if len(r.st.Inspect.Messages()) != before {
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
// fails, and its sender gets one event. A refused verb's write commits nothing, its expiry
// included: the next write that commits makes the lapse real.
func testALapsedLeaseIsRedeliveredThenFails(t *testing.T, h Harness) {
	r := newRig(t, h)
	sent := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k"})
	id := sent.Deliveries[0].ID
	for i := range mail.MaxAttempts {
		got := r.receive("s-gold")
		if len(got.Items) != 1 || got.Items[0].Delivery.Attempts != int64(i) {
			t.Fatalf("receive %d: %+v, want one delivery after %d lapses", i+1, got, i)
		}
		r.clock.Advance(mail.LeaseFor + time.Second)
		if err := r.m.Renew(r.ctx, "s-gold", id, got.Items[0].Delivery.Lease); !errors.Is(err, mail.ErrLeaseLost) {
			t.Fatalf("a renew after the lease lapsed: %v", err)
		}
	}
	if got := r.receive("s-gold"); len(got.Items) != 0 {
		t.Fatalf("received after %d lapses: %+v", mail.MaxAttempts, got)
	}
	if d := r.delivery(id); d.State != mail.Failed || d.Why != mail.WhyAttempts || d.DoneAt == nil || d.Lease != "" {
		t.Fatalf("after %d lapses: %+v", mail.MaxAttempts, d)
	}
	// the send's wake, then one per redelivery; the last lapse fails it instead
	if r.woken("p-gold") != mail.MaxAttempts {
		t.Fatalf("gold woken %d times, want %d", r.woken("p-gold"), mail.MaxAttempts)
	}
	if ev := r.events("p-jarvis"); len(ev) != 1 || ev[0] != mail.WhyAttempts+":"+sent.Message.ID {
		t.Fatalf("the sender's events: %v", ev)
	}
}

// A renew moves the lease on, without an attempt.
func testARenewKeepsTheLease(t *testing.T, h Harness) {
	r := newRig(t, h)
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
func testAnExpiredMessageEnds(t *testing.T, h Harness) {
	r := newRig(t, h)
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
	ch := r.st.Inspect.Changes()
	if last := ch[len(ch)-1]; last != (crewtest.InboxChange{Kind: mail.ChangeExpired, Ref: toUser.Message.ID}) {
		t.Fatalf("the feed: %+v", ch)
	}
}

// A reply acks the request's delivery and sends one response to the requester. A retry with the
// same content answers it even after the lease is gone; other content is ErrKeyReused. Replying to
// a message that is not a request is refused.
func testAReplyAnswersARequestOnce(t *testing.T, h Harness) {
	r := newRig(t, h)
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
func testMergedProfilesForwardToTheirSurvivor(t *testing.T, h Harness) {
	r := newRig(t, h)
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
		return tx.PutSession(crew.Session{ID: "s-c0", Profile: "p-c0", Host: "autovim:old", Instance: "s-c0"})
	})
	q := r.send("s-c0", mail.Send{To: []string{"gold@p"}, Kind: mail.KindRequest, Body: "q", Key: "k1"})
	gd := r.receive("s-gold").Items[0].Delivery
	resp, err := r.m.Reply(r.ctx, "s-gold", gd.ID, gd.Lease, mail.Reply{Body: "a", Key: "r"})
	if err != nil {
		t.Fatalf("a reply to a sender past the chain's limit still commits: %v", err)
	}
	var to mail.Delivery
	for _, x := range r.st.Inspect.Deliveries() {
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
		return tx.PutSession(crew.Session{ID: "s-short", Profile: "p-short", Host: "autovim:old", Instance: "s-short"})
	})
	q2 := r.send("s-short", mail.Send{To: []string{"gold@p"}, Kind: mail.KindRequest, Body: "q", Key: "k2"})
	var gd2 mail.Delivery // the first reply's recipient_gone notice is gold's mail too
	for _, it := range r.receive("s-gold").Items {
		if it.Message.ID == q2.Message.ID {
			gd2 = it.Delivery
		}
	}
	resp2, err := r.m.Reply(r.ctx, "s-gold", gd2.ID, gd2.Lease, mail.Reply{Body: "a", Key: "r2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range r.st.Inspect.Deliveries() {
		if x.Message == resp2.ID && (x.Recipient != "p-gold" || x.State != mail.Queued) {
			t.Fatalf("the response to a merged requester (request %s): %+v", q2.Message.ID, x)
		}
	}
}

// A session's end fails its bound deliveries (session_ended) and tells each sender; a notice that
// cannot be delivered never bounces: it shows in the human's history.
func testASessionsEndFailsItsBoundMail(t *testing.T, h Harness) {
	r := newRig(t, h)
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
	for _, d := range r.st.Inspect.Deliveries() {
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
	ch := r.st.Inspect.Changes()
	if last := ch[len(ch)-1]; last.Kind != mail.ChangeUndeliverable {
		t.Fatalf("the feed: %+v", ch)
	}
}

// Adoption: a delivery into lacks moves, with into's next seq; one into already has merges; one
// into's copy had failed is reopened (its sender told); a session-bound one fails. Survivors are
// queued, unbound, and into is woken.
func testAdoptionNormalizesOrMergesEveryUnhandledDelivery(t *testing.T, h Harness) {
	r := newRig(t, h)
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
	// a message p-old sent to itself and to gold, whose gold copy failed: its reopening notice is to
	// p-old as the sender, so it must follow p-old's forwarding to gold
	self := r.send("s-old", mail.Send{To: []string{"gold@autodoc:w", "gold@p"}, Body: "self", Key: "k5"})
	for _, d := range self.Deliveries {
		if d.Recipient == "p-gold" {
			r.write(func(tx mail.Tx) error { d.State, d.Why = mail.Failed, mail.WhyAttempts; return tx.PutDelivery(d) })
		}
	}
	r.receive("s-old") // a lease on the old profile's mail: adoption clears it
	before := r.woken("p-gold")
	// crew's order: the forwarding is real first, then the mail moves
	r.write(func(tx mail.Tx) error {
		p, _, _ := tx.Profile("p-old")
		p.Status, p.MergedInto = crew.ProfileMerged, "p-gold"
		if err := tx.PutProfile(p); err != nil {
			return err
		}
		return r.m.MergeDeliveries(tx, "p-old", "p-gold")
	})
	if ev := r.events("p-gold"); len(ev) != 1 || ev[0] != mail.WhyReopened+":"+self.Message.ID {
		t.Fatalf("the self-sent message's reopening notice went to %v, want gold (p-old's survivor)", ev)
	}
	if ev := r.events("p-old"); len(ev) != 0 {
		t.Fatalf("a notice was left on the merged profile: %v", ev)
	}

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
	// one wake for the mail that moved, one for the reopening notice that followed p-old's forwarding
	if got := r.woken("p-gold"); got != before+2 {
		t.Fatalf("into woken %d times by the adoption, want 2", got-before)
	}
	seqs := map[int64]bool{}
	for _, d := range r.st.Inspect.Deliveries() {
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

// Discard fails every unhandled delivery of the profile, each sender told; UnhandledCount counts
// queued and leased ones alike.
func testDiscardFailsEveryUnhandledDelivery(t *testing.T, h Harness) {
	r := newRig(t, h)
	a := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "a", Key: "k1"})
	b := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k2"})
	r.receive("s-gold")
	count := func() int {
		var n int
		r.write(func(tx mail.Tx) error { var err error; n, err = r.m.UnhandledCount(tx, "p-gold"); return err })
		return n
	}
	if n := count(); n != 2 {
		t.Fatalf("unhandled before the discard: %d, want 2 (both leased)", n)
	}
	r.write(func(tx mail.Tx) error { return r.m.DiscardDeliveries(tx, "p-gold") })
	if n := count(); n != 0 {
		t.Fatalf("unhandled after the discard: %d", n)
	}
	for _, s := range []mail.Sent{a, b} {
		if d := r.delivery(s.Deliveries[0].ID); d.State != mail.Failed || d.Why != mail.WhyDiscarded || d.Lease != "" {
			t.Fatalf("after discard: %+v", d)
		}
	}
	if n := len(r.events("p-jarvis")); n != 2 {
		t.Fatalf("%d events to the sender, want 2", n)
	}
}

// A profile of a gone scope receives nothing again (Zen's #62 r2): a discard's notice to the
// discarded profile as a sender (a message it sent itself) fails recipient_gone and shows in the
// human's history instead of waiting where nothing can receive it, and a later send into the
// scope is refused. crew marks the scope gone before the mail effects run.
func testAGoneScopesProfilesReceiveNothing(t *testing.T, h Harness) {
	r := newRig(t, h)
	r.profile("p-old", "autodoc:w", "gold", "s-old")
	self := r.send("s-old", mail.Send{To: []string{"gold", "gold@p"}, Body: "self", Key: "k"})
	r.write(func(tx mail.Tx) error {
		if err := tx.PutScope(crew.Scope{ID: "autodoc:w", Kind: "autodoc", State: crew.ScopeGone}); err != nil {
			return err
		}
		return r.m.DiscardDeliveries(tx, "p-old")
	})
	for _, d := range r.st.Inspect.Deliveries() {
		if d.Recipient == "p-old" && (d.State == mail.Queued || d.State == mail.Leased) {
			t.Fatalf("a delivery waits on the gone scope's profile: %+v", d)
		}
	}
	var notice mail.Delivery
	for _, m := range r.st.Inspect.Messages() {
		if m.Kind == mail.KindEvent && m.ReplyTo == self.Message.ID {
			for _, d := range r.st.Inspect.Deliveries() {
				if d.Message == m.ID {
					notice = d
				}
			}
		}
	}
	if notice.State != mail.Failed || notice.Why != mail.WhyRecipientGone {
		t.Fatalf("the discard's notice to the discarded sender: %+v, want failed recipient_gone", notice)
	}
	if ch := r.st.Inspect.Changes(); len(ch) == 0 || ch[len(ch)-1].Kind != mail.ChangeUndeliverable {
		t.Fatalf("the human's feed: %+v, want the undeliverable notice", ch)
	}
	if _, err := r.m.Send(r.ctx, "s-jarvis", mail.Send{To: []string{"gold@autodoc:w"}, Body: "b", Key: "later"}); !errors.Is(err, mail.ErrNoRecipient) {
		t.Fatalf("a send into the gone scope: %v, want ErrNoRecipient", err)
	}
}

// A receive leases at most ReceiveMax, in seq order, and says more remains; a preview leases
// nothing.
func testAReceiveIsBoundedAndAPreviewLeasesNothing(t *testing.T, h Harness) {
	r := newRig(t, h)
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

// HasEligible sees a queued, unbound, unexpired delivery only, however much expired mail lies
// before it.
func testHasEligibleSeesOnlyWhatANewSessionMayReceive(t *testing.T, h Harness) {
	r := newRig(t, h)
	has := func() bool {
		var ok bool
		r.write(func(tx mail.Tx) error { var err error; ok, err = r.m.HasEligible(tx, "p-gold"); return err })
		return ok
	}
	r.send("s-jarvis", mail.Send{To: []string{"session:s-gold"}, Body: "b", Key: "k1"})
	r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k2", TTL: time.Minute})
	if !has() {
		t.Fatal("an unbound queued delivery is not eligible")
	}
	r.clock.Advance(time.Minute)
	if has() {
		t.Fatal("a bound delivery, or one past its expiry, is eligible for a new session")
	}
	// more expired mail than one look reads, then one live delivery behind it, in seq order
	for i := range 300 {
		r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "old", Key: fmt.Sprint("x", i), TTL: time.Minute})
	}
	r.clock.Advance(time.Minute)
	r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "live", Key: "live"})
	if !has() {
		t.Fatal("a live delivery behind 300 expired ones was not seen")
	}
}

// A preview reads: expire doesn't run in it, so eligibility itself must leave expired mail out
// before the count's bound. More expired deliveries than a preview counts, then live ones behind
// them: the preview counts the live ones and lists them first.
func testAPreviewSeesLiveMailBehindExpiredMail(t *testing.T, h Harness) {
	r := newRig(t, h)
	for i := range 1001 {
		r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "old", Key: fmt.Sprint("x", i), TTL: time.Minute})
	}
	r.clock.Advance(time.Minute)
	for i := range 2 {
		r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "live", Key: fmt.Sprint("live", i)})
	}
	in, err := r.m.Preview(r.ctx, "s-gold", 5)
	if err != nil {
		t.Fatal(err)
	}
	if in.Unread != 2 || len(in.Items) != 2 || in.Items[0].Message.Body != "live" {
		t.Fatalf("the preview behind 1001 expired deliveries: unread %d, %d items", in.Unread, len(in.Items))
	}
}

// lazyCrew is the crew Manager as mail's port, set once both modules exist: each is the other's
// dependency (mail authenticates through crew; crew's adopt calls mail's effects).
type lazyCrew struct{ m *crew.Manager }

func (c *lazyCrew) Authenticate(tx crew.Tx, credential string) (crew.Session, error) {
	return c.m.Authenticate(tx, credential)
}

func (c *lazyCrew) RaisePending(tx crew.Tx, profile string) error {
	return c.m.RaisePending(tx, profile)
}

func (c *lazyCrew) Received(tx crew.Tx, session string) error { return c.m.Received(tx, session) }

func (c *lazyCrew) Watch() (<-chan struct{}, func()) { return c.m.Watch() }

// The real crew.Adopt over mail's real effects, on one store (Zen's #62 r0 F3): a profile of the
// adopted scope sent itself and its namesake in the adopting scope a message, and the namesake's
// copy had failed. Adoption reopens it, and the reopening notice, to the old profile as the sender,
// must follow that profile's merge to the survivor: nothing is left on a profile nobody receives.
func testAdoptionsNoticesFollowTheMergeThroughCrewsAdopt(t *testing.T, h Harness) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	st := h.New(t, clock.Now)
	port := &lazyCrew{}
	mm := mail.New(st.Mail, port)
	cm := crew.New(st.Crew, crew.WithSessionEnder(mm))
	port.m = cm

	secret := strings.Repeat("s", 40)
	host := func(scope, run string) int64 {
		t.Helper()
		e, err := cm.Claim(ctx, scope, run, "")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	register := func(scope, run string, epoch int64, instance string) crew.Registered {
		t.Helper()
		r, err := cm.Register(ctx, crew.Registration{Scope: scope, Run: run, Epoch: epoch, Codename: "gold",
			Kind: "claude", Workspace: "w", Instance: instance, Secret: secret})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	oldEpoch := host("autodoc:old", "r1")
	old := register("autodoc:old", "r1", oldEpoch, "i1")
	newEpoch := host("autodoc:new", "r2")
	survivor := register("autodoc:new", "r2", newEpoch, "i2")

	self, err := mm.Send(ctx, crew.Credential(old.Session, secret), mail.Send{To: []string{"gold", "gold@autodoc:new"}, Body: "self", Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Mail.Write(ctx, func(tx mail.Tx) error {
		for _, d := range self.Deliveries {
			if d.Recipient == survivor.Profile {
				d.State, d.Why = mail.Failed, mail.WhyAttempts
				return tx.PutDelivery(d)
			}
		}
		t.Fatal("no delivery to the survivor")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := cm.Release(ctx, "autodoc:old", "r1", oldEpoch); err != nil {
		t.Fatal(err)
	}
	if err := cm.Adopt(ctx, "autodoc:old", "autodoc:new", "r2", newEpoch); err != nil {
		t.Fatal(err)
	}

	byID := map[string]mail.Message{}
	for _, m := range st.Inspect.Messages() {
		byID[m.ID] = m
	}
	reopened := 0
	for _, d := range st.Inspect.Deliveries() {
		m := byID[d.Message]
		if d.Recipient == old.Profile && (d.State == mail.Queued || d.State == mail.Leased) {
			t.Errorf("a delivery was left on the merged profile: %+v (%s %q)", d, m.Kind, m.Body)
		}
		if m.Kind == mail.KindEvent && m.Body == mail.WhyReopened && m.ReplyTo == self.Message.ID {
			reopened++
			if d.Recipient != survivor.Profile || d.State != mail.Queued {
				t.Errorf("the reopening notice: %+v, want queued for the survivor %s", d, survivor.Profile)
			}
		}
	}
	if reopened != 1 {
		t.Fatalf("%d reopening notices, want 1", reopened)
	}
}

// The real crew.Discard over mail's real effects: a discarded profile's self-sent message leaves
// nothing waiting on it, its notice is undeliverable history, and a later send into the scope is
// refused (Zen's #62 r2). It needs crew to mark the scope gone before DiscardDeliveries.
func testDiscardLeavesNothingWaitingThroughCrewsDiscard(t *testing.T, h Harness) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	st := h.New(t, clock.Now)
	port := &lazyCrew{}
	mm := mail.New(st.Mail, port)
	cm := crew.New(st.Crew, crew.WithSessionEnder(mm))
	port.m = cm
	secret := strings.Repeat("s", 40)
	oldEpoch, err := cm.Claim(ctx, "autodoc:old", "r1", "")
	if err != nil {
		t.Fatal(err)
	}
	old, err := cm.Register(ctx, crew.Registration{Scope: "autodoc:old", Run: "r1", Epoch: oldEpoch, Codename: "gold",
		Kind: "claude", Workspace: "w", Instance: "i1", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	newEpoch, err := cm.Claim(ctx, "autodoc:new", "r2", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := cm.Register(ctx, crew.Registration{Scope: "autodoc:new", Run: "r2", Epoch: newEpoch, Codename: "zen",
		Kind: "claude", Workspace: "w", Instance: "i2", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mm.Send(ctx, crew.Credential(old.Session, secret), mail.Send{To: []string{"gold"}, Body: "self", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := cm.Release(ctx, "autodoc:old", "r1", oldEpoch); err != nil {
		t.Fatal(err)
	}
	if err := cm.Discard(ctx, "autodoc:old", "autodoc:new", "r2", newEpoch); err != nil {
		t.Fatal(err)
	}
	for _, d := range st.Inspect.Deliveries() {
		if d.Recipient == old.Profile && (d.State == mail.Queued || d.State == mail.Leased) {
			t.Errorf("a delivery waits on the discarded profile: %+v", d)
		}
	}
	if _, err := mm.Send(ctx, crew.Credential(other.Session, secret), mail.Send{To: []string{"gold@autodoc:old"}, Body: "b", Key: "later"}); !errors.Is(err, mail.ErrNoRecipient) {
		t.Fatalf("a send into the discarded scope: %v, want ErrNoRecipient", err)
	}
}

// NextDeadline, which the wake scheduler's timer reads, is the earliest instant mail changes by time
// alone: none with no mail; a queued delivery's message expiry; a lease's end when it comes first;
// never a finished delivery's.
func testNextDeadlineIsTheEarliestLive(t *testing.T, h Harness) {
	r := newRig(t, h)
	next := func() (int64, bool) {
		t.Helper()
		var at int64
		var ok bool
		if err := r.st.Mail.Read(r.ctx, func(tx mail.Tx) error {
			var err error
			at, ok, err = r.m.NextDeadline(tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return at, ok
	}
	if _, ok := next(); ok {
		t.Fatal("a deadline with no mail")
	}
	now := r.clock.Now()
	// a finished delivery: its message's expiry (the earliest of all) must not count
	done := r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "done", Key: "k-done", TTL: time.Minute})
	got := r.receive("s-gold")
	if err := r.m.Ack(r.ctx, "s-gold", got.Items[0].Delivery.ID, got.Items[0].Delivery.Lease); err != nil {
		t.Fatal(err)
	}
	_ = done
	if _, ok := next(); ok {
		t.Fatal("a deadline from an acked delivery")
	}
	r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "live", Key: "k-live", TTL: 30 * time.Minute})
	if at, ok := next(); !ok || at != now.Add(30*time.Minute).UnixNano() {
		t.Fatalf("a queued delivery's deadline: %d %v, want its message's expiry", at, ok)
	}
	r.receive("s-gold") // its lease ends in 10 minutes, before the expiry
	if at, ok := next(); !ok || at != now.Add(mail.LeaseFor).UnixNano() {
		t.Fatalf("a leased delivery's deadline: %d %v, want its lease's end", at, ok)
	}
}

// Each received or previewed item names its sender as the address the receiver would reply to: the
// bare codename in the same scope, name@project from another AutoVim scope, and "autodoc" for the
// daemon's notices.
func testAnItemNamesItsSenderAsAnAddress(t *testing.T, h Harness) {
	r := newRig(t, h)
	r.profile("p-zen", "autovim:other", "zen", "s-zen")
	r.send("s-jarvis", mail.Send{To: []string{"gold"}, Body: "same scope", Key: "k1"})
	r.send("s-zen", mail.Send{To: []string{"gold@p"}, Body: "other scope", Key: "k2"})
	in, err := r.m.Preview(r.ctx, "s-gold", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := r.receive("s-gold")
	for _, items := range [][]mail.Item{in.Items, got.Items} {
		var froms []string
		for _, it := range items {
			froms = append(froms, it.From)
		}
		if strings.Join(froms, ",") != "jarvis,zen@other" {
			t.Fatalf("the senders: %v, want jarvis,zen@other", froms)
		}
	}
	// a sender merged into a survivor after it sent is named as its survivor, which a reply reaches
	r.profile("p-old", "autodoc:w", "gold", "s-old")
	r.send("s-old", mail.Send{To: []string{"jarvis@autovim:p"}, Body: "before the merge", Key: "k3"})
	r.write(func(tx mail.Tx) error {
		if err := tx.PutScope(crew.Scope{ID: "autodoc:w", Kind: "autodoc", State: crew.ScopeGone}); err != nil {
			return err
		}
		p, _, _ := tx.Profile("p-old")
		p.Status, p.MergedInto = crew.ProfileMerged, "p-gold"
		return tx.PutProfile(p)
	})
	merged := r.receive("s-jarvis")
	if len(merged.Items) != 1 || merged.Items[0].From != "gold" || merged.Items[0].Message.Sender != "p-old" {
		t.Fatalf("a merged sender's item: %+v, want From gold (the survivor), Sender p-old (history)", merged.Items)
	}
	if _, err := r.m.Send(r.ctx, "s-jarvis", mail.Send{To: []string{merged.Items[0].From}, Body: "reply", Key: "k4"}); err != nil {
		t.Fatalf("a reply to the advertised address: %v", err)
	}
	// handled: acked, so the clock below redelivers nothing of it alongside the notices
	if err := r.m.Ack(r.ctx, "s-jarvis", merged.Items[0].Delivery.ID, merged.Items[0].Delivery.Lease); err != nil {
		t.Fatal(err)
	}
	// a delivery that ends unhandled makes a notice from the daemon
	r.clock.Advance(mail.LeaseFor + time.Second)
	for range mail.MaxAttempts {
		r.receive("s-gold")
		r.clock.Advance(mail.LeaseFor + time.Second)
	}
	notice := r.receive("s-jarvis") // the failures of jarvis's two sends, the reply above included
	if len(notice.Items) == 0 {
		t.Fatal("no notice reached jarvis")
	}
	for _, it := range notice.Items {
		if it.Message.Kind != mail.KindEvent || it.From != "autodoc" {
			t.Fatalf("the daemon's notice: %+v, want an event from autodoc", it)
		}
	}
}
