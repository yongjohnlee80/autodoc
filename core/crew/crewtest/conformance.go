package crewtest

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// Harness makes the Store a conformance run tests: New returns an empty one whose transactions
// read the time from now.
type Harness struct {
	New func(t *testing.T, now func() time.Time) crew.Store
}

// Clock is a test clock: Now, and Advance to move it.
type Clock struct {
	mu sync.Mutex
	at time.Time
}

// NewClock starts a clock at a fixed instant.
func NewClock() *Clock { return &Clock{at: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)} }

// Now is the clock's time.
func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.at }

// Advance moves the clock on by d.
func (c *Clock) Advance(d time.Duration) { c.mu.Lock(); c.at = c.at.Add(d); c.mu.Unlock() }

// Enders records the sessions the crew module ended, as the mail module would hear of them.
type Enders struct {
	mu          sync.Mutex
	Ended       []EndedSession
	Merged      [][2]string     // (from, into) profiles
	MergedSeen  []crew.Profile  // from's row when MergeDeliveries ran
	Discarded   []string        // profiles
	DiscardSeen []string        // the profile's scope state when DiscardDeliveries ran
	Pending     map[string]bool // profiles with eligible mail, for HasEligible
	Unhandled   map[string]int  // profiles' unhandled deliveries, for UnhandledCount
}

// EndedSession is one call of EndSessionDeliveries.
type EndedSession struct{ Session, Why string }

// EndSessionDeliveries records the call (crew.SessionEnder).
func (e *Enders) EndSessionDeliveries(_ crew.Tx, s crew.Session, why string) error {
	e.mu.Lock()
	e.Ended = append(e.Ended, EndedSession{s.ID, why})
	e.mu.Unlock()
	return nil
}

// MergeDeliveries records adoption's mail half (crew.SessionEnder), and from's profile row as the
// mail module would read it then: the forwarding must already be in place.
func (e *Enders) MergeDeliveries(tx crew.Tx, from, into string) error {
	p, _, err := tx.Profile(from)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.Merged = append(e.Merged, [2]string{from, into})
	e.MergedSeen = append(e.MergedSeen, p)
	e.mu.Unlock()
	return nil
}

// Expire does nothing: this fake mail module has no time-driven state (crew.SessionEnder).
func (e *Enders) Expire(crew.Tx) error { return nil }

// NextDeadline has none (crew.SessionEnder).
func (e *Enders) NextDeadline(crew.Tx) (int64, bool, error) { return 0, false, nil }

// UnhandledCount reports the counts Unhandled holds (crew.SessionEnder).
func (e *Enders) UnhandledCount(_ crew.Tx, profile string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Unhandled[profile], nil
}

// HasEligible reports the profiles Pending names (crew.SessionEnder).
func (e *Enders) HasEligible(_ crew.Tx, profile string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Pending[profile], nil
}

// DiscardDeliveries records discard's mail half (crew.SessionEnder), and the profile's scope state
// as the mail module would read it then: the scope must already be gone.
func (e *Enders) DiscardDeliveries(tx crew.Tx, profile string) error {
	p, _, err := tx.Profile(profile)
	if err != nil {
		return err
	}
	sc, _, err := tx.Scope(p.Scope)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.Discarded = append(e.Discarded, profile)
	e.DiscardSeen = append(e.DiscardSeen, sc.State)
	e.mu.Unlock()
	return nil
}

// Run is the crew module's conformance suite, over the Store h makes: the ownership state machine
// of ADR 1793271500 §4.3, which every engine's adapter must keep.
func Run(t *testing.T, h Harness) {
	ctx := context.Background()
	setup := func(t *testing.T) (*crew.Manager, crew.Store, *Clock, *Enders) {
		clock, enders := NewClock(), &Enders{}
		store := h.New(t, clock.Now)
		return crew.New(store, crew.WithSessionEnder(enders)), store, clock, enders
	}
	const scope, proc = "autovim:plugins", "" // proc "": unknown, so the lease alone decides

	t.Run("the wake queries", func(t *testing.T) {
		_, store, clock, _ := setup(t)
		now := clock.Now().UnixNano()
		at := func(d time.Duration) *int64 { v := now + int64(d); return &v }
		ended := now
		rows := []struct {
			w        crew.Wake
			ended    bool
			endpoint bool
		}{
			{crew.Wake{Session: "s1", PendingGen: 2, AnnouncedGen: 1, State: crew.WakeIdle}, false, true},                          // due
			{crew.Wake{Session: "s2", PendingGen: 1, AnnouncedGen: 1, State: crew.WakeIdle}, false, true},                          // nothing pending
			{crew.Wake{Session: "s3", PendingGen: 2, State: crew.WakeIdle, NextAt: at(10 * time.Second)}, false, true},             // backing off
			{crew.Wake{Session: "s4", PendingGen: 2, State: crew.WakeIdle}, true, true},                                            // ended
			{crew.Wake{Session: "s5", PendingGen: 2, State: crew.WakeIdle}, false, false},                                          // no endpoint
			{crew.Wake{Session: "s6", PendingGen: 1, State: crew.WakeAwaiting, Token: "t", NextAt: at(-time.Second)}, false, true}, // timed out
			{crew.Wake{Session: "s7", PendingGen: 1, State: crew.WakeInFlight, Token: "t"}, false, true},                           // a call out
			{crew.Wake{Session: "s8", PendingGen: 1, State: crew.WakeAwaiting, Token: "t", NextAt: at(time.Minute)}, false, true},  // waiting
		}
		if err := store.Write(ctx, func(tx crew.Tx) error {
			for _, r := range rows {
				s := crew.Session{ID: r.w.Session, Profile: "p-" + r.w.Session, Host: scope, Instance: r.w.Session,
					SecretHash: []byte("h"), ArgsDigest: []byte("d")}
				if r.ended {
					s.Ended, s.EndedWhy = &ended, crew.EndedExit
				}
				if err := tx.PutSession(s); err != nil {
					return err
				}
				if r.endpoint {
					if err := tx.PutEndpoint(crew.Endpoint{Session: r.w.Session, Generation: 1, Kind: "fake"}); err != nil {
						return err
					}
				}
				if err := tx.PutWake(r.w); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		ids := func(ws []crew.Wake) string {
			var out []string
			for _, w := range ws {
				out = append(out, w.Session)
			}
			return strings.Join(out, ",")
		}
		if err := store.Read(ctx, func(tx crew.Tx) error {
			due, err := tx.WakesDue(now, 64)
			if err != nil || ids(due) != "s1" {
				t.Errorf("WakesDue: %s %v, want s1 (not ended s4, not endpoint-less s5)", ids(due), err)
			}
			if out, err := tx.WakesTimedOut(now, 64); err != nil || ids(out) != "s6" {
				t.Errorf("WakesTimedOut: %s %v, want s6", ids(out), err)
			}
			if out, err := tx.WakesInFlight(); err != nil || ids(out) != "s7" {
				t.Errorf("WakesInFlight: %s %v, want s7", ids(out), err)
			}
			if next, ok, err := tx.NextWakeAt(); err != nil || !ok || next != *at(-time.Second) {
				t.Errorf("NextWakeAt: %d %v %v, want s6's deadline", next, ok, err)
			}
			if _, err := tx.WakesDue(now, 0); err == nil {
				t.Error("WakesDue with limit 0 answered, rather than refusing")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("claim, reattach, busy, and a lapsed lease", func(t *testing.T) {
		m, _, clock, _ := setup(t)
		e1, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil || e1 != 1 {
			t.Fatalf("first claim: epoch %d, %v; want 1", e1, err)
		}
		if e, err := m.Claim(ctx, scope, "run-a", proc); err != nil || e != e1 {
			t.Fatalf("the holder's own claim again (a reattach): epoch %d, %v; want %d", e, err, e1)
		}
		if _, err := m.Claim(ctx, scope, "run-b", proc); !errors.Is(err, crew.ErrScopeBusy) {
			t.Fatalf("another run while the lease holds: %v, want ErrScopeBusy", err)
		}
		clock.Advance(89 * time.Second)
		if _, err := m.Claim(ctx, scope, "run-b", proc); !errors.Is(err, crew.ErrScopeBusy) {
			t.Fatalf("another run at 89 s: %v, want ErrScopeBusy (the lease is 90 s)", err)
		}
		clock.Advance(2 * time.Second)
		e2, err := m.Claim(ctx, scope, "run-b", proc)
		if err != nil || e2 != e1+1 {
			t.Fatalf("another run after the lease lapsed: epoch %d, %v; want %d", e2, err, e1+1)
		}
		if err := m.Release(ctx, scope, "run-a", e1); !errors.Is(err, crew.ErrStaleEpoch) {
			t.Fatalf("the fenced owner's release: %v, want ErrStaleEpoch", err)
		}
	})

	t.Run("a reattach renews the lease", func(t *testing.T) {
		m, _, clock, _ := setup(t)
		if _, err := m.Claim(ctx, scope, "run-a", proc); err != nil {
			t.Fatal(err)
		}
		clock.Advance(60 * time.Second)
		if _, err := m.Claim(ctx, scope, "run-a", proc); err != nil {
			t.Fatal(err)
		}
		clock.Advance(60 * time.Second) // 120 s since the claim, 60 s since the reattach
		if _, err := m.Claim(ctx, scope, "run-b", proc); !errors.Is(err, crew.ErrScopeBusy) {
			t.Fatalf("60 s after a reattach: %v, want ErrScopeBusy", err)
		}
	})

	t.Run("a reattach needs the same process", func(t *testing.T) {
		m, _, _, _ := setup(t)
		live, err := crew.ProcRef(os.Getpid()) // this test's own process: alive, so its lease alone isn't the answer
		if err != nil {
			t.Skipf("no process references here: %v", err)
		}
		if _, err := m.Claim(ctx, scope, "run-a", live); err != nil {
			t.Fatal(err)
		}
		// the same run token from another process tuple is not a reattach, and the holder is live
		other := strings.Replace(live, ":"+strconv.Itoa(os.Getpid())+":", ":"+strconv.Itoa(os.Getpid()+1)+":", 1)
		if _, err := m.Claim(ctx, scope, "run-a", other); !errors.Is(err, crew.ErrScopeBusy) {
			t.Fatalf("the run token from another process: %v, want ErrScopeBusy", err)
		}
	})

	t.Run("a released run can't reattach", func(t *testing.T) {
		m, _, _, _ := setup(t)
		e1, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Release(ctx, scope, "run-a", e1); err != nil {
			t.Fatalf("release: %v", err)
		}
		if err := m.Release(ctx, scope, "run-a", e1); !errors.Is(err, crew.ErrStaleEpoch) {
			t.Fatalf("a second release: %v, want ErrStaleEpoch", err)
		}
		e2, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil || e2 != e1+1 {
			t.Fatalf("a claim after release, same run: epoch %d, %v; want a NEW epoch %d", e2, err, e1+1)
		}
	})

	t.Run("takeover fences a live owner and ends its sessions", func(t *testing.T) {
		m, store, _, enders := setup(t)
		e1, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		seedSession(t, store, scope, e1, "s1")
		e2, err := m.Takeover(ctx, scope, "run-b", proc)
		if err != nil || e2 != e1+1 {
			t.Fatalf("takeover: epoch %d, %v; want %d", e2, err, e1+1)
		}
		if err := m.Release(ctx, scope, "run-a", e1); !errors.Is(err, crew.ErrStaleEpoch) {
			t.Fatalf("the old owner after a takeover: %v, want ErrStaleEpoch", err)
		}
		wantEnded(t, store, enders, "s1", crew.EndedTakeover)
	})

	t.Run("a reclaim ends the scope's sessions", func(t *testing.T) {
		m, store, clock, enders := setup(t)
		e1, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		seedSession(t, store, scope, e1, "s1")
		clock.Advance(91 * time.Second)
		if _, err := m.Claim(ctx, scope, "run-b", proc); err != nil {
			t.Fatal(err)
		}
		wantEnded(t, store, enders, "s1", crew.EndedReclaim)
	})

	t.Run("a scope adoption ended is never claimed", func(t *testing.T) {
		m, store, clock, _ := setup(t)
		if _, err := m.Claim(ctx, scope, "run-a", proc); err != nil {
			t.Fatal(err)
		}
		for _, state := range []string{crew.ScopeAdopting, crew.ScopeGone} {
			if err := store.Write(ctx, func(tx crew.Tx) error {
				sc, _, err := tx.Scope(scope)
				if err != nil {
					return err
				}
				sc.State = state
				return tx.PutScope(sc)
			}); err != nil {
				t.Fatal(err)
			}
			clock.Advance(91 * time.Second)
			if _, err := m.Claim(ctx, scope, "run-b", proc); !errors.Is(err, crew.ErrScopeBusy) {
				t.Fatalf("a claim of a scope in state %s: %v, want ErrScopeBusy", state, err)
			}
		}
	})

	t.Run("only a host's scopes are claimed", func(t *testing.T) {
		m, _, _, _ := setup(t)
		for _, s := range []string{"", "global", "autovim:", "jarvis", "autodoc:a b", "other:x"} {
			if _, err := m.Claim(ctx, s, "run-a", proc); !errors.Is(err, crew.ErrInvalid) {
				t.Errorf("claim of %q: %v, want ErrInvalid", s, err)
			}
		}
		if _, err := m.Claim(ctx, scope, "", proc); !errors.Is(err, crew.ErrInvalid) {
			t.Errorf("an empty run token: %v, want ErrInvalid", err)
		}
	})

	t.Run("racing claims: one owner", func(t *testing.T) {
		m, _, _, _ := setup(t)
		const n = 16
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = m.Claim(ctx, scope, "run-"+string(rune('a'+i)), proc)
			}()
		}
		wg.Wait()
		won := 0
		for i, err := range errs {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, crew.ErrScopeBusy):
				t.Errorf("claim %d: %v", i, err)
			}
		}
		if won != 1 {
			t.Fatalf("%d of %d racing claims won, want exactly 1", won, n)
		}
	})

	reg := func(epoch int64, codename, instance string) crew.Registration {
		return crew.Registration{Scope: scope, Run: "run-a", Epoch: epoch, Codename: codename, Kind: "claude",
			Workspace: "ws-1", Instance: instance, Secret: strings.Repeat("s", 32) + instance}
	}

	t.Run("register, and its replays", func(t *testing.T) {
		m, store, _, _ := setup(t)
		e, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		r := reg(e, "jarvis", "agent-1")
		got, err := m.Register(ctx, r)
		if err != nil || got.Session == "" || got.Profile == "" {
			t.Fatalf("register: %+v, %v", got, err)
		}
		if again, err := m.Register(ctx, r); err != nil || again != got {
			t.Fatalf("a replay after a lost answer: %+v, %v; want %+v", again, err, got)
		}
		for name, change := range map[string]func(*crew.Registration){
			"another secret":    func(r *crew.Registration) { r.Secret = strings.Repeat("x", 40) },
			"another codename":  func(r *crew.Registration) { r.Codename = "vision" },
			"another kind":      func(r *crew.Registration) { r.Kind = "codex" },
			"another workspace": func(r *crew.Registration) { r.Workspace = "ws-2" },
		} {
			bad := r
			change(&bad)
			if _, err := m.Register(ctx, bad); !errors.Is(err, crew.ErrInstanceReused) {
				t.Errorf("a replay with %s: %v, want ErrInstanceReused", name, err)
			}
		}
		// The digest must tell argument tuples apart whatever bytes they hold: joined with a
		// separator, these two would encode alike.
		odd := reg(e, "gold", "agent-9")
		odd.Workspace = "codex\x00ws-1"
		if _, err := m.Register(ctx, odd); err != nil {
			t.Fatalf("register gold: %v", err)
		}
		shifted := odd
		shifted.Codename, shifted.Kind, shifted.Workspace = "gold\x00claude", "codex", "ws-1"
		if _, err := m.Register(ctx, shifted); !errors.Is(err, crew.ErrInstanceReused) {
			t.Errorf("a replay whose fields shift across the separator: %v, want ErrInstanceReused", err)
		}
		if _, err := m.Register(ctx, reg(e, "jarvis", "agent-2")); !errors.Is(err, crew.ErrCodenameBusy) {
			t.Fatalf("a second live session of the codename: %v, want ErrCodenameBusy", err)
		}
		if err := m.Exit(ctx, scope, "run-a", e, got.Session, crew.EndedExit); err != nil {
			t.Fatalf("exit: %v", err)
		}
		if _, err := m.Register(ctx, r); !errors.Is(err, crew.ErrInstanceReused) {
			t.Fatalf("a replay of an ended session: %v, want ErrInstanceReused (never an ended session back)", err)
		}
		next, err := m.Register(ctx, reg(e, "jarvis", "agent-2"))
		if err != nil || next.Profile != got.Profile || next.Session == got.Session {
			t.Fatalf("the codename's next session: %+v, %v; want a new session of profile %s", next, err, got.Profile)
		}
		// The codename comes back under another CLI: its profile takes the new kind, or the
		// session's own endpoints would be refused as another kind's.
		if err := m.Exit(ctx, scope, "run-a", e, next.Session, crew.EndedExit); err != nil {
			t.Fatalf("exit: %v", err)
		}
		r3 := reg(e, "jarvis", "agent-3")
		r3.Kind = "codex"
		third, err := m.Register(ctx, r3)
		if err != nil || third.Profile != got.Profile {
			t.Fatalf("the codename under codex: %+v, %v; want a session of profile %s", third, err, got.Profile)
		}
		var p crew.Profile
		if err := store.Read(ctx, func(tx crew.Tx) error { var err error; p, _, err = tx.Profile(got.Profile); return err }); err != nil {
			t.Fatal(err)
		}
		if p.Kind != "codex" {
			t.Fatalf("the profile's kind after a codex session registered: %q, want codex", p.Kind)
		}
	})

	t.Run("a new epoch is a new namespace of instances", func(t *testing.T) {
		m, _, clock, _ := setup(t)
		e1, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		first, err := m.Register(ctx, reg(e1, "jarvis", "agent-1"))
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(91 * time.Second) // the host crashed; its successor, a new process, claims
		e2, err := m.Claim(ctx, scope, "run-a2", proc)
		if err != nil || e2 != e1+1 {
			t.Fatalf("the successor's claim: epoch %d, %v; want %d", e2, err, e1+1)
		}
		r := reg(e2, "jarvis", "agent-1") // the successor's own agent-1
		r.Run = "run-a2"
		r.Secret = strings.Repeat("n", 40)
		second, err := m.Register(ctx, r)
		if err != nil || second.Session == first.Session {
			t.Fatalf("the successor's agent-1: %+v, %v; want a new session", second, err)
		}
		if _, err := m.Register(ctx, reg(e1, "jarvis", "agent-3")); !errors.Is(err, crew.ErrStaleEpoch) {
			t.Fatalf("a register under the fenced epoch: %v, want ErrStaleEpoch", err)
		}
	})

	t.Run("register seeds pending_gen only for waiting mail", func(t *testing.T) {
		m, store, _, enders := setup(t)
		e, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		quiet, err := m.Register(ctx, reg(e, "jarvis", "agent-1"))
		if err != nil {
			t.Fatal(err)
		}
		if w := wakeOf(t, store, quiet.Session); w.PendingGen != 0 || w.State != crew.WakeIdle {
			t.Fatalf("a session with no mail waiting: wake %+v, want idle with pending_gen 0", w)
		}
		// a profile with mail waiting before its first session exists
		if err := store.Write(ctx, func(tx crew.Tx) error {
			return tx.PutProfile(crew.Profile{ID: "p-vision", Scope: scope, Codename: "vision", Kind: "claude",
				Status: crew.ProfileActive, Created: tx.Now().UnixNano()})
		}); err != nil {
			t.Fatal(err)
		}
		enders.mu.Lock()
		enders.Pending = map[string]bool{"p-vision": true}
		enders.mu.Unlock()
		waiting, err := m.Register(ctx, reg(e, "vision", "agent-2"))
		if err != nil || waiting.Profile != "p-vision" {
			t.Fatalf("register vision: %+v, %v", waiting, err)
		}
		if w := wakeOf(t, store, waiting.Session); w.PendingGen != 1 {
			t.Fatalf("a session whose profile has mail waiting: pending_gen %d, want 1", w.PendingGen)
		}
	})

	t.Run("exit: once, idempotent, and fenced", func(t *testing.T) {
		m, store, clock, enders := setup(t)
		e, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.Register(ctx, reg(e, "jarvis", "agent-1"))
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Exit(ctx, scope, "run-a", e, got.Session, "crashed"); !errors.Is(err, crew.ErrInvalid) {
			t.Fatalf("an unknown exit reason: %v, want ErrInvalid", err)
		}
		if err := m.Exit(ctx, scope, "run-a", e, got.Session, crew.EndedStartFailed); err != nil {
			t.Fatal(err)
		}
		if err := m.Exit(ctx, scope, "run-a", e, got.Session, crew.EndedExit); err != nil {
			t.Fatalf("exiting an ended session again: %v, want nil", err)
		}
		wantEnded(t, store, enders, got.Session, crew.EndedStartFailed)
		if err := m.Exit(ctx, scope, "run-a", e, "no-such-session", crew.EndedExit); !errors.Is(err, crew.ErrBadCredential) {
			t.Fatalf("exit of another host's session: %v, want ErrBadCredential", err)
		}
		clock.Advance(91 * time.Second)
		if _, err := m.Claim(ctx, scope, "run-b", proc); err != nil {
			t.Fatal(err)
		}
		if err := m.Exit(ctx, scope, "run-a", e, got.Session, crew.EndedExit); !errors.Is(err, crew.ErrStaleEpoch) {
			t.Fatalf("exit from a fenced host: %v, want ErrStaleEpoch", err)
		}
		if err := m.Renew(ctx, scope, "run-a", e); !errors.Is(err, crew.ErrStaleEpoch) {
			t.Fatalf("renew from a fenced host: %v, want ErrStaleEpoch", err)
		}
	})

	t.Run("GuardSession and RaisePending, for the mail module", func(t *testing.T) {
		m, store, clock, _ := setup(t)
		e, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.Register(ctx, reg(e, "jarvis", "agent-1"))
		if err != nil {
			t.Fatal(err)
		}
		inWrite := func(fn func(crew.Tx) error) error { return store.Write(ctx, fn) }
		if err := inWrite(func(tx crew.Tx) error { _, err := m.GuardSession(tx, got.Session); return err }); err != nil {
			t.Fatalf("a live session's guard: %v", err)
		}
		for range 2 {
			if err := inWrite(func(tx crew.Tx) error { return m.RaisePending(tx, got.Profile) }); err != nil {
				t.Fatal(err)
			}
		}
		if w := wakeOf(t, store, got.Session); w.PendingGen != 2 {
			t.Fatalf("pending_gen after two raises: %d, want 2", w.PendingGen)
		}
		if err := inWrite(func(tx crew.Tx) error { return m.RaisePending(tx, "no-such-profile") }); err != nil {
			t.Fatalf("raising a profile with no live session: %v, want nil", err)
		}
		// a live session with no wake row yet (registered before the scheduler kept one): the raise
		// makes its row, idle, with the raise in it
		seedSession(t, store, scope, e, "bare")
		if err := inWrite(func(tx crew.Tx) error { return m.RaisePending(tx, "p-bare") }); err != nil {
			t.Fatal(err)
		}
		if w := wakeOf(t, store, "bare"); w.PendingGen != 1 || w.State != crew.WakeIdle {
			t.Fatalf("the wake row a raise made: %+v, want idle with pending_gen 1", w)
		}
		clock.Advance(91 * time.Second)
		if _, err := m.Claim(ctx, scope, "run-b", proc); err != nil { // reclaims: the session ends
			t.Fatal(err)
		}
		if err := inWrite(func(tx crew.Tx) error { _, err := m.GuardSession(tx, got.Session); return err }); !errors.Is(err, crew.ErrBadCredential) {
			t.Fatalf("a reclaimed session's guard: %v, want ErrBadCredential", err)
		}
	})

	t.Run("authenticate and whoami", func(t *testing.T) {
		clock, enders := NewClock(), &Enders{}
		store := h.New(t, clock.Now)
		m := crew.New(store, crew.WithSessionEnder(enders), crew.WithStoreID("store-1"))
		e, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		r := reg(e, "jarvis", "agent-1")
		got, err := m.Register(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		cred := crew.Credential(got.Session, r.Secret)
		id, err := m.Whoami(ctx, cred, "store-1")
		want := crew.Identity{Codename: "jarvis", Scope: scope, Profile: got.Profile, Session: got.Session, Store: "store-1"}
		if err != nil || id != want {
			t.Fatalf("whoami: %+v, %v; want %+v", id, err, want)
		}
		for name, c := range map[string]string{
			"another secret":   crew.Credential(got.Session, strings.Repeat("z", 40)),
			"no such session":  crew.Credential("nope", r.Secret),
			"no separator":     got.Session + r.Secret,
			"an empty secret":  got.Session + ".",
			"an empty session": "." + r.Secret,
			"the empty string": "",
		} {
			if _, err := m.Whoami(ctx, c, ""); !errors.Is(err, crew.ErrBadCredential) {
				t.Errorf("whoami with %s: %v, want ErrBadCredential", name, err)
			}
		}
		if _, err := m.Whoami(ctx, cred, "store-2"); !errors.Is(err, crew.ErrContextMismatch) {
			t.Fatalf("whoami with another store's context: %v, want ErrContextMismatch", err)
		}
		if err := m.Exit(ctx, scope, "run-a", e, got.Session, crew.EndedExit); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Whoami(ctx, cred, "store-1"); !errors.Is(err, crew.ErrBadCredential) {
			t.Fatalf("whoami of an ended session: %v, want ErrBadCredential", err)
		}
	})

	t.Run("endpoints: checked, canonical, and a fresh wake", func(t *testing.T) {
		clock, enders := NewClock(), &Enders{}
		store := h.New(t, clock.Now)
		m := crew.New(store, crew.WithSessionEnder(enders), crew.WithUID(1000))
		e, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		r := reg(e, "jarvis", "agent-1")
		got, err := m.Register(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		cred := crew.Credential(got.Session, r.Secret)
		claude := `{"socket":"/run/user/1000/cc-socks/a.sock","pid":42,"start":"9000","session":"c-1"}`
		// a wake row mid-backoff, as a failed attempt leaves it
		if err := store.Write(ctx, func(tx crew.Tx) error {
			w, _, err := tx.Wake(got.Session)
			if err != nil {
				return err
			}
			next := tx.Now().Add(30 * time.Second).UnixNano()
			w.State, w.Token, w.Failures, w.NextAt = crew.WakeAwaiting, "tok", 2, &next
			return tx.PutWake(w)
		}); err != nil {
			t.Fatal(err)
		}
		gen, err := m.SetEndpoint(ctx, cred, "claude", "  "+claude+"\n")
		if err != nil || gen != 1 {
			t.Fatalf("a claude endpoint: generation %d, %v; want 1", gen, err)
		}
		w := wakeOf(t, store, got.Session)
		if w.State != crew.WakeIdle || w.Token != "" || w.Failures != 0 || w.NextAt != nil {
			t.Fatalf("the wake row after a new endpoint: %+v; want idle, no token, no failures, no deadline", w)
		}
		if gen, err := m.SetEndpoint(ctx, cred, "claude", claude); err != nil || gen != 2 {
			t.Fatalf("a second endpoint: generation %d, %v; want 2", gen, err)
		}
		var ep crew.Endpoint
		if err := store.Read(ctx, func(tx crew.Tx) error { var err error; ep, _, err = tx.Endpoint(got.Session); return err }); err != nil {
			t.Fatal(err)
		}
		if ep.Locator != claude || ep.Kind != "claude" {
			t.Fatalf("stored endpoint %+v; want the canonical locator %s", ep, claude)
		}
		for name, c := range map[string][2]string{
			"another user's socket":     {"claude", `{"socket":"/run/user/1001/cc-socks/a.sock","pid":42,"start":"9000","session":"c-1"}`},
			"a socket outside the root": {"claude", `{"socket":"/tmp/elsewhere/a.sock","pid":42,"start":"9000","session":"c-1"}`},
			"a path climbing out":       {"claude", `{"socket":"/run/user/1000/cc-socks/../../x.sock","pid":42,"start":"9000","session":"c-1"}`},
			"an unknown field":          {"claude", `{"socket":"/run/user/1000/cc-socks/a.sock","pid":42,"start":"9000","session":"c-1","token":"x"}`},
			"no pid":                    {"claude", `{"socket":"/run/user/1000/cc-socks/a.sock","start":"9000","session":"c-1"}`},
			"a non-loopback agy":        {"agy", `{"address":"10.0.0.5:4000","conversation":"x"}`},
			"an unknown kind":           {"vim", `{}`},
			"not JSON":                  {"claude", `socket=/run/user/1000/cc-socks/a.sock`},
			"a stray closing bracket":   {"claude", claude + `]`},
			"a stray closing brace":     {"claude", claude + `}`},
			"a second value":            {"claude", claude + ` {}`},
		} {
			if _, err := m.SetEndpoint(ctx, cred, c[0], c[1]); !errors.Is(err, crew.ErrBadEndpoint) {
				t.Errorf("%s: %v, want ErrBadEndpoint", name, err)
			}
		}
		if _, err := m.SetEndpoint(ctx, cred, "codex", `{"server":"/tmp/codex-daemon-1000/s.sock","thread":"t-1"}`); !errors.Is(err, crew.ErrBadEndpoint) {
			t.Fatalf("a codex endpoint for a claude session: %v, want ErrBadEndpoint", err)
		}
		if _, err := m.SetEndpoint(ctx, crew.Credential(got.Session, strings.Repeat("q", 40)), "claude", claude); !errors.Is(err, crew.ErrBadCredential) {
			t.Fatalf("an endpoint with a bad credential: %v, want ErrBadCredential", err)
		}
		if err := m.RaiseAttention(ctx, cred, crew.AttentionPermission, "edit main.go?"); err != nil {
			t.Fatal(err)
		}
		if err := m.RaiseAttention(ctx, cred, "nudge", "x"); !errors.Is(err, crew.ErrInvalid) {
			t.Fatalf("an unknown attention kind: %v, want ErrInvalid", err)
		}
		if err := m.Exit(ctx, scope, "run-a", e, got.Session, crew.EndedExit); err != nil {
			t.Fatal(err)
		}
		if err := store.Read(ctx, func(tx crew.Tx) error {
			if _, ok, err := tx.Endpoint(got.Session); err != nil || ok {
				t.Errorf("an ended session's endpoint: kept %v, %v; want it gone", ok, err)
			}
			a, ok, err := tx.Attention(got.Session)
			if err != nil || !ok || a.Cleared == nil || a.Kind != crew.AttentionPermission || a.Owner != scope {
				t.Errorf("an ended session's attention: %+v, %v, %v; want it cleared", a, ok, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if mem, ok := store.(*Mem); ok {
			want := []InboxChange{{"attention", got.Session}, {"cleared", got.Session}}
			if got := mem.Inbox(); !slices.Equal(got, want) {
				t.Fatalf("the human feed's journal: %v, want %v", got, want)
			}
		}
	})

	t.Run("list: presence and reachability", func(t *testing.T) {
		clock, enders := NewClock(), &Enders{}
		store := h.New(t, clock.Now)
		started := clock.Now()
		m := crew.New(store, crew.WithSessionEnder(enders), crew.WithUID(1000), crew.WithStarted(started))
		e, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		rv, rj := reg(e, "vision", "agent-2"), reg(e, "jarvis", "agent-1")
		vision, err := m.Register(ctx, rv)
		if err != nil {
			t.Fatal(err)
		}
		jarvis, err := m.Register(ctx, rj)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.SetEndpoint(ctx, crew.Credential(jarvis.Session, rj.Secret), "claude",
			`{"socket":"/run/user/1000/cc-socks/j.sock","pid":7,"start":"1","session":"c"}`); err != nil {
			t.Fatal(err)
		}
		list := func() []crew.Entry {
			t.Helper()
			l, err := m.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return l
		}
		want := []crew.Entry{
			{Codename: "jarvis", Scope: scope, Kind: "claude", Presence: crew.PresenceLive, Reachable: true},
			{Codename: "vision", Scope: scope, Kind: "claude", Presence: crew.PresenceLive, Reachable: false},
		}
		if got := list(); !slices.Equal(got, want) {
			t.Fatalf("list: %+v\nwant %+v", got, want)
		}
		clock.Advance(60 * time.Second) // the daemon is 60 s old; renew, then let the host lapse
		if err := m.Renew(ctx, scope, "run-a", e); err != nil {
			t.Fatal(err)
		}
		clock.Advance(91 * time.Second)
		if got := list(); got[0].Presence != crew.PresenceOffline {
			t.Fatalf("a lapsed host's agent: %s, want offline", got[0].Presence)
		}
		young := crew.New(store, crew.WithSessionEnder(enders), crew.WithStarted(clock.Now().Add(-10*time.Second)))
		if got, err := young.List(ctx); err != nil || got[0].Presence != crew.PresenceReconnecting {
			t.Fatalf("a lapsed host on a daemon 10 s old: %+v, %v; want reconnecting", got, err)
		}
		if err := store.Write(ctx, func(tx crew.Tx) error {
			w, _, err := tx.Wake(jarvis.Session)
			if err != nil {
				return err
			}
			w.Failures = 5
			return tx.PutWake(w)
		}); err != nil {
			t.Fatal(err)
		}
		if got := list(); got[0].Reachable {
			t.Fatal("an agent after 5 failed wakes still reads reachable")
		}
		clock.Advance(-91 * time.Second) // the host back within its lease, to exit cleanly
		if err := m.Exit(ctx, scope, "run-a", e, vision.Session, crew.EndedExit); err != nil {
			t.Fatal(err)
		}
		if got := list(); got[1].Presence != crew.PresenceEnded || got[1].Reachable {
			t.Fatalf("an exited agent: %+v, want ended and unreachable", got[1])
		}
	})

	t.Run("orphans, adopt and discard", func(t *testing.T) {
		clock, enders := NewClock(), &Enders{Unhandled: map[string]int{}}
		store := h.New(t, clock.Now)
		m := crew.New(store, crew.WithSessionEnder(enders))
		const w1, w2 = "autodoc:aaaa", "autodoc:bbbb"
		regIn := func(sc, run string, epoch int64, codename, instance string) crew.Registered {
			t.Helper()
			r := crew.Registration{Scope: sc, Run: run, Epoch: epoch, Codename: codename, Kind: "claude",
				Workspace: "ws-1", Instance: instance, Secret: strings.Repeat("s", 32) + instance}
			got, err := m.Register(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		e1, err := m.Claim(ctx, w1, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		oldJarvis := regIn(w1, "run-a", e1, "jarvis", "a-1")
		oldVision := regIn(w1, "run-a", e1, "vision", "a-2")
		enders.mu.Lock()
		enders.Unhandled[oldJarvis.Profile], enders.Unhandled[oldVision.Profile] = 2, 1
		enders.mu.Unlock()
		if o, err := m.Orphans(ctx); err != nil || len(o) != 0 {
			t.Fatalf("orphans while w1's holder is live: %+v, %v; want none", o, err)
		}
		clock.Advance(91 * time.Second) // w1's window closed without releasing
		e2, err := m.Claim(ctx, w2, "run-b", proc)
		if err != nil {
			t.Fatal(err)
		}
		newJarvis := regIn(w2, "run-b", e2, "jarvis", "b-1")
		o, err := m.Orphans(ctx)
		want := []crew.Orphan{{Scope: w1, Profiles: []string{"jarvis", "vision"}, Unread: 3}}
		if err != nil || !reflect.DeepEqual(o, want) {
			t.Fatalf("orphans: %+v, %v; want %+v", o, err, want)
		}
		if err := m.Adopt(ctx, w1, w2, "run-b", e2+1); !errors.Is(err, crew.ErrStaleEpoch) {
			t.Fatalf("an adopt from a stale epoch: %v, want ErrStaleEpoch", err)
		}
		if err := m.Adopt(ctx, w1, w2, "run-b", e2); err != nil {
			t.Fatalf("adopt: %v", err)
		}
		enders.mu.Lock()
		merged := slices.Clone(enders.Merged)
		enders.mu.Unlock()
		if wantM := [][2]string{{oldJarvis.Profile, newJarvis.Profile}, {oldVision.Profile, ""}}; !reflect.DeepEqual(merged, wantM) {
			t.Fatalf("mail's merges: %v, want %v (jarvis into w2's jarvis, vision moved whole)", merged, wantM)
		}
		// the forwarding was real before mail moved anything: a notice the merge makes to the
		// profile, as a sender, resolves to the survivor
		enders.mu.Lock()
		seen := slices.Clone(enders.MergedSeen)
		enders.mu.Unlock()
		if len(seen) != 2 || seen[0].Status != crew.ProfileMerged || seen[0].MergedInto != newJarvis.Profile ||
			seen[1].Status != crew.ProfileActive || seen[1].Scope != w2 {
			t.Fatalf("the profiles as MergeDeliveries saw them: %+v; want jarvis merged into w2's, vision already in w2", seen)
		}
		if err := store.Read(ctx, func(tx crew.Tx) error {
			j, _, err := tx.Profile(oldJarvis.Profile)
			if err != nil {
				return err
			}
			v, _, err := tx.Profile(oldVision.Profile)
			if err != nil {
				return err
			}
			sc, _, err := tx.Scope(w1)
			if err != nil {
				return err
			}
			s, _, err := tx.Session(oldVision.Session)
			if err != nil {
				return err
			}
			if j.Status != crew.ProfileMerged || j.MergedInto != newJarvis.Profile {
				t.Errorf("w1's jarvis: %+v, want merged into w2's", j)
			}
			if v.Scope != w2 || v.Status != crew.ProfileActive {
				t.Errorf("w1's vision: %+v, want moved to w2, active", v)
			}
			if sc.State != crew.ScopeGone {
				t.Errorf("w1: %s, want gone", sc.State)
			}
			if s.Ended == nil || s.EndedWhy != crew.EndedAdopted {
				t.Errorf("w1's session: ended %v %q, want adopted", s.Ended != nil, s.EndedWhy)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := m.Release(ctx, w1, "run-a", e1); !errors.Is(err, crew.ErrStaleEpoch) {
			t.Fatalf("w1's old owner after adoption: %v, want ErrStaleEpoch", err)
		}
		if _, err := m.Claim(ctx, w1, "run-a", proc); !errors.Is(err, crew.ErrScopeBusy) {
			t.Fatalf("a claim of the adopted scope: %v, want ErrScopeBusy", err)
		}
		if err := m.Adopt(ctx, w1, w2, "run-b", e2); !errors.Is(err, crew.ErrScopeBusy) {
			t.Fatalf("a second adopt: %v, want ErrScopeBusy", err)
		}
		if o, _ := m.Orphans(ctx); len(o) != 0 {
			t.Fatalf("orphans after adoption: %+v, want none", o)
		}

		// discard: a third window's scope, held and then lapsed
		const w3 = "autodoc:cccc"
		e3, err := m.Claim(ctx, w3, "run-c", proc)
		if err != nil {
			t.Fatal(err)
		}
		gone := regIn(w3, "run-c", e3, "tifa", "c-1")
		if err := m.Discard(ctx, w3, w2, "run-b", e2); !errors.Is(err, crew.ErrScopeBusy) {
			t.Fatalf("discarding a held scope: %v, want ErrScopeBusy", err)
		}
		if err := m.Renew(ctx, w2, "run-b", e2); err != nil { // w2 stays live while w3 lapses
			t.Fatal(err)
		}
		clock.Advance(91 * time.Second)
		// w2 lapsed too, but nobody replaced its owner: it still holds w2 at e2, and renews
		if err := m.Renew(ctx, w2, "run-b", e2); err != nil {
			t.Fatalf("a lapsed owner nobody replaced, renewing: %v", err)
		}
		if err := m.Discard(ctx, w3, w2, "run-b", e2); err != nil {
			t.Fatalf("discard: %v", err)
		}
		enders.mu.Lock()
		discarded, states := slices.Clone(enders.Discarded), slices.Clone(enders.DiscardSeen)
		enders.mu.Unlock()
		if !slices.Equal(discarded, []string{gone.Profile}) {
			t.Fatalf("mail's discards: %v, want [%s]", discarded, gone.Profile)
		}
		if !slices.Equal(states, []string{crew.ScopeGone}) {
			t.Fatalf("the scope's state as mail's discard saw it: %v, want [gone] (a notice queued on a live scope's profile waits forever)", states)
		}
		if err := m.Discard(ctx, w2, w2, "run-b", e2); !errors.Is(err, crew.ErrInvalid) {
			t.Fatalf("a scope discarding itself: %v, want ErrInvalid", err)
		}
	})
}

// wakeOf is session's wake row.
func wakeOf(t *testing.T, store crew.Store, session string) crew.Wake {
	t.Helper()
	var w crew.Wake
	if err := store.Read(context.Background(), func(tx crew.Tx) error {
		var ok bool
		var err error
		w, ok, err = tx.Wake(session)
		if err == nil && !ok {
			t.Fatalf("session %s has no wake row", session)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

// seedSession puts a live session of scope at epoch, as crew.register will.
func seedSession(t *testing.T, store crew.Store, scope string, epoch int64, id string) {
	t.Helper()
	if err := store.Write(context.Background(), func(tx crew.Tx) error {
		return tx.PutSession(crew.Session{ID: id, Profile: "p-" + id, Host: scope, Epoch: epoch, Instance: "i-" + id,
			SecretHash: []byte{1}, ArgsDigest: []byte{2}, Started: tx.Now().UnixNano()})
	}); err != nil {
		t.Fatal(err)
	}
}

// wantEnded checks session id ended for why, and that the mail module heard of it once.
func wantEnded(t *testing.T, store crew.Store, enders *Enders, id, why string) {
	t.Helper()
	var s crew.Session
	if err := store.Read(context.Background(), func(tx crew.Tx) error {
		var err error
		s, _, err = tx.Session(id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if s.Ended == nil || s.EndedWhy != why {
		t.Fatalf("session %s: ended %v, why %q; want ended, %q", id, s.Ended != nil, s.EndedWhy, why)
	}
	enders.mu.Lock()
	defer enders.mu.Unlock()
	if len(enders.Ended) != 1 || enders.Ended[0] != (EndedSession{id, why}) {
		t.Fatalf("the mail module heard %v, want one %s ended %q", enders.Ended, id, why)
	}
}
