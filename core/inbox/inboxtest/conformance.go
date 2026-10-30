// Package inboxtest is the inbox module's conformance suite: every engine's adapter runs it, so the
// human feed (ADR 1793271500 §4.8) and §5 case 7 hold the same way on each.
package inboxtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/mail/mailtest"
)

// Stores is one store as each module sees it: the same transactions throughout.
type Stores struct {
	Inbox inbox.Store
	Mail  mail.Store
}

// Harness makes the stores a run tests: New returns empty ones whose transactions read the time
// from now, and whose committed writes tell sig.
type Harness struct {
	New func(t *testing.T, now func() time.Time, sig crew.Signals) Stores
}

// Run is the inbox module's conformance suite.
func Run(t *testing.T, h Harness) {
	for _, c := range []struct {
		name string
		fn   func(*testing.T, Harness)
	}{
		{"AWatchSnapshotsThenFollowsTheJournal", testAWatchSnapshotsThenFollowsTheJournal},
		{"AWatchWakesOnACommitAndARollbackTellsNothing", testAWatchWakesOnACommitAndARollbackTellsNothing},
		{"AReplyWinsOnceAndReplaysExactly", testAReplyWinsOnceAndReplaysExactly},
		{"AReplyRacingExpiryGetsOneOutcome", testAReplyRacingExpiryGetsOneOutcome},
		{"ADismissTellsTheSender", testADismissTellsTheSender},
		{"AReportIsReadOnce", testAReportIsReadOnce},
		{"TheHistoryAndUndeliverableNotices", testTheHistoryAndUndeliverableNotices},
		{"FocusResolvesTheCurrentOwner", testFocusResolvesTheCurrentOwner},
		{"ReservedProfilesAreMadeOnce", testReservedProfilesAreMadeOnce},
	} {
		t.Run(c.name, func(t *testing.T) { c.fn(t, h) })
	}
}

type rig struct {
	t     *testing.T
	ctx   context.Context
	clock *crewtest.Clock
	st    Stores
	feed  *inbox.Feed
	wakes atomic.Int64 // Signals.Wake calls
	feeds atomic.Int64 // Signals.Feed calls
	m     *mail.Manager
	in    *inbox.Manager
	n     int
}

// newRig has the reserved profiles (EnsureReserved, as serve makes them), and in scope autovim:p
// the profiles jarvis (session s-jarvis) and gold (s-gold).
func newRig(t *testing.T, h Harness) *rig {
	r := &rig{t: t, ctx: context.Background(), clock: crewtest.NewClock(), feed: inbox.NewFeed()}
	r.st = h.New(t, r.clock.Now, crew.Signals{
		Feed: func() { r.feeds.Add(1); r.feed.Notify() },
		Wake: func() { r.wakes.Add(1) },
	})
	r.m = mail.New(r.st.Mail, mailtest.Guard(), mail.WithIDs(func() string { r.n++; return fmt.Sprintf("id%03d", r.n) }))
	r.in = inbox.New(r.st.Inbox, r.m, r.feed)
	if err := r.m.EnsureReserved(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.agent("p-jarvis", "jarvis", "s-jarvis")
	r.agent("p-gold", "gold", "s-gold")
	return r
}

func (r *rig) agent(id, codename, session string) {
	r.t.Helper()
	r.write(func(tx inbox.Tx) error {
		if err := tx.PutProfile(crew.Profile{ID: id, Scope: "autovim:p", Codename: codename, Status: crew.ProfileActive}); err != nil {
			return err
		}
		if err := tx.PutSession(crew.Session{ID: session, Profile: id, Host: "autovim:p", Instance: session}); err != nil {
			return err
		}
		return tx.PutWake(crew.Wake{Session: session, State: crew.WakeIdle})
	})
}

func (r *rig) write(fn func(inbox.Tx) error) {
	r.t.Helper()
	if err := r.st.Inbox.Write(r.ctx, fn); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) read(fn func(inbox.Tx) error) {
	r.t.Helper()
	if err := r.st.Inbox.Read(r.ctx, fn); err != nil {
		r.t.Fatal(err)
	}
}

// ask sends the human a request (or, with kind "", a report) from session.
func (r *rig) ask(session, kind, subject string, ttl time.Duration) mail.Message {
	r.t.Helper()
	s, err := r.m.Send(r.ctx, session, mail.Send{To: []string{mail.UserCodename}, Kind: kind, Subject: subject,
		Body: subject + " body", Key: "k-" + subject, TTL: ttl})
	if err != nil {
		r.t.Fatal(err)
	}
	return s.Message
}

func (r *rig) watch(cursor int64, wait time.Duration) inbox.Watched {
	r.t.Helper()
	w, err := r.in.Watch(r.ctx, cursor, wait)
	if err != nil {
		r.t.Fatal(err)
	}
	return w
}

func kinds(ch []inbox.Change) []string {
	var out []string
	for _, c := range ch {
		out = append(out, c.Kind+":"+c.Ref)
	}
	return out
}

func messages(items []inbox.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Message.Subject)
	}
	return out
}

// responses is every response delivered to profile, by body.
func (r *rig) responses(profile string) []string {
	var out []string
	r.read(func(tx inbox.Tx) error {
		items, err := tx.ToUser(profile, 0, 0) // the profile's deliveries, newest first
		for _, it := range items {
			if it.Message.Kind == mail.KindResponse {
				out = append(out, it.Message.Body)
			}
		}
		return err
	})
	return out
}

// A fresh journal has nothing to show, so a watch at cursor 0 waits rather than answering an empty
// snapshot whose cursor would answer the next watch at once. Once it has rows, cursor 0 is a
// snapshot of what is open (the requests and unread reports, and raised attention) with the
// cursor of that same read; the next watch follows the changes after it, and only them.
func testAWatchSnapshotsThenFollowsTheJournal(t *testing.T, h Harness) {
	r := newRig(t, h)
	if w := r.watch(0, 0); w.Snapshot != nil || len(w.Changes) != 0 || w.Cursor != 0 {
		t.Fatalf("a watch of a journal that never had a row: %+v, want no snapshot, no changes, cursor 0", w)
	}
	q := r.ask("s-jarvis", mail.KindRequest, "decide", 0)
	rep := r.ask("s-gold", "", "checkpoint", 0)
	w := r.watch(0, 0)
	if w.Snapshot == nil || !slices.Equal(messages(w.Snapshot.Items), []string{"decide", "checkpoint"}) {
		t.Fatalf("the first snapshot: %+v, want the request and the report", w)
	}
	if it := w.Snapshot.Items[0]; it.Codename != "jarvis" || it.Scope != "autovim:p" || it.Message.SenderSession != "s-jarvis" {
		t.Fatalf("a snapshot item's sender: %+v, want jarvis of autovim:p, from s-jarvis", it)
	}
	if w.Cursor != 2 {
		t.Fatalf("the snapshot's cursor: %d, want 2 (the request's row and the report's)", w.Cursor)
	}
	if again := r.watch(w.Cursor, 0); again.Snapshot != nil || len(again.Changes) != 0 || again.Cursor != w.Cursor {
		t.Fatalf("a watch at the snapshot's cursor, nothing new: %+v", again)
	}
	if _, err := r.in.Reply(r.ctx, q.ID, "yes", "r1"); err != nil {
		t.Fatal(err)
	}
	next := r.watch(w.Cursor, 0)
	if !slices.Equal(kinds(next.Changes), []string{"answered:" + q.ID}) || next.Cursor != w.Cursor+1 {
		t.Fatalf("the changes after the snapshot: %+v, want the answer alone", next)
	}
	snap := r.watch(0, 0)
	if snap.Snapshot == nil || !slices.Equal(messages(snap.Snapshot.Items), []string{"checkpoint"}) || snap.Cursor != next.Cursor {
		t.Fatalf("a reconnecting window's snapshot: %+v, want the report alone at cursor %d", snap, next.Cursor)
	}
	// a cursor past the newest is another store's: a snapshot
	if far := r.watch(99, 0); far.Snapshot == nil || far.Cursor != next.Cursor {
		t.Fatalf("a cursor past the journal: %+v, want a snapshot", far)
	}
	if rep.Kind != mail.KindMessage {
		t.Fatalf("a report's kind: %q", rep.Kind)
	}
}

// A watch waiting at its cursor returns as soon as a write commits a change, without polling; a
// write that rolls back tells nothing, and one that leaves a wake row due tells the scheduler.
func testAWatchWakesOnACommitAndARollbackTellsNothing(t *testing.T, h Harness) {
	r := newRig(t, h)
	feeds, wakes := r.feeds.Load(), r.wakes.Load()
	boom := errors.New("rolled back")
	if err := r.st.Inbox.Write(r.ctx, func(tx inbox.Tx) error {
		if err := tx.AddInboxChange("report", "x"); err != nil {
			return err
		}
		if err := tx.PutWake(crew.Wake{Session: "s-gold", State: crew.WakeIdle, PendingGen: 1}); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("the rolled-back write: %v", err)
	}
	if r.feeds.Load() != feeds || r.wakes.Load() != wakes {
		t.Fatalf("a rolled-back write told feed %d, wake %d times; want none", r.feeds.Load()-feeds, r.wakes.Load()-wakes)
	}
	r.ask("s-gold", "", "checkpoint", 0)
	at := r.watch(0, 0).Cursor // a snapshot's cursor: whenever the watch below starts, it follows from here
	got := make(chan inbox.Watched, 1)
	go func() {
		w, err := r.in.Watch(r.ctx, at, inbox.MaxWait)
		if err != nil {
			t.Error(err)
		}
		got <- w
	}()
	// the send to gold leaves gold's wake row due: the scheduler is told; the human's feed isn't
	if _, err := r.m.Send(r.ctx, "s-jarvis", mail.Send{To: []string{"gold"}, Body: "b", Key: "k-gold"}); err != nil {
		t.Fatal(err)
	}
	if r.wakes.Load() != wakes+1 {
		t.Fatalf("a send to an agent told the scheduler %d times, want once", r.wakes.Load()-wakes)
	}
	q := r.ask("s-jarvis", mail.KindRequest, "decide", 0)
	select {
	case w := <-got:
		if !slices.Equal(kinds(w.Changes), []string{"request:" + q.ID}) {
			t.Fatalf("the woken watch: %+v, want the request", w)
		}
	// NOTE: a bound on a failing run only: a watch the commit never woke fails here rather than
	// hanging the suite until the test binary's own timeout.
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not wake on the committed request")
	}
}

// §5 case 7: an exact replay of a reply after a lost answer returns the same response, even once
// the request is answered; the same key with another body, or for another request, is
// key_reused; a second reply or a dismiss loses, told it was answered; the requester gets one
// response, and the human's delivery reaches retention.
func testAReplyWinsOnceAndReplaysExactly(t *testing.T, h Harness) {
	r := newRig(t, h)
	q := r.ask("s-jarvis", mail.KindRequest, "decide", 0)
	other := r.ask("s-gold", mail.KindRequest, "choose", 0)
	first, err := r.in.Reply(r.ctx, q.ID, "yes", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := r.in.Reply(r.ctx, q.ID, "yes", "r1"); err != nil || again.ID != first.ID {
		t.Fatalf("an exact replay: %+v, %v; want the same response %s", again, err, first.ID)
	}
	for name, c := range map[string][3]string{
		"another body":    {q.ID, "no", "r1"},
		"another request": {other.ID, "yes", "r1"},
	} {
		if _, err := r.in.Reply(r.ctx, c[0], c[1], c[2]); !errors.Is(err, mail.ErrKeyReused) {
			t.Errorf("the key with %s: %v, want ErrKeyReused", name, err)
		}
	}
	if _, err := r.in.Reply(r.ctx, q.ID, "no", "r2"); !errors.Is(err, mail.ErrAlreadyAnswered) {
		t.Fatalf("a second window's reply: %v, want ErrAlreadyAnswered", err)
	}
	if err := r.in.Dismiss(r.ctx, q.ID); !errors.Is(err, mail.ErrAlreadyAnswered) {
		t.Fatalf("a dismiss after the answer: %v, want ErrAlreadyAnswered", err)
	}
	if got := r.responses("p-jarvis"); !slices.Equal(got, []string{"yes"}) {
		t.Fatalf("jarvis's responses: %v, want the one answer", got)
	}
	var user mail.Delivery
	r.read(func(tx inbox.Tx) error {
		var err error
		user, _, err = tx.DeliveryOf(q.ID, mail.UserProfile)
		return err
	})
	if user.State != mail.Acked || user.DoneAt == nil {
		t.Fatalf("the human's delivery of the answered request: %+v, want acked with done_at (retention)", user)
	}
	if _, err := r.in.Reply(r.ctx, "nope", "x", "r3"); !errors.Is(err, mail.ErrNotARequest) {
		t.Fatalf("a reply to no message: %v, want ErrNotARequest", err)
	}
	rep := r.ask("s-gold", "", "report", 0)
	if _, err := r.in.Reply(r.ctx, rep.ID, "x", "r4"); !errors.Is(err, mail.ErrNotARequest) {
		t.Fatalf("a reply to a report: %v, want ErrNotARequest", err)
	}
}

// §5 case 7: a reply racing the request's expiry gets one committed outcome. Past the deadline,
// each of the human's operations is refused, and the refusal itself leaves the expiry real: the
// request ends expired and journaled (every window's toast ends), with no answer, and a snapshot no
// longer shows it. Each operation is checked on its own message, so one's expiry can't stand in for
// another's.
func testAReplyRacingExpiryGetsOneOutcome(t *testing.T, h Harness) {
	r := newRig(t, h)
	last := func() string {
		t.Helper()
		var ch []inbox.Change
		r.read(func(tx inbox.Tx) error { var err error; ch, err = tx.Changes(0, 0); return err })
		if len(ch) == 0 {
			return ""
		}
		return kinds(ch[len(ch)-1:])[0]
	}
	for _, c := range []struct {
		name string
		kind string
		op   func(id string) error
		want error
	}{
		{"a reply", mail.KindRequest, func(id string) error { _, err := r.in.Reply(r.ctx, id, "yes", "r-"+id); return err }, mail.ErrExpired},
		{"a dismiss", mail.KindRequest, func(id string) error { return r.in.Dismiss(r.ctx, id) }, mail.ErrExpired},
		{"an ack", "", func(id string) error { return r.in.Ack(r.ctx, id) }, mail.ErrExpired},
	} {
		msg := r.ask("s-jarvis", c.kind, "late "+c.name, time.Minute)
		r.clock.Advance(2 * time.Minute)
		if err := c.op(msg.ID); !errors.Is(err, c.want) {
			t.Fatalf("%s past the deadline: %v, want %v", c.name, err, c.want)
		}
		if got := last(); got != "expired:"+msg.ID {
			t.Fatalf("the journal's last change after %s was refused: %q, want the expiry, committed by the refusal", c.name, got)
		}
	}
	w := r.watch(0, 0)
	if w.Snapshot == nil || len(w.Snapshot.Items) != 0 {
		t.Fatalf("a snapshot after the expiries: %+v, want nothing open", w)
	}
	if got := r.responses("p-jarvis"); len(got) != 0 {
		t.Fatalf("jarvis's responses: %v, want none", got)
	}
}

// A dismiss ends the request and tells its sender (an event, why=dismissed), once: a replay
// answers ok and tells no one again.
func testADismissTellsTheSender(t *testing.T, h Harness) {
	r := newRig(t, h)
	q := r.ask("s-jarvis", mail.KindRequest, "decide", 0)
	for range 2 {
		if err := r.in.Dismiss(r.ctx, q.ID); err != nil {
			t.Fatalf("dismiss: %v", err)
		}
	}
	var events []string
	r.read(func(tx inbox.Tx) error {
		items, err := tx.ToUser("p-jarvis", 0, 0)
		for _, it := range items {
			if it.Message.Kind == mail.KindEvent {
				events = append(events, it.Message.Body+":"+it.Message.ReplyTo)
			}
		}
		return err
	})
	if !slices.Equal(events, []string{mail.WhyDismissed + ":" + q.ID}) {
		t.Fatalf("jarvis's notices: %v, want one dismissed notice", events)
	}
	if _, err := r.in.Reply(r.ctx, q.ID, "late", "r1"); !errors.Is(err, mail.ErrAlreadyAnswered) {
		t.Fatalf("a reply after the dismiss: %v, want ErrAlreadyAnswered", err)
	}
}

// A report is read once: its delivery is acked, the journal says read, a replay answers ok, and a
// snapshot no longer shows it; a request is never acked.
func testAReportIsReadOnce(t *testing.T, h Harness) {
	r := newRig(t, h)
	rep := r.ask("s-gold", "", "checkpoint", 0)
	q := r.ask("s-jarvis", mail.KindRequest, "decide", 0)
	for range 2 {
		if err := r.in.Ack(r.ctx, rep.ID); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}
	if err := r.in.Ack(r.ctx, q.ID); !errors.Is(err, mail.ErrInvalid) {
		t.Fatalf("an ack of a request: %v, want ErrInvalid", err)
	}
	w := r.watch(0, 0)
	if w.Snapshot == nil || !slices.Equal(messages(w.Snapshot.Items), []string{"decide"}) {
		t.Fatalf("the snapshot after the read: %+v, want the request alone", w)
	}
	var ch []inbox.Change
	r.read(func(tx inbox.Tx) error { var err error; ch, err = tx.Changes(2, 0); return err })
	if !slices.Equal(kinds(ch), []string{"read:" + rep.ID}) {
		t.Fatalf("the journal after the two acks: %v, want one read", kinds(ch))
	}
}

// The history is the human's deliveries, newest first, paged by seq; the undeliverable list is
// the generated notices nobody could receive, newest first. Both read the same on every engine.
func testTheHistoryAndUndeliverableNotices(t *testing.T, h Harness) {
	r := newRig(t, h)
	for _, s := range []string{"one", "two", "three"} {
		r.ask("s-gold", "", s, 0)
	}
	all, err := r.in.List(r.ctx, inbox.ListAll, 0, 50)
	if err != nil || !slices.Equal(messages(all), []string{"three", "two", "one"}) {
		t.Fatalf("the history: %v, %v; want newest first", messages(all), err)
	}
	page, err := r.in.List(r.ctx, inbox.ListAll, all[0].Delivery.Seq, 1)
	if err != nil || !slices.Equal(messages(page), []string{"two"}) {
		t.Fatalf("the page below %d: %v, %v; want two", all[0].Delivery.Seq, messages(page), err)
	}
	// two notices that failed, at different times, and one delivered: only the failed are listed
	r.write(func(tx inbox.Tx) error {
		for i, id := range []string{"ev1", "ev2", "ev3"} {
			at := r.clock.Now().Add(time.Duration(i) * time.Minute).UnixNano()
			if err := tx.PutMessage(mail.Message{ID: id, Sender: mail.DaemonProfile, IdemKey: id, Digest: []byte(id),
				Kind: mail.KindEvent, Subject: "expired", Body: "expired", State: mail.MessageOpen, Created: at}); err != nil {
				return err
			}
			d := mail.Delivery{ID: "d-" + id, Message: id, Recipient: "p-gone-" + id, Seq: 1, State: mail.Failed,
				Why: mail.WhyRecipientGone, DoneAt: &at}
			if id == "ev3" {
				d.Recipient, d.Seq, d.State, d.Why, d.DoneAt = "p-gold", 99, mail.Queued, "", nil
			}
			if err := tx.PutDelivery(d); err != nil {
				return err
			}
		}
		return nil
	})
	gone, err := r.in.List(r.ctx, inbox.ListUndeliverable, 0, 50)
	var ids []string
	for _, it := range gone {
		ids = append(ids, it.Delivery.ID)
	}
	if err != nil || !slices.Equal(ids, []string{"d-ev2", "d-ev1"}) {
		t.Fatalf("the undeliverable notices: %v, %v; want the two failed, newest first", ids, err)
	}
	if _, err := r.in.List(r.ctx, "bogus", 0, 1); !errors.Is(err, inbox.ErrInvalid) {
		t.Fatalf("an unknown filter: %v, want ErrInvalid", err)
	}
}

// Focus resolves the session's current owner when it is called: a live session's host; an ended
// session is not found. The owner's host hears a focus change.
func testFocusResolvesTheCurrentOwner(t *testing.T, h Harness) {
	r := newRig(t, h)
	f, err := r.in.Focus(r.ctx, "s-gold")
	if err != nil || f.Owner != "autovim:p" {
		t.Fatalf("focus gold: %+v, %v; want owner autovim:p", f, err)
	}
	var ch []inbox.Change
	r.read(func(tx inbox.Tx) error { var err error; ch, err = tx.Changes(0, 0); return err })
	if !slices.Equal(kinds(ch), []string{"focus:s-gold"}) {
		t.Fatalf("the journal: %v, want the focus", kinds(ch))
	}
	r.write(func(tx inbox.Tx) error {
		s, _, err := tx.Session("s-gold")
		if err != nil {
			return err
		}
		at := tx.Now().UnixNano()
		s.Ended = &at
		return tx.PutSession(s)
	})
	if _, err := r.in.Focus(r.ctx, "s-gold"); !errors.Is(err, inbox.ErrNotFound) {
		t.Fatalf("focus of an ended session: %v, want ErrNotFound", err)
	}
	if _, err := r.in.Focus(r.ctx, "s-none"); !errors.Is(err, inbox.ErrNotFound) {
		t.Fatalf("focus of no session: %v, want ErrNotFound", err)
	}
}

// EnsureReserved makes the daemon's and the human's profiles once; a second start changes
// nothing, and a store whose human already has a profile keeps it.
func testReservedProfilesAreMadeOnce(t *testing.T, h Harness) {
	r := newRig(t, h)
	if err := r.m.EnsureReserved(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.read(func(tx inbox.Tx) error {
		d, ok, err := tx.Profile(mail.DaemonProfile)
		if err != nil || !ok || d.Scope != mail.GlobalScope || d.Status != crew.ProfileActive {
			return fmt.Errorf("the daemon's profile: %+v, %v, %v", d, ok, err)
		}
		u, ok, err := tx.ActiveProfile(mail.GlobalScope, mail.UserCodename)
		if err != nil || !ok || u.ID != mail.UserProfile {
			return fmt.Errorf("the human's profile: %+v, %v, %v", u, ok, err)
		}
		return nil
	})
}
