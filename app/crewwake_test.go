package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/todo/sqlitejournal"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// The wired daemon's wake path, end to end (ADR 1793271500 §5 cases 1 and 2): serve's modules on a
// temp SQLite store whose clock the test moves, the scheduler serve starts (startScheduler) with a
// fake waker, and agents that register, set an endpoint, receive and ack as the guide says.

// fakeWaker records each wake by session, fails or blocks on demand, and counts the calls out at
// once.
type fakeWaker struct {
	mu       sync.Mutex
	fail     int           // the next calls to fail
	block    chan struct{} // when set, each call waits for it (or its ctx)
	out, max int
	woke     chan string
}

func newFakeWaker() *fakeWaker { return &fakeWaker{woke: make(chan string, 1024)} }

func (w *fakeWaker) Wake(ctx context.Context, ep crew.Endpoint, notice string) error {
	w.mu.Lock()
	w.out++
	w.max = max(w.max, w.out)
	fail, block := w.fail > 0, w.block
	if fail {
		w.fail--
	}
	w.mu.Unlock()
	defer func() { w.mu.Lock(); w.out--; w.mu.Unlock() }()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if notice != crew.WakeNotice {
		return fmt.Errorf("a notice that isn't the constant one: %q", notice)
	}
	w.woke <- ep.Session
	if fail {
		return errors.New("the CLI refused the notice")
	}
	return nil
}

type wakeRig struct {
	t     *testing.T
	ctx   context.Context
	clock *crewtest.Clock
	deps  *moduleDeps
	st    *sqlitecrewmail.Store // a reader of the same store, for assertions
	waker *fakeWaker
	epoch int64
	n     int
}

func newWakeRig(t *testing.T) *wakeRig {
	t.Helper()
	r := &wakeRig{t: t, ctx: context.Background(), clock: crewtest.NewClock(), waker: newFakeWaker()}
	db, err := store.Open(r.ctx, filepath.Join(t.TempDir(), "autodoc.db"), store.WithClock(r.clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r.deps = &moduleDeps{store: db, storeID: "store-1", workspaces: rpc.Fixed(), journal: sqlitejournal.New(db), log: logger.Nop{}}
	modules(r.deps)
	if err := r.deps.mail.EnsureReserved(r.ctx); err != nil {
		t.Fatal(err)
	}
	r.st = sqlitecrewmail.New(db)
	if r.epoch, err = r.deps.crew.Claim(r.ctx, "autodoc:aaaa", "run-a", ""); err != nil {
		t.Fatal(err)
	}
	return r
}

// start starts serve's scheduler with wakers (none: as serve does before phase 3), on the store's
// clock; the answer stops it.
func (r *wakeRig) start(wakers map[string]crew.Waker) (stop func()) {
	stop = startScheduler(r.ctx, r.deps.crew, wakers, logger.Nop{}, crew.WithWakeClock(r.clock.Now),
		crew.WithWakeRetry(10*time.Millisecond))
	r.t.Cleanup(stop)
	return stop
}

func (r *wakeRig) withWaker() map[string]crew.Waker { return map[string]crew.Waker{"claude": r.waker} }

// agent registers codename with a Claude endpoint, as a host and the CLI's hook do; it answers the
// session and its credential.
func (r *wakeRig) agent(codename string) (string, string) {
	r.t.Helper()
	r.n++
	secret := strings.Repeat("s", 32) + codename
	reg, err := r.deps.crew.Register(r.ctx, crew.Registration{Scope: "autodoc:aaaa", Run: "run-a", Epoch: r.epoch,
		Codename: codename, Kind: "claude", Workspace: "w", Instance: fmt.Sprintf("agent-%d", r.n), Secret: secret})
	if err != nil {
		r.t.Fatal(err)
	}
	cred := crew.Credential(reg.Session, secret)
	pid := 100 + r.n // start is the pid's process reference, as registration requires
	loc := fmt.Sprintf(`{"socket":"/run/user/%d/cc-socks/%s.sock","pid":%d,"start":"b:n:%d:9000","session":"c-%s"}`,
		os.Getuid(), codename, pid, pid, codename)
	if _, err := r.deps.crew.SetEndpoint(r.ctx, cred, "claude", loc); err != nil {
		r.t.Fatal(err)
	}
	return reg.Session, cred
}

func (r *wakeRig) send(from, to, body string) {
	r.t.Helper()
	if _, err := r.deps.mail.Send(r.ctx, from, mail.Send{To: []string{to}, Body: body, Key: "k-" + body}); err != nil {
		r.t.Fatal(err)
	}
}

// woken waits for the waker to be called for session, and fails after a while.
func (r *wakeRig) woken(session string) {
	r.t.Helper()
	// NOTE: a bound on a failing run only: a wake that never comes fails here rather than hanging.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case s := <-r.waker.woke:
			if s == session {
				return
			}
		case <-deadline:
			r.t.Fatalf("no wake reached session %s", session)
		}
	}
}

// drain receives and acks until more is false, as the guide says, and answers the bodies.
func (r *wakeRig) drain(cred string) []string {
	r.t.Helper()
	var bodies []string
	for {
		got, err := r.deps.mail.Receive(r.ctx, cred)
		if err != nil {
			r.t.Fatal(err)
		}
		for _, it := range got.Items {
			bodies = append(bodies, it.Message.Body)
			if err := r.deps.mail.Ack(r.ctx, cred, it.Delivery.ID, it.Delivery.Lease); err != nil {
				r.t.Fatal(err)
			}
		}
		if !got.More {
			return bodies
		}
	}
}

func (r *wakeRig) wake(session string) crew.Wake {
	r.t.Helper()
	var w crew.Wake
	if err := r.st.Read(r.ctx, func(tx crew.Tx) error { var err error; w, _, err = tx.Wake(session); return err }); err != nil {
		r.t.Fatal(err)
	}
	return w
}

// settled waits until session's wake row satisfies ok: an attempt's end is written after its call
// returns, so a test that moves the clock between the two would start the backoff from the moved
// clock.
func (r *wakeRig) settled(session string, ok func(crew.Wake) bool) {
	r.t.Helper()
	// NOTE: a bound on a failing run only.
	for deadline := time.Now().Add(10 * time.Second); !ok(r.wake(session)); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			r.t.Fatalf("session %s's wake row never settled: %+v", session, r.wake(session))
		}
	}
}

// advance moves the store's clock and pokes the scheduler, whose timers run on real time.
func (r *wakeRig) advance(d time.Duration) {
	r.clock.Advance(d)
	r.deps.crew.Poke()
}

// TestNoMailIsStranded (§5 case 1): each way mail could wait unseen, with no keypress and no
// unrelated send, ends with the scheduler reaching the agent, which drains to more=false.
func TestNoMailIsStranded(t *testing.T) {
	t.Run("commit, then a crash before the wake", func(t *testing.T) {
		r := newWakeRig(t)
		_, jarvis := r.agent("jarvis")
		gold, goldCred := r.agent("gold")
		r.send(jarvis, "gold", "sent before the daemon's scheduler ran")
		r.start(r.withWaker()) // the restarted daemon's first look finds it
		r.woken(gold)
		if got := r.drain(goldCred); len(got) != 1 {
			t.Fatalf("gold drained %v", got)
		}
	})
	t.Run("the only wake fails, then retries", func(t *testing.T) {
		r := newWakeRig(t)
		r.waker.fail = 1
		r.start(r.withWaker())
		_, jarvis := r.agent("jarvis")
		gold, goldCred := r.agent("gold")
		r.send(jarvis, "gold", "one")
		r.woken(gold) // the failed attempt
		r.settled(gold, func(w crew.Wake) bool { return w.State == crew.WakeIdle && w.Failures == 1 })
		r.advance(6 * time.Second)
		r.woken(gold) // its retry, after the first backoff
		if got := r.drain(goldCred); len(got) != 1 {
			t.Fatalf("gold drained %v", got)
		}
	})
	t.Run("an accepted wake never answered, across a daemon restart", func(t *testing.T) {
		r := newWakeRig(t)
		stop := r.start(r.withWaker())
		_, jarvis := r.agent("jarvis")
		gold, goldCred := r.agent("gold")
		r.send(jarvis, "gold", "one")
		r.woken(gold) // accepted; gold never receives
		r.settled(gold, func(w crew.Wake) bool { return w.State == crew.WakeAwaiting })
		stop() // the daemon restarts while everyone is idle
		r.start(r.withWaker())
		r.advance(2*time.Minute + time.Second) // the unanswered wake times out
		r.settled(gold, func(w crew.Wake) bool { return w.State == crew.WakeIdle && w.Failures == 1 })
		r.advance(6 * time.Second) // and its backoff passes
		r.woken(gold)
		if got := r.drain(goldCred); len(got) != 1 {
			t.Fatalf("gold drained %v", got)
		}
	})
	t.Run("a received delivery abandoned until its lease lapses", func(t *testing.T) {
		r := newWakeRig(t)
		r.start(r.withWaker())
		_, jarvis := r.agent("jarvis")
		gold, goldCred := r.agent("gold")
		r.send(jarvis, "gold", "one")
		r.woken(gold)
		if got, err := r.deps.mail.Receive(r.ctx, goldCred); err != nil || len(got.Items) != 1 {
			t.Fatalf("receive: %+v, %v", got, err)
		} // and never acked
		r.advance(mail.LeaseFor + time.Second) // the lease lapses: the delivery is queued again
		r.woken(gold)
		if got := r.drain(goldCred); len(got) != 1 {
			t.Fatalf("gold drained %v after the lapse", got)
		}
	})
	t.Run("two racing sends", func(t *testing.T) {
		r := newWakeRig(t)
		r.start(r.withWaker())
		_, jarvis := r.agent("jarvis")
		_, vision := r.agent("vision")
		gold, goldCred := r.agent("gold")
		var wg sync.WaitGroup
		for _, from := range []string{jarvis, vision} {
			wg.Add(1)
			go func() { defer wg.Done(); r.send(from, "gold", "from "+from[:8]) }()
		}
		wg.Wait()
		r.woken(gold)
		if got := r.drain(goldCred); len(got) != 2 {
			t.Fatalf("gold drained %v, want both", got)
		}
	})
	t.Run("a burst of twenty", func(t *testing.T) {
		r := newWakeRig(t)
		r.start(r.withWaker())
		_, jarvis := r.agent("jarvis")
		gold, goldCred := r.agent("gold")
		for i := range 20 {
			r.send(jarvis, "gold", fmt.Sprintf("burst-%02d", i))
		}
		r.woken(gold)
		if got := r.drain(goldCred); len(got) != 20 {
			t.Fatalf("gold drained %d, want all 20", len(got))
		}
	})
}

// TestFourCallsAtMostWithFiftyDue (§5 case 2): with fifty sessions due at once and every call
// held, no more than four calls run; released, every session is woken.
func TestFourCallsAtMostWithFiftyDue(t *testing.T) {
	r := newWakeRig(t)
	r.waker.block = make(chan struct{})
	_, jarvis := r.agent("jarvis")
	var sessions []string
	for i := range 50 {
		s, _ := r.agent(fmt.Sprintf("a%02d", i))
		sessions = append(sessions, s)
		r.send(jarvis, fmt.Sprintf("a%02d", i), fmt.Sprintf("m%02d", i))
	}
	r.start(r.withWaker())
	// NOTE: a bound on a failing run only, while the first calls arrive and hold their permits.
	deadline := time.Now().Add(10 * time.Second)
	for {
		r.waker.mu.Lock()
		out := r.waker.out
		r.waker.mu.Unlock()
		if out == 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.deps.crew.Poke() // another look while all four permits are held
	time.Sleep(100 * time.Millisecond)
	r.waker.mu.Lock()
	most := r.waker.max
	r.waker.mu.Unlock()
	if most != 4 {
		t.Fatalf("%d calls out at once, want exactly 4", most)
	}
	close(r.waker.block)
	seen := map[string]bool{}
	// NOTE: a bound on a failing run only.
	timeout := time.After(20 * time.Second)
	for len(seen) < 50 {
		select {
		case s := <-r.waker.woke:
			seen[s] = true
		case <-timeout:
			t.Fatalf("%d of 50 sessions woken", len(seen))
		}
	}
}

// TestAWaitHearsASendWhileAWakeIsOut: a send to a session whose wake attempt is still out raises
// pending_gen on an in_flight row, which leaves no row due; the session's mail.wait must still hear
// it, at once, not at its timeout.
func TestAWaitHearsASendWhileAWakeIsOut(t *testing.T) {
	r := newWakeRig(t)
	r.waker.block = make(chan struct{})
	t.Cleanup(func() { close(r.waker.block) })
	r.start(r.withWaker())
	_, jarvis := r.agent("jarvis")
	gold, goldCred := r.agent("gold")
	r.send(jarvis, "gold", "one")
	// NOTE: a bound on a failing run only, while the attempt reaches the blocked waker.
	for deadline := time.Now().Add(10 * time.Second); r.wake(gold).State != crew.WakeInFlight; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the wake attempt never went out")
		}
	}
	if got := r.drain(goldCred); len(got) != 1 { // gold reads its mail by itself; the call is still out
		t.Fatalf("gold drained %v", got)
	}
	got := make(chan mail.Ready, 1)
	go func() {
		w, err := r.deps.mail.Wait(r.ctx, goldCred, 5*time.Second)
		if err != nil {
			t.Error(err)
		}
		got <- w
	}()
	time.Sleep(50 * time.Millisecond) // let the wait register and find nothing
	r.send(jarvis, "gold", "two")
	if w := <-got; !w.Ready {
		t.Fatalf("gold's mail.wait: %+v, want ready on the send, not a timeout", w)
	}
}

// TestNoWakerNoAttemptButTheWaitsWork: as serve runs until phase 3 brings wakers, the scheduler
// never starts its loop: a session with an endpoint and mail waiting gets no attempt and no
// reachability change, however long it waits, while its mail.wait and its host's crew.wait still
// answer ready.
func TestNoWakerNoAttemptButTheWaitsWork(t *testing.T) {
	r := newWakeRig(t)
	r.start(nil)
	_, jarvis := r.agent("jarvis")
	gold, goldCred := r.agent("gold")
	got := make(chan mail.Ready, 1)
	go func() {
		w, err := r.deps.mail.Wait(r.ctx, goldCred, 5*time.Second)
		if err != nil {
			t.Error(err)
		}
		got <- w
	}()
	time.Sleep(50 * time.Millisecond)
	r.send(jarvis, "gold", "one")
	if w := <-got; !w.Ready {
		t.Fatalf("gold's mail.wait with no waker: %+v, want ready", w)
	}
	cw, err := r.deps.crew.Wait(r.ctx, "autodoc:aaaa", "run-a", r.epoch, []string{gold}, time.Second)
	if err != nil || len(cw.Ready) != 1 || cw.Ready[0].Session != gold || cw.Ready[0].Unhandled != 1 {
		t.Fatalf("the host's crew.wait: %+v, %v; want gold ready with 1 unhandled", cw, err)
	}
	// A running loop reserves an attempt within milliseconds of a poke (it stamps the row's
	// daemon_run and attempt_gen). Poke for half a second, past backoffs, and the row stays untouched.
	for range 10 {
		r.advance(10 * time.Minute)
		time.Sleep(50 * time.Millisecond)
	}
	if w := r.wake(gold); w.State != crew.WakeIdle || w.DaemonRun != "" || w.AttemptGen != 0 || w.Failures != 0 {
		t.Fatalf("gold's wake row with no waker: %+v, want never reserved (no daemon_run, attempt_gen 0)", w)
	}
	var ch []inbox.Change
	if err := r.st.Inbox().Read(r.ctx, func(tx inbox.Tx) error { var err error; ch, err = tx.Changes(0, 0); return err }); err != nil {
		t.Fatal(err)
	}
	for _, c := range ch {
		if c.Kind == crew.ChangeReachability {
			t.Fatalf("a reachability change with no waker: %+v", c)
		}
	}
}

// TestCrewWaitAnswersTheHostsReadySessions: crew.wait names only the host's own live sessions that
// have mail, with their unhandled count; a fenced epoch is refused; with nothing waiting it times
// out.
func TestCrewWaitAnswersTheHostsReadySessions(t *testing.T) {
	r := newWakeRig(t)
	r.start(nil)
	_, jarvis := r.agent("jarvis")
	gold, _ := r.agent("gold")
	vision, _ := r.agent("vision")
	if w, err := r.deps.crew.Wait(r.ctx, "autodoc:aaaa", "run-a", r.epoch, []string{gold, vision}, 50*time.Millisecond); err != nil || !w.TimedOut || len(w.Ready) != 0 {
		t.Fatalf("crew.wait with nothing waiting: %+v, %v; want timed out", w, err)
	}
	r.send(jarvis, "gold", "one")
	r.send(jarvis, "gold", "two")
	w, err := r.deps.crew.Wait(r.ctx, "autodoc:aaaa", "run-a", r.epoch, []string{gold, vision, "not-a-session"}, time.Second)
	if err != nil || len(w.Ready) != 1 || w.Ready[0] != (crew.Pending{Session: gold, Unhandled: 2}) {
		t.Fatalf("crew.wait: %+v, %v; want gold alone, 2 unhandled", w, err)
	}
	if _, err := r.deps.crew.Wait(r.ctx, "autodoc:aaaa", "run-a", r.epoch+1, []string{gold}, time.Second); !errors.Is(err, crew.ErrStaleEpoch) {
		t.Fatalf("crew.wait under another epoch: %v, want ErrStaleEpoch", err)
	}
}

// TestCrewWaitHearsMailBoundToTheSession: mail sent to session:<id> is the session's alone, and
// crew.wait answers for it, as the session's mail.wait does, and again once a lease of it lapses.
// The host waits on its sessions, not on what a new session of the codename would get.
func TestCrewWaitHearsMailBoundToTheSession(t *testing.T) {
	r := newWakeRig(t)
	r.start(nil)
	_, jarvis := r.agent("jarvis")
	gold, goldCred := r.agent("gold")
	got := make(chan crew.Waited, 1)
	go func() {
		w, err := r.deps.crew.Wait(r.ctx, "autodoc:aaaa", "run-a", r.epoch, []string{gold}, 5*time.Second)
		if err != nil {
			t.Error(err)
		}
		got <- w
	}()
	time.Sleep(50 * time.Millisecond) // the wait is registered and found nothing
	if _, err := r.deps.mail.Send(r.ctx, jarvis, mail.Send{To: []string{"session:" + gold}, Body: "for this session", Key: "bound"}); err != nil {
		t.Fatal(err)
	}
	if w := <-got; len(w.Ready) != 1 || w.Ready[0].Session != gold {
		t.Fatalf("crew.wait after a send to session:%s: %+v, want gold ready, not a timeout", gold, w)
	}
	if rec, err := r.deps.mail.Receive(r.ctx, goldCred); err != nil || len(rec.Items) != 1 {
		t.Fatalf("gold's receive: %+v, %v", rec, err)
	} // leased, and never acked
	if w, err := r.deps.crew.Wait(r.ctx, "autodoc:aaaa", "run-a", r.epoch, []string{gold}, 50*time.Millisecond); err != nil || !w.TimedOut {
		t.Fatalf("crew.wait while the bound delivery is leased: %+v, %v; want timed out", w, err)
	}
	r.clock.Advance(mail.LeaseFor + time.Second)
	if w, err := r.deps.crew.Wait(r.ctx, "autodoc:aaaa", "run-a", r.epoch, []string{gold}, time.Second); err != nil || len(w.Ready) != 1 {
		t.Fatalf("crew.wait after the bound lease lapsed: %+v, %v; want gold ready", w, err)
	}
}
