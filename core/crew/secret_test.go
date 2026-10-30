package crew_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// agySession is an agy session registered in a fresh in-memory store, its manager, and the epoch
// and credential it was registered with.
func agySession(t *testing.T) (*crew.Manager, string, string, int64) {
	t.Helper()
	ctx := context.Background()
	m := crew.New(crewtest.NewMem(crewtest.NewClock().Now), crew.WithUID(1000))
	e, err := m.Claim(ctx, "autodoc:aaaa", "run-a", "")
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 32) + "tifa"
	got, err := m.Register(ctx, crew.Registration{Scope: "autodoc:aaaa", Run: "run-a", Epoch: e, Codename: "tifa",
		Kind: "agy", Workspace: "w", Instance: "agent-1", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return m, got.Session, crew.Credential(got.Session, secret), e
}

const agyLoc = `{"address":"127.0.0.1:4000","conversation":"c-1"}`

// TestAnEndpointSecretIsTheGenerationsAlone: an agy endpoint's secret is held for the generation it
// came with and no other: a later endpoint without one leaves none, a later one with one replaces
// it, and an older generation's never reads. Another kind takes no secret, and a secret that could
// not be a header value is refused.
func TestAnEndpointSecretIsTheGenerationsAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, session, cred, _ := agySession(t)
	g1, err := m.SetEndpointWith(ctx, cred, "agy", agyLoc, "tok-one")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.EndpointSecret(session, g1); !ok || got != "tok-one" {
		t.Fatalf("generation %d's secret: %q %v, want tok-one", g1, got, ok)
	}
	g2, err := m.SetEndpointWith(ctx, cred, "agy", agyLoc, "tok-two")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.EndpointSecret(session, g1); ok {
		t.Error("the replaced generation's secret still reads")
	}
	if got, ok := m.EndpointSecret(session, g2); !ok || got != "tok-two" {
		t.Errorf("generation %d's secret: %q %v, want tok-two", g2, got, ok)
	}
	g3, err := m.SetEndpoint(ctx, cred, "agy", agyLoc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.EndpointSecret(session, g3); ok {
		t.Error("an endpoint registered without a secret reads one")
	}
	for _, bad := range []string{"has space", "tab\there", strings.Repeat("x", 513), "nul\x00"} {
		if _, err := m.SetEndpointWith(ctx, cred, "agy", agyLoc, bad); !errors.Is(err, crew.ErrBadEndpoint) {
			t.Errorf("secret %.20q: %v, want ErrBadEndpoint", bad, err)
		}
	}
}

// TestOnlyAnAgyEndpointTakesASecret: a secret with any other kind is refused, and never held.
func TestOnlyAnAgyEndpointTakesASecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := crew.New(crewtest.NewMem(crewtest.NewClock().Now), crew.WithUID(1000))
	e, err := m.Claim(ctx, "autodoc:aaaa", "run-a", "")
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 32) + "gold"
	got, err := m.Register(ctx, crew.Registration{Scope: "autodoc:aaaa", Run: "run-a", Epoch: e, Codename: "gold",
		Kind: "claude", Workspace: "w", Instance: "agent-1", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	loc := `{"socket":"/run/user/1000/cc-socks/a.sock","pid":42,"start":"b:n:42:9000","session":"c-1"}`
	if _, err := m.SetEndpointWith(ctx, crew.Credential(got.Session, secret), "claude", loc, "tok"); !errors.Is(err, crew.ErrBadEndpoint) {
		t.Errorf("a claude endpoint with a secret: %v, want ErrBadEndpoint", err)
	}
	for gen := int64(0); gen < 3; gen++ {
		if _, ok := m.EndpointSecret(got.Session, gen); ok {
			t.Errorf("a refused secret is held for generation %d", gen)
		}
	}
}

// TestAnEndedSessionsSecretIsForgotten: ending a session forgets its secret in the same write, so a
// registration that follows it (a delayed bridge) is refused rather than holding one again.
func TestAnEndedSessionsSecretIsForgotten(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, session, cred, e := agySession(t)
	gen, err := m.SetEndpointWith(ctx, cred, "agy", agyLoc, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Exit(ctx, "autodoc:aaaa", "run-a", e, session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.EndpointSecret(session, gen); ok {
		t.Error("an ended session's secret still reads")
	}
	if _, err := m.SetEndpointWith(ctx, cred, "agy", agyLoc, "tok-late"); !errors.Is(err, crew.ErrBadCredential) {
		t.Errorf("a registration after the end: %v, want ErrBadCredential", err)
	}
	for g := gen; g <= gen+1; g++ {
		if _, ok := m.EndpointSecret(session, g); ok {
			t.Errorf("a late registration left a secret for generation %d", g)
		}
	}
}

// gatedStore is a store whose writes the cell steers: fail rolls the next ones back after their
// change (an error the commit itself might give), and hold keeps the next write, once committed,
// from returning until it is closed (committed is closed then), so another write overtakes it.
type gatedStore struct {
	*crewtest.Mem
	mu        sync.Mutex
	fail      bool
	hold      chan struct{}
	committed chan struct{}
}

func (s *gatedStore) Write(ctx context.Context, fn func(crew.Tx) error) error {
	s.mu.Lock()
	fail, hold, committed := s.fail, s.hold, s.committed
	s.hold, s.committed = nil, nil
	s.mu.Unlock()
	err := s.Mem.Write(ctx, func(tx crew.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if fail {
			return errors.New("the commit failed")
		}
		return nil
	})
	if hold != nil {
		close(committed)
		<-hold
	}
	return err
}

// holdNext makes s's next write wait, committed, until release is called.
func (s *gatedStore) holdNext() (committed <-chan struct{}, release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hold, s.committed = make(chan struct{}), make(chan struct{})
	h := s.hold
	return s.committed, func() { close(h) }
}

// gatedAgy is agySession on a gatedStore, with an endpoint registered with tok-one.
func gatedAgy(t *testing.T) (*gatedStore, *crew.Manager, string, string, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st := &gatedStore{Mem: crewtest.NewMem(crewtest.NewClock().Now)}
	m := crew.New(st, crew.WithUID(1000))
	e, err := m.Claim(ctx, "autodoc:aaaa", "run-a", "")
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 32) + "tifa"
	got, err := m.Register(ctx, crew.Registration{Scope: "autodoc:aaaa", Run: "run-a", Epoch: e, Codename: "tifa",
		Kind: "agy", Workspace: "w", Instance: "agent-1", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	cred := crew.Credential(got.Session, secret)
	gen, err := m.SetEndpointWith(ctx, cred, "agy", agyLoc, "tok-one")
	if err != nil {
		t.Fatal(err)
	}
	return st, m, got.Session, cred, e, gen
}

// TestAWriteThatRollsBackKeepsTheLiveSecret: a write that fails to commit changes no secret: an
// endpoint replacement, a session's exit and its scope's release that roll back each leave the
// stored generation and the live session with the secret they had.
func TestAWriteThatRollsBackKeepsTheLiveSecret(t *testing.T) {
	t.Parallel()
	for name, write := range map[string]func(m *crew.Manager, session, cred string, epoch int64) error{
		"an endpoint replaced": func(m *crew.Manager, _, cred string, _ int64) error {
			_, err := m.SetEndpointWith(context.Background(), cred, "agy", agyLoc, "tok-two")
			return err
		},
		"the session's exit": func(m *crew.Manager, session, _ string, epoch int64) error {
			return m.Exit(context.Background(), "autodoc:aaaa", "run-a", epoch, session, crew.EndedExit)
		},
		"its scope's release": func(m *crew.Manager, _, _ string, epoch int64) error {
			return m.Release(context.Background(), "autodoc:aaaa", "run-a", epoch)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st, m, session, cred, epoch, gen := gatedAgy(t)
			st.fail = true
			if err := write(m, session, cred, epoch); err == nil {
				t.Fatal("the write committed")
			}
			if err := st.Read(context.Background(), func(tx crew.Tx) error {
				e, ok, err := tx.Endpoint(session)
				if err != nil || !ok || e.Generation != gen {
					t.Fatalf("the stored endpoint after the rollback: %+v %v %v, want generation %d", e, ok, err, gen)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if got, ok := m.EndpointSecret(session, gen); !ok || got != "tok-one" {
				t.Errorf("generation %d's secret after the rollback: %q %v, want tok-one", gen, got, ok)
			}
		})
	}
}

// TestAnOvertakenRegistrationLeavesTheNewerSecret: secrets are held after their writes commit, in
// no set order. A registration that commits and is overtaken (a later one commits, and its secret
// is held, before the first's is) leaves the later generation's secret, not its own.
func TestAnOvertakenRegistrationLeavesTheNewerSecret(t *testing.T) {
	t.Parallel()
	st, m, session, cred, _, g1 := gatedAgy(t)
	committed, release := st.holdNext()
	done := make(chan error, 1)
	go func() {
		_, err := m.SetEndpointWith(context.Background(), cred, "agy", agyLoc, "tok-two")
		done <- err
	}()
	<-committed
	g3, err := m.SetEndpointWith(context.Background(), cred, "agy", agyLoc, "tok-three")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, ok := m.EndpointSecret(session, g3); !ok || got != "tok-three" {
		t.Errorf("the newest generation %d's secret: %q %v, want tok-three", g3, got, ok)
	}
	for g := g1; g < g3; g++ {
		if got, ok := m.EndpointSecret(session, g); ok {
			t.Errorf("generation %d's secret %q still reads", g, got)
		}
	}
}

// TestARegistrationHeldPastTheEndLeavesNoSecret: a registration that commits before its session's
// end, and whose secret would be held after the end is, leaves none: the ended session holds no
// secret again.
func TestARegistrationHeldPastTheEndLeavesNoSecret(t *testing.T) {
	t.Parallel()
	st, m, session, cred, epoch, g1 := gatedAgy(t)
	committed, release := st.holdNext()
	done := make(chan error, 1)
	go func() {
		_, err := m.SetEndpointWith(context.Background(), cred, "agy", agyLoc, "tok-two")
		done <- err
	}()
	<-committed
	if err := m.Exit(context.Background(), "autodoc:aaaa", "run-a", epoch, session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for g := g1; g <= g1+1; g++ {
		if got, ok := m.EndpointSecret(session, g); ok {
			t.Errorf("the ended session's generation %d holds %q", g, got)
		}
	}
}
