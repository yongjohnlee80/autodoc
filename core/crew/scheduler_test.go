package crew_test

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
)

// fakeWaker records each call and answers what answer says; block, when set, holds each call until
// it is closed.
type fakeWaker struct {
	mu     sync.Mutex
	calls  []string // the sessions it was asked to wake
	notes  []string
	answer error
	block  chan struct{}
	inside chan struct{} // one value per call that has started
}

func (f *fakeWaker) Wake(ctx context.Context, ep crew.Endpoint, notice string) error {
	f.mu.Lock()
	f.calls = append(f.calls, ep.Session)
	f.notes = append(f.notes, notice)
	block, inside, answer := f.block, f.inside, f.answer
	f.mu.Unlock()
	if inside != nil {
		inside <- struct{}{}
	}
	if block != nil {
		<-block
	}
	return answer
}

func (f *fakeWaker) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

type wakeRig struct {
	t     *testing.T
	ctx   context.Context
	clock *crewtest.Clock
	mem   *crewtest.Mem
	m     *crew.Manager
	w     *fakeWaker
	s     *crew.Scheduler
	epoch int64
}

func newWakeRig(t *testing.T, run string) *wakeRig {
	r := &wakeRig{t: t, ctx: context.Background(), clock: crewtest.NewClock(), w: &fakeWaker{}}
	r.mem = crewtest.NewMem(r.clock.Now)
	r.m = crew.New(r.mem, crew.WithSessionEnder(&crewtest.Enders{}))
	r.s = r.m.NewScheduler(run, map[string]crew.Waker{"fake": r.w}, crew.WithWakeClock(r.clock.Now),
		crew.WithWakeErrors(func(err error) { t.Errorf("scheduler: %v", err) }))
	e, err := r.m.Claim(r.ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	r.epoch = e
	return r
}

// session registers codename and gives it an endpoint and one pending generation.
func (r *wakeRig) session(codename string) crew.Registered {
	r.t.Helper()
	reg, err := r.m.Register(r.ctx, crew.Registration{Scope: "autodoc:w", Run: "host", Epoch: r.epoch, Codename: codename,
		Kind: "fake", Workspace: "w", Instance: "i-" + codename, Secret: strings.Repeat("s", 40)})
	if err != nil {
		r.t.Fatal(err)
	}
	r.write(func(tx crew.Tx) error {
		if err := tx.PutEndpoint(crew.Endpoint{Session: reg.Session, Generation: 1, Kind: "fake"}); err != nil {
			return err
		}
		return r.m.RaisePending(tx, reg.Profile)
	})
	return reg
}

func (r *wakeRig) write(fn func(crew.Tx) error) {
	r.t.Helper()
	if err := r.mem.Write(r.ctx, fn); err != nil {
		r.t.Fatal(err)
	}
}

func (r *wakeRig) wake(session string) crew.Wake {
	r.t.Helper()
	var w crew.Wake
	if err := r.mem.Read(r.ctx, func(tx crew.Tx) error { var err error; w, _, err = tx.Wake(session); return err }); err != nil {
		r.t.Fatal(err)
	}
	return w
}

func (r *wakeRig) look() { r.s.Look(r.ctx); r.s.Wait() }

func (r *wakeRig) received(session string) {
	r.write(func(tx crew.Tx) error { return r.m.Received(tx, session) })
}

// A due session is woken once, with the constant notice; an accepted notice waits for a receive,
// which clears it and its deadline. Nothing more is sent while nothing new is pending.
func TestAWakeIsSentOnceAndClearedByAReceive(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	r.look()
	if r.w.count() != 1 || r.w.notes[0] != crew.WakeNotice {
		t.Fatalf("the waker: %v %q", r.w.calls, r.w.notes)
	}
	w := r.wake(s.Session)
	if w.State != crew.WakeAwaiting || w.NextAt == nil || *w.NextAt != r.clock.Now().Add(2*time.Minute).UnixNano() {
		t.Fatalf("after an accepted notice: %+v", w)
	}
	r.look()
	if r.w.count() != 1 {
		t.Fatal("an awaiting session was woken again")
	}
	r.received(s.Session)
	if w := r.wake(s.Session); w.State != crew.WakeIdle || w.AnnouncedGen != w.PendingGen || w.NextAt != nil || w.Failures != 0 {
		t.Fatalf("after the receive: %+v", w)
	}
	r.look()
	if r.w.count() != 1 {
		t.Fatal("a session with nothing new pending was woken")
	}
}

// A failed call backs off (5 s, then 30 s); an unanswered notice times out after 2 minutes into a
// failure's backoff.
func TestFailuresBackOffAndAnUnansweredWakeTimesOut(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	r.w.answer = errors.New("refused")
	r.look()
	if w := r.wake(s.Session); w.State != crew.WakeIdle || w.Failures != 1 || *w.NextAt != r.clock.Now().Add(5*time.Second).UnixNano() {
		t.Fatalf("after one failure: %+v", w)
	}
	r.look()
	if r.w.count() != 1 {
		t.Fatal("retried before its backoff")
	}
	r.clock.Advance(5 * time.Second)
	r.look()
	if w := r.wake(s.Session); r.w.count() != 2 || w.Failures != 2 || *w.NextAt != r.clock.Now().Add(30*time.Second).UnixNano() {
		t.Fatalf("after two failures: %d calls, %+v", r.w.count(), w)
	}
	r.w.answer = nil
	r.clock.Advance(30 * time.Second)
	r.look() // accepted, then unanswered
	r.clock.Advance(2 * time.Minute)
	r.look()
	if w := r.wake(s.Session); w.State != crew.WakeIdle || w.Failures != 3 || *w.NextAt != r.clock.Now().Add(2*time.Minute).UnixNano() {
		t.Fatalf("after the unanswered wake: %+v", w)
	}
}

// No endpoint, no attempt: the host shows the mail instead. A receive during the call leaves the
// row done when the call returns, with no deadline and no failure.
func TestNoEndpointNoWakeAndAReceiveDuringTheCall(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	r.write(func(tx crew.Tx) error { return tx.DeleteEndpoint(s.Session) })
	r.look()
	if r.w.count() != 0 {
		t.Fatal("a session with no endpoint was woken")
	}
	r.write(func(tx crew.Tx) error {
		return tx.PutEndpoint(crew.Endpoint{Session: s.Session, Generation: 1, Kind: "fake"})
	})
	r.w.block, r.w.inside = make(chan struct{}), make(chan struct{}, 1)
	r.s.Look(r.ctx)
	<-r.w.inside
	r.received(s.Session)
	close(r.w.block)
	r.s.Wait()
	if w := r.wake(s.Session); w.State != crew.WakeIdle || w.NextAt != nil || w.Failures != 0 {
		t.Fatalf("a call that returned after a receive: %+v", w)
	}
}

// At most four calls are out at once, whatever the sessions due; a returning call lets the rest go.
func TestFourPermitsBoundTheCalls(t *testing.T) {
	r := newWakeRig(t, "run1")
	for i := range 6 {
		r.session(fmt.Sprint("a", i))
	}
	r.w.block, r.w.inside = make(chan struct{}), make(chan struct{}, 6)
	r.s.Look(r.ctx)
	for range 4 {
		<-r.w.inside
	}
	select {
	case <-r.w.inside:
		t.Fatal("a fifth call started while four were out")
	case <-time.After(50 * time.Millisecond):
	}
	close(r.w.block)
	r.s.Wait()
	r.look()
	if r.w.count() != 6 {
		t.Fatalf("%d calls, want 6", r.w.count())
	}
}

// A daemon start retries its predecessor's attempts at once; its own are left. A late outcome after
// a reset (a new endpoint) matches nothing.
func TestARestartRecoversAndALateOutcomeIsDropped(t *testing.T) {
	r := newWakeRig(t, "run2")
	s := r.session("gold")
	r.write(func(tx crew.Tx) error {
		w, _, _ := tx.Wake(s.Session)
		w.State, w.Token, w.DaemonRun = crew.WakeInFlight, "old", "run1"
		return tx.PutWake(w)
	})
	if err := r.m.RecoverRun(r.ctx, "run2"); err != nil {
		t.Fatal(err)
	}
	if w := r.wake(s.Session); w.State != crew.WakeIdle || w.Token != "" || w.NextAt == nil || *w.NextAt != r.clock.Now().UnixNano() {
		t.Fatalf("after recovery: %+v", w)
	}
	if err := r.m.Outcome(r.ctx, s.Session, "old", crew.OutcomeFailed); err != nil {
		t.Fatal(err)
	}
	if w := r.wake(s.Session); w.Failures != 0 || w.State != crew.WakeIdle {
		t.Fatalf("a late outcome changed the row: %+v", w)
	}
	r.look()
	if r.w.count() != 1 {
		t.Fatal("the recovered session was not woken at once")
	}
}

// An attempt whose session ended before its call is abandoned, and nothing is sent.
func TestAnEndedSessionsAttemptIsAbandoned(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	tok, ep, ok, err := r.m.Reserve(r.ctx, s.Session, "run1")
	if err != nil || !ok {
		t.Fatalf("reserve: %v %v", ok, err)
	}
	if err := r.m.Exit(r.ctx, "autodoc:w", "host", r.epoch, s.Session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	if cur, err := r.m.StillCurrent(r.ctx, s.Session, tok, ep.Generation); err != nil || cur {
		t.Fatalf("still current after the exit: %v %v", cur, err)
	}
}
