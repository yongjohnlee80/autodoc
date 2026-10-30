package crewtest

import (
	"context"
	"errors"
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
	mu        sync.Mutex
	Ended     []EndedSession
	Merged    [][2]string     // (from, into) profiles
	Discarded []string        // profiles
	Pending   map[string]bool // profiles with eligible mail, for HasEligible
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

// MergeDeliveries records adoption's mail half (crew.SessionEnder).
func (e *Enders) MergeDeliveries(_ crew.Tx, from, into string) error {
	e.mu.Lock()
	e.Merged = append(e.Merged, [2]string{from, into})
	e.mu.Unlock()
	return nil
}

// HasEligible reports the profiles Pending names (crew.SessionEnder).
func (e *Enders) HasEligible(_ crew.Tx, profile string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Pending[profile], nil
}

// DiscardDeliveries records discard's mail half (crew.SessionEnder).
func (e *Enders) DiscardDeliveries(_ crew.Tx, profile string) error {
	e.mu.Lock()
	e.Discarded = append(e.Discarded, profile)
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
		if _, err := m.Claim(ctx, scope, "run-a", "boot:4026531836:100:5000"); err != nil {
			t.Fatal(err)
		}
		// the same run token from another process tuple is not a reattach, and the holder is live
		if _, err := m.Claim(ctx, scope, "run-a", "boot:4026531836:101:5000"); !errors.Is(err, crew.ErrScopeBusy) {
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
		m, _, _, _ := setup(t)
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
		clock.Advance(91 * time.Second) // the host crashed; its successor claims
		e2, err := m.Claim(ctx, scope, "run-a", proc)
		if err != nil {
			t.Fatal(err)
		}
		r := reg(e2, "jarvis", "agent-1") // the successor's own agent-1
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
		clock.Advance(91 * time.Second)
		if _, err := m.Claim(ctx, scope, "run-b", proc); err != nil { // reclaims: the session ends
			t.Fatal(err)
		}
		if err := inWrite(func(tx crew.Tx) error { _, err := m.GuardSession(tx, got.Session); return err }); !errors.Is(err, crew.ErrBadCredential) {
			t.Fatalf("a reclaimed session's guard: %v, want ErrBadCredential", err)
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
