package crew_test

import (
	"context"
	"errors"
	"strings"
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
