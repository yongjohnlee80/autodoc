package crewtest

import (
	"context"
	"errors"
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
	mu    sync.Mutex
	Ended []EndedSession
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
