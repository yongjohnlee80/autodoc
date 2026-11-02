package crew_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// accessRig is a manager over the in-memory store whose host holds a scope, with one agy session
// registered (tifa): the store, so a cell can fail its wakes, and its credential.
func accessRig(t *testing.T) (*crew.Manager, crew.Store, string, string, int64) {
	t.Helper()
	ctx := context.Background()
	store := crewtest.NewMem(crewtest.NewClock().Now)
	m := crew.New(store, crew.WithUID(1000))
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
	return m, store, got.Session, crew.Credential(got.Session, secret), e
}

// TestCrewAccessSaysWhatTheSessionHolds: crew.access answers a live session's identity, its host,
// and its endpoint: none before it registers one, registered after, failing once its wakes fail
// UnreachableAfter times; reading it changes nothing (the generation stays).
func TestCrewAccessSaysWhatTheSessionHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, store, session, cred, _ := accessRig(t)
	a, err := m.AccessOf(ctx, cred, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Session != crew.AccessLive || a.Codename != "tifa" || a.Kind != "agy" || a.Scope != "autodoc:aaaa" ||
		a.Host != "holds" || a.Endpoint != crew.EndpointNone || a.Why != crew.WhyNoEndpoint || a.Generation != 0 {
		t.Fatalf("before an endpoint: %+v, want live tifa, endpoint none (%s)", a, crew.WhyNoEndpoint)
	}
	g, err := m.SetEndpoint(ctx, cred, "agy", agyLoc)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if a, err = m.AccessOf(ctx, cred, ""); err != nil {
			t.Fatal(err)
		}
		if a.Endpoint != crew.EndpointRegistered || a.Generation != g || a.Why != "" {
			t.Fatalf("after its endpoint: %+v, want registered at generation %d", a, g)
		}
	}
	if err := store.Write(ctx, func(tx crew.Tx) error {
		w, _, err := tx.Wake(session)
		if err != nil {
			return err
		}
		w.Session, w.Failures = session, crew.UnreachableAfter
		return tx.PutWake(w)
	}); err != nil {
		t.Fatal(err)
	}
	if a, err = m.AccessOf(ctx, cred, ""); err != nil {
		t.Fatal(err)
	}
	if a.Endpoint != crew.EndpointFailing || !strings.HasPrefix(a.Why, crew.WhyWakesFailing) {
		t.Fatalf("after %d failed wakes: %+v, want failing (%s)", crew.UnreachableAfter, a, crew.WhyWakesFailing)
	}
}

// TestCrewAccessOfAnEndedSessionSaysOnlyThat: a credential whose session ended answers that it
// ended and how, with no endpoint: what `crew reissue` waits on its host's re-join for. A secret
// that matches no session, or a credential of no shape, is bad_credential, as every agent verb's.
func TestCrewAccessOfAnEndedSessionSaysOnlyThat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _, session, cred, e := accessRig(t)
	if _, err := m.SetEndpoint(ctx, cred, "agy", agyLoc); err != nil {
		t.Fatal(err)
	}
	if err := m.Exit(ctx, "autodoc:aaaa", "run-a", e, session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	a, err := m.AccessOf(ctx, cred, "")
	if err != nil {
		t.Fatalf("an ended session's credential: %v, want its state", err)
	}
	if a.Session != crew.AccessEnded || a.Why != crew.EndedExit || a.Endpoint != "" || a.Generation != 0 || a.Codename != "tifa" {
		t.Fatalf("after its exit: %+v, want ended (%s) and no endpoint", a, crew.EndedExit)
	}
	for _, bad := range []string{crew.Credential(session, strings.Repeat("x", 36)), "no-dot", crew.Credential("s-nobody", "x")} {
		if _, err := m.AccessOf(ctx, bad, ""); !errors.Is(err, crew.ErrBadCredential) {
			t.Errorf("credential %q: %v, want ErrBadCredential", bad, err)
		}
	}
}

// TestCrewAccessNeverHoldsACredential: no key of crew.access's answer, nor of its verb's result,
// holds a credential or a secret: only a host writes those, into the slot's own context.
func TestCrewAccessNeverHoldsACredential(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, _, _, cred, _ := accessRig(t)
	if _, err := m.SetEndpointWith(ctx, cred, "agy", agyLoc, "tok-one"); err != nil {
		t.Fatal(err)
	}
	got, err := verbOf(t, m, "crew.access").Call(ctx, []any{cred})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	if keys["session"] != crew.AccessLive {
		t.Fatalf("crew.access: %s, want a live session", b)
	}
	for k, v := range keys {
		if strings.Contains(k, "credential") || strings.Contains(k, "secret") || strings.Contains(k, "token") {
			t.Errorf("crew.access answers %q", k)
		}
		if s, ok := v.(string); ok && (strings.Contains(s, strings.Repeat("s", 32)) || strings.Contains(s, "tok-one")) {
			t.Errorf("crew.access's %q holds a secret: %q", k, s)
		}
	}
}
