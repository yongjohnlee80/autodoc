package crew_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	onCall func()        // runs inside each call, before it answers
}

func (f *fakeWaker) Wake(ctx context.Context, ep crew.Endpoint, notice string) error {
	f.mu.Lock()
	f.calls = append(f.calls, ep.Session)
	f.notes = append(f.notes, notice)
	block, inside, answer, onCall := f.block, f.inside, f.answer, f.onCall
	f.mu.Unlock()
	if onCall != nil {
		onCall()
	}
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

	fmu    sync.Mutex
	failed []string // each wake the scheduler told of as failed: "<session>: <reason>"
}

func newWakeRig(t *testing.T, run string) *wakeRig { return newWakeRigWith(t, run, nil) }

// newWakeRigWith is newWakeRig with more wakers, by kind, beside the fake one.
func newWakeRigWith(t *testing.T, run string, more map[string]crew.Waker) *wakeRig {
	r := &wakeRig{t: t, ctx: context.Background(), clock: crewtest.NewClock(), w: &fakeWaker{}}
	r.mem = crewtest.NewMem(r.clock.Now)
	r.m = crew.New(r.mem, crew.WithSessionEnder(&crewtest.Enders{}))
	wakers := map[string]crew.Waker{"fake": r.w}
	for k, w := range more {
		wakers[k] = w
	}
	r.s = r.m.NewScheduler(run, wakers, crew.WithWakeClock(r.clock.Now),
		crew.WithWakeErrors(func(err error) { t.Errorf("scheduler: %v", err) }),
		crew.WithWakeFailures(func(ep crew.Endpoint, err error) {
			r.fmu.Lock()
			defer r.fmu.Unlock()
			r.failed = append(r.failed, ep.Session+": "+err.Error())
		}))
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
	// the row keeps the count, never the reason: the scheduler tells of it, with the session's
	if r.fmu.Lock(); !slices.Equal(r.failed, []string{s.Session + ": refused"}) {
		t.Fatalf("the failure told: %q", r.failed)
	}
	r.fmu.Unlock()
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
	if r.fmu.Lock(); len(r.failed) != 2 {
		t.Fatalf("an accepted wake was told as failed, or a failure untold: %q", r.failed)
	}
	r.fmu.Unlock()
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
	tok, ep, ok, err := r.m.Reserve(r.ctx, s.Session, "run1", []string{"fake"})
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

// faultStore is a crew.Store whose nth write (counting from 1) fails once, as a store's transient
// error would.
type faultStore struct {
	crew.Store
	mu       sync.Mutex
	writes   int
	failAt   int
	failFrom int  // every write from this one on fails (0: none)
	down     bool // every write fails while set
}

var errInjected = errors.New("injected store fault")

func (f *faultStore) Write(ctx context.Context, fn func(crew.Tx) error) error {
	f.mu.Lock()
	f.writes++
	fail := f.writes == f.failAt || f.down || (f.failFrom > 0 && f.writes >= f.failFrom)
	f.mu.Unlock()
	if fail {
		return errInjected
	}
	return f.Store.Write(ctx, fn)
}

func (f *faultStore) failNext() { f.mu.Lock(); f.failAt = f.writes + 1; f.mu.Unlock() }

// within waits, in real time, for cond: Run's own timers drive these cells, never a poke of ours.
func within(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("%s never happened", what)
		case <-tick.C:
		}
	}
}

// The failure that makes a session unreachable journals it in the human feed, once, in the same
// write; a later failure adds nothing; a receive that makes it reachable again journals that.
func TestReachabilityIsJournaledOnLossAndOnReturn(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	r.w.answer = errors.New("refused")
	reach := func() int {
		n := 0
		for _, c := range r.mem.Inbox() {
			if c.Kind == crew.ChangeReachability && c.Ref == s.Session {
				n++
			}
		}
		return n
	}
	for i := 1; i <= 6; i++ {
		r.look()
		want := 0
		if i >= 5 {
			want = 1
		}
		if got := reach(); got != want {
			t.Fatalf("after %d failures: %d reachability changes, want %d", i, got, want)
		}
		r.clock.Advance(10 * time.Minute)
	}
	r.received(s.Session)
	if got := reach(); got != 2 {
		t.Fatalf("after the receive: %d reachability changes, want 2 (lost, then back)", got)
	}
}

// A refused outcome write is kept and written at the next look: Run's retry timer, with no poke,
// no send and no restart, takes the row out of in_flight.
func TestRunRecordsAnOutcomeTheStoreFirstRefused(t *testing.T) {
	clock := crewtest.NewClock()
	mem := crewtest.NewMem(clock.Now)
	fs := &faultStore{Store: mem}
	m := crew.New(fs, crew.WithSessionEnder(&crewtest.Enders{}))
	w := &fakeWaker{}
	w.onCall = fs.failNext // the call's own outcome write is the one refused
	r := &wakeRig{t: t, ctx: context.Background(), clock: clock, mem: mem, m: m, w: w}
	e, err := m.Claim(r.ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	r.epoch = e
	sess := r.session("gold")
	sched := m.NewScheduler("run1", map[string]crew.Waker{"fake": w}, crew.WithWakeClock(clock.Now), crew.WithWakeRetry(10*time.Millisecond))
	ctx, cancel := context.WithCancel(r.ctx)
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()
	within(t, "the outcome recorded after its refused write", func() bool { return r.wake(sess.Session).State == crew.WakeAwaiting })
	cancel()
	<-done
	if w.count() != 1 {
		t.Fatalf("%d calls, want 1: the owed outcome is written, not the wake sent again", w.count())
	}
}

// A look whose write the store refuses is retried by Run's timer: pending mail with no deadline
// is still woken, with no poke.
func TestRunLooksAgainAfterAStoreError(t *testing.T) {
	clock := crewtest.NewClock()
	mem := crewtest.NewMem(clock.Now)
	fs := &faultStore{Store: mem}
	m := crew.New(fs, crew.WithSessionEnder(&crewtest.Enders{}))
	w := &fakeWaker{}
	r := &wakeRig{t: t, ctx: context.Background(), clock: clock, mem: mem, m: m, w: w}
	e, err := m.Claim(r.ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	r.epoch = e
	r.session("gold")
	fs.mu.Lock()
	fs.failAt = fs.writes + 2 // Run's recovery is the next write; the first look's sweep, the one after
	fs.mu.Unlock()
	sched := m.NewScheduler("run1", map[string]crew.Waker{"fake": w}, crew.WithWakeClock(clock.Now), crew.WithWakeRetry(10*time.Millisecond))
	ctx, cancel := context.WithCancel(r.ctx)
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()
	within(t, "the wake after the refused look", func() bool { return w.count() == 1 })
	cancel()
	<-done
}

// Two attempts of one session can each owe an end: an old call that outlived an endpoint
// replacement, and the current one. Neither replaces the other, and paying them records the
// current attempt's end and takes nothing from the stale one.
func TestEachAttemptOwesItsOwnEnd(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	tok, _, ok, err := r.m.Reserve(r.ctx, s.Session, "run1", []string{"fake"})
	if err != nil || !ok {
		t.Fatalf("reserve: %v %v", ok, err)
	}
	r.s.Owe(s.Session, tok, crew.OutcomeAccepted)   // the current attempt, B
	r.s.Owe(s.Session, "stale", crew.OutcomeFailed) // the old call, A, after it
	if left := r.s.PayOwed(r.ctx); left {
		t.Fatal("ends still owed with a healthy store")
	}
	if w := r.wake(s.Session); w.State != crew.WakeAwaiting || w.Failures != 0 {
		t.Fatalf("the current attempt's end was not recorded: %+v", w)
	}
}

// While the store refuses the looks' writes, an overdue deadline never shortens the retry: Run's
// recovery succeeds, every sweep after it is refused with an awaiting_receive whose timeout has
// passed still readable, and Run backs off at the retry wait instead of spinning. Once the store
// is back, the wait is processed.
func TestRunBacksOffWhileTheStoreRefuses(t *testing.T) {
	clock := crewtest.NewClock()
	mem := crewtest.NewMem(clock.Now)
	fs := &faultStore{Store: mem}
	m := crew.New(fs, crew.WithSessionEnder(&crewtest.Enders{}))
	w := &fakeWaker{}
	r := &wakeRig{t: t, ctx: context.Background(), clock: clock, mem: mem, m: m, w: w}
	e, err := m.Claim(r.ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	r.epoch = e
	s := r.session("gold")
	past := clock.Now().Add(-time.Minute).UnixNano()
	r.write(func(tx crew.Tx) error { // an unanswered wake whose timeout has already passed
		wk, _, _ := tx.Wake(s.Session)
		wk.State, wk.Token, wk.NextAt = crew.WakeAwaiting, "t", &past
		return tx.PutWake(wk)
	})
	fs.mu.Lock()
	base := fs.writes
	fs.failFrom = base + 2 // Run's recovery (the next write) succeeds; every sweep after it fails
	fs.mu.Unlock()
	sched := m.NewScheduler("run1", map[string]crew.Waker{"fake": w}, crew.WithWakeClock(clock.Now), crew.WithWakeRetry(20*time.Millisecond))
	ctx, cancel := context.WithCancel(r.ctx)
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()
	defer func() { cancel(); <-done }()
	time.Sleep(300 * time.Millisecond) // a cell's own window, measuring how often Run tries
	fs.mu.Lock()
	tries := fs.writes - base - 1 // the sweeps refused after the recovery
	fs.mu.Unlock()
	still := r.wake(s.Session) // read while the store still refuses: nothing may have moved yet
	fs.mu.Lock()
	fs.failFrom = 0
	fs.mu.Unlock()
	if tries < 3 {
		t.Fatalf("%d refused sweeps in 300 ms: the failing look was not reached, or Run stopped retrying", tries)
	}
	if tries > 40 { // 300 ms at a 20 ms retry is about 15; a spin on the overdue deadline is thousands
		t.Fatalf("%d refused sweeps in 300 ms: Run spun on the overdue deadline", tries)
	}
	if still.State != crew.WakeAwaiting {
		t.Fatalf("the wait moved while every sweep was refused: %+v", still)
	}
	within(t, "the timed-out wait processed once the store is back", func() bool { return r.wake(s.Session).State != crew.WakeAwaiting })
}

// A session whose endpoint no waker serves is watch-only
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler, phase 3 brings the wakers one kind at a
// time): never reserved, never failed, never reported unreachable, while a served kind is woken.
// Its row recovered from an earlier run (next_at = now, past at once) never sets Run's timer
// either: a deadline on a row no look can act on would have Run look again at once, forever.
func TestAKindWithNoWakerIsWatchOnly(t *testing.T) {
	clock := crewtest.NewClock()
	mem := crewtest.NewMem(clock.Now)
	fs := &faultStore{Store: mem}
	m := crew.New(fs, crew.WithSessionEnder(&crewtest.Enders{}))
	w := &fakeWaker{}
	r := &wakeRig{t: t, ctx: context.Background(), clock: clock, mem: mem, m: m, w: w}
	e, err := m.Claim(r.ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	r.epoch = e
	gold, tifa := r.session("gold"), r.session("tifa")
	r.write(func(tx crew.Tx) error {
		return tx.PutEndpoint(crew.Endpoint{Session: tifa.Session, Generation: 1, Kind: "other"})
	})
	// idle and pending, due but for its kind: the reservation itself refuses it
	if _, _, ok, err := m.Reserve(r.ctx, tifa.Session, "run1", []string{"fake"}); err != nil || ok {
		t.Fatalf("reserving tifa, whose kind no waker serves: %v %v, want nothing reserved", ok, err)
	}
	r.write(func(tx crew.Tx) error {
		wk, _, err := tx.Wake(tifa.Session) // an attempt an earlier run left in flight
		if err != nil {
			return err
		}
		wk.State, wk.Token, wk.AttemptGen, wk.DaemonRun = crew.WakeInFlight, "t", wk.PendingGen, "run0"
		return tx.PutWake(wk)
	})
	sched := m.NewScheduler("run1", map[string]crew.Waker{"fake": w}, crew.WithWakeClock(clock.Now), crew.WithWakeRetry(20*time.Millisecond))
	ctx, cancel := context.WithCancel(r.ctx)
	done := make(chan error, 1)
	fs.mu.Lock()
	base := fs.writes
	fs.mu.Unlock()
	go func() { done <- sched.Run(ctx) }()
	defer func() { cancel(); <-done }()
	within(t, "gold, of a served kind, woken", func() bool { return w.count() == 1 })
	time.Sleep(300 * time.Millisecond) // a cell's own window, measuring how often Run looks
	fs.mu.Lock()
	writes := fs.writes - base
	fs.mu.Unlock()
	if writes > 40 { // the recovery, gold's attempt and a few looks; a spin on tifa's deadline is thousands
		t.Fatalf("%d writes in 300 ms: Run spun on a deadline of a row no look can act on", writes)
	}
	w.mu.Lock()
	calls := slices.Clone(w.calls)
	w.mu.Unlock()
	if len(calls) != 1 || calls[0] != gold.Session {
		t.Fatalf("calls %v, want gold's alone", calls)
	}
	if tw := r.wake(tifa.Session); tw.State != crew.WakeIdle || tw.Failures != 0 || tw.PendingGen <= tw.AnnouncedGen {
		t.Fatalf("tifa's row: %+v, want idle, pending, never failed (watch-only)", tw)
	}
}

// A waker found after the scheduler started (AddWaker) serves its kind from the next look: the
// kind's sessions, watch-only until then, are reserved and woken; nil adds nothing.
func TestAWakerAddedLaterServesItsKind(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	r.write(func(tx crew.Tx) error {
		return tx.PutEndpoint(crew.Endpoint{Session: s.Session, Generation: 1, Kind: "late"})
	})
	r.look()
	if r.w.count() != 0 {
		t.Fatalf("%d calls before a waker for its kind: want none (watch-only)", r.w.count())
	}
	r.s.AddWaker("late", nil)
	r.look()
	late := &fakeWaker{}
	r.s.AddWaker("late", late)
	r.look()
	if late.count() != 1 || r.w.count() != 0 {
		t.Fatalf("after AddWaker: %d calls on the late waker, %d on the other; want 1 and 0", late.count(), r.w.count())
	}
}

// Fire rings at once through the scheduler's own reservation; when the loop's attempt holds the row
// already, it waits for that attempt's end: accepted, fired; and a caller's bound that ends first
// is not fired. A failing doorbell is not fired, and backs off as the loop's would.
func TestFireRingsNowOrWaitsForTheLoopsAttempt(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	if fired, err := r.m.Fire(r.ctx, s.Profile); err != nil || !fired || r.w.count() != 1 {
		t.Fatalf("a due session: fired %v %v, %d calls", fired, err, r.w.count())
	}
	if w := r.wake(s.Session); w.State != crew.WakeAwaiting {
		t.Fatalf("after its fire: %+v, recorded as the loop's would be", w)
	}
	r.received(s.Session)

	// the loop's attempt holds the row: Fire waits for its end
	r.w.block, r.w.inside = make(chan struct{}), make(chan struct{}, 1)
	r.write(func(tx crew.Tx) error { return r.m.RaisePending(tx, s.Profile) })
	r.s.Look(r.ctx)
	<-r.w.inside
	result := make(chan bool, 1)
	go func() {
		fired, err := r.m.Fire(r.ctx, s.Profile)
		if err != nil {
			t.Error(err)
		}
		result <- fired
	}()
	select {
	case <-result:
		t.Fatal("Fire answered before the loop's attempt ended")
	case <-time.After(50 * time.Millisecond):
	}
	close(r.w.block)
	if !<-result || r.w.count() != 2 {
		t.Fatalf("Fire after the loop's accepted attempt: not fired, or %d calls", r.w.count())
	}
	r.s.Wait()
	r.received(s.Session)

	// a bound that ends while the loop's attempt is still in flight: not fired
	r.w.block = make(chan struct{})
	r.write(func(tx crew.Tx) error { return r.m.RaisePending(tx, s.Profile) })
	r.s.Look(r.ctx)
	<-r.w.inside
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Millisecond)
	if fired, err := r.m.Fire(ctx, s.Profile); fired || err != nil {
		t.Fatalf("a bound that ended first: fired %v %v", fired, err)
	}
	cancel()
	close(r.w.block)
	r.s.Wait()
	r.received(s.Session)

	// a failing doorbell: not fired, and backed off
	r.w.block, r.w.inside, r.w.answer = nil, nil, errors.New("refused")
	r.write(func(tx crew.Tx) error { return r.m.RaisePending(tx, s.Profile) })
	if fired, err := r.m.Fire(r.ctx, s.Profile); fired || err != nil {
		t.Fatalf("a failing doorbell: fired %v %v", fired, err)
	}
	if w := r.wake(s.Session); w.State != crew.WakeIdle || w.Failures != 1 || w.NextAt == nil {
		t.Fatalf("after the failed fire: %+v, want backed off", w)
	}
	// backing off: nothing rings until the backoff ends
	calls := r.w.count()
	if fired, _ := r.m.Fire(r.ctx, s.Profile); fired || r.w.count() != calls {
		t.Fatalf("a fire during the backoff rang (%d calls)", r.w.count())
	}
}

// entry is crew.list's row for codename.
func (r *wakeRig) entry(codename string) crew.Entry {
	r.t.Helper()
	es, err := r.m.List(r.ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, e := range es {
		if e.Codename == codename {
			return e
		}
	}
	r.t.Fatalf("crew.list has no %s: %+v", codename, es)
	return crew.Entry{}
}

// reachabilityChanges counts the human feed's reachability changes for session.
func (r *wakeRig) reachabilityChanges(session string) int {
	n := 0
	for _, c := range r.mem.Inbox() {
		if c.Kind == crew.ChangeReachability && c.Ref == session {
			n++
		}
	}
	return n
}

// TestATimeoutCountsTowardFive: a failure that may pass (a dial cut off by its deadline) counts once
// and backs off: still reachable, why saying when the next try is; the fifth makes the session
// unreachable, why naming that failure.
func TestATimeoutCountsTowardFive(t *testing.T) {
	r := newWakeRig(t, "run1")
	s := r.session("gold")
	r.w.answer = fmt.Errorf("crew: dialing the socket: %v", context.DeadlineExceeded)
	r.look()
	w := r.wake(s.Session)
	e := r.entry("gold")
	if w.Failures != 1 || !e.Reachable || e.Why != crew.WhyBackoffUntil+" "+time.Unix(0, *w.NextAt).UTC().Format(time.RFC3339) {
		t.Fatalf("after one timeout: %d failures, %+v; want 1, reachable, backing off", w.Failures, e)
	}
	for i := 2; i <= 5; i++ {
		r.clock.Advance(time.Hour)
		r.look()
	}
	w, e = r.wake(s.Session), r.entry("gold")
	if w.Failures != 5 || e.Reachable || e.Why != crew.WhyWakesFailing+": crew: dialing the socket: "+context.DeadlineExceeded.Error() {
		t.Fatalf("after five timeouts: %d failures, %+v; want 5, unreachable, why naming the timeout", w.Failures, e)
	}
}
