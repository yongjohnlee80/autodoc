package crew_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// TestTheModuleDeclaresItsVerbs: with no store (as --contracts builds it) the module answers its
// name, verbs and errors; every contract passes its own check, has an effect, and names only
// codes the module declares; and the codes are in the module's range (module.CheckCodes).
func TestTheModuleDeclaresItsVerbs(t *testing.T) {
	m := crew.New(nil)
	if err := module.CheckCodes(m); err != nil {
		t.Fatal(err)
	}
	declared := map[int64]bool{}
	for _, e := range m.Errors() {
		declared[e.Code] = true
	}
	seen := map[string]bool{}
	for _, v := range m.Verbs() {
		c := v.Contract
		if err := c.Check(); err != nil {
			t.Errorf("%s: %v", c.Verb, err)
		}
		if c.Effect == module.EffectUnset {
			t.Errorf("%s has no effect", c.Verb)
		}
		if seen[c.Verb] {
			t.Errorf("%s declared twice", c.Verb)
		}
		seen[c.Verb] = true
		for _, code := range c.Errors {
			if !declared[code] {
				t.Errorf("%s answers %d, which Errors does not declare", c.Verb, code)
			}
		}
	}
	for _, want := range []string{"crew.claim", "crew.takeover", "crew.release", "crew.register", "crew.renew", "crew.exit",
		"crew.whoami", "crew.list", "crew.orphans", "crew.adopt", "crew.discard", "crew.endpoint", "crew.attention",
		"crew.guide"} {
		if !seen[want] {
			t.Errorf("no %s", want)
		}
	}
}

// verbOf is the module's verb named name.
func verbOf(t *testing.T, m *crew.Manager, name string) module.Verb {
	t.Helper()
	for _, v := range m.Verbs() {
		if v.Verb == name {
			return v
		}
	}
	t.Fatalf("no %s", name)
	return module.Verb{}
}

// verbRig is a manager over the in-memory store whose host holds scope at epoch, with one session
// registered (jarvis): its id and its credential.
type verbRig struct {
	m       *crew.Manager
	store   crew.Store
	enders  *crewtest.Enders
	epoch   int64
	session string
	cred    string
}

const verbScope = "autovim:plugins"

func newVerbRig(t *testing.T) verbRig {
	t.Helper()
	ctx := context.Background()
	store, enders := crewtest.NewMem(crewtest.NewClock().Now), &crewtest.Enders{}
	m := crew.New(store, crew.WithSessionEnder(enders))
	e, err := m.Claim(ctx, verbScope, "run-a", "")
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 32) + "agent-1"
	got, err := m.Register(ctx, crew.Registration{Scope: verbScope, Run: "run-a", Epoch: e, Codename: "jarvis", Kind: "claude",
		Workspace: "ws-1", Instance: "agent-1", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return verbRig{m: m, store: store, enders: enders, epoch: e, session: got.Session, cred: got.Session + "." + secret}
}

// attention is the session's attention row, and whether it has one.
func (r verbRig) attention(t *testing.T) (crew.Attention, bool) {
	t.Helper()
	var a crew.Attention
	var ok bool
	if err := r.store.Read(context.Background(), func(tx crew.Tx) error {
		var err error
		a, ok, err = tx.Attention(r.session)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return a, ok
}

// TestTheAttentionVerbRoutesItsOp: crew.attention raise raises the session's attention with its
// kind and text, clear clears it, and any other op is refused as invalid, touching nothing.
func TestTheAttentionVerbRoutesItsOp(t *testing.T) {
	ctx := context.Background()
	r := newVerbRig(t)
	attention := verbOf(t, r.m, "crew.attention")
	if _, err := attention.Call(ctx, []any{r.cred, "toggle", crew.AttentionBlocked, "waiting"}); !errors.Is(err, crew.ErrInvalid) {
		t.Fatalf("op toggle: %v, want ErrInvalid", err)
	}
	if a, ok := r.attention(t); ok {
		t.Fatalf("the refused op left an attention: %+v", a)
	}
	if _, err := attention.Call(ctx, []any{r.cred, "raise", crew.AttentionPermission, "may I write?"}); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if a, ok := r.attention(t); !ok || a.Kind != crew.AttentionPermission || a.Text != "may I write?" || a.Cleared != nil {
		t.Fatalf("after raise: %+v, %v; want a raised permission saying its text", a, ok)
	}
	if _, err := attention.Call(ctx, []any{r.cred, "clear"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if a, ok := r.attention(t); !ok || a.Cleared == nil {
		t.Fatalf("after clear: %+v, %v; want it cleared", a, ok)
	}
}

// TestTheExitVerbDefaultsItsWhy: crew.exit with no why ends the session as an exit, which the mail
// module hears; a why given is kept (start_failed).
func TestTheExitVerbDefaultsItsWhy(t *testing.T) {
	ctx := context.Background()
	r := newVerbRig(t)
	if _, err := verbOf(t, r.m, "crew.exit").Call(ctx, []any{verbScope, "run-a", r.epoch, r.session}); err != nil {
		t.Fatalf("exit with no why: %v", err)
	}
	if len(r.enders.Ended) != 1 || r.enders.Ended[0] != (crewtest.EndedSession{Session: r.session, Why: crew.EndedExit}) {
		t.Fatalf("the mail module heard %v, want %s ended %q", r.enders.Ended, r.session, crew.EndedExit)
	}

	r = newVerbRig(t)
	if _, err := verbOf(t, r.m, "crew.exit").Call(ctx, []any{verbScope, "run-a", r.epoch, r.session, crew.EndedStartFailed}); err != nil {
		t.Fatalf("exit, start_failed: %v", err)
	}
	if len(r.enders.Ended) != 1 || r.enders.Ended[0].Why != crew.EndedStartFailed {
		t.Fatalf("the mail module heard %v, want %q", r.enders.Ended, crew.EndedStartFailed)
	}
}

// TestTheGuideAndListVerbsAnswerTheManagers: crew.guide answers the kind's guide (GuideFor, not
// another kind's), and crew.list the addressbook, its one session's codename in its scope.
func TestTheGuideAndListVerbsAnswerTheManagers(t *testing.T) {
	ctx := context.Background()
	r := newVerbRig(t)
	got, err := verbOf(t, r.m, "crew.guide").Call(ctx, []any{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	want := r.m.GuideFor("codex")
	if got != want {
		t.Fatalf("crew.guide codex: %+v, want GuideFor(codex)", got)
	}
	if want == r.m.GuideFor("claude") {
		t.Fatal("the codex and claude guides are alike: the cell cannot tell the kind was passed")
	}
	got, err = verbOf(t, r.m, "crew.list").Call(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := got.([]crew.Entry)
	if !ok || len(entries) != 1 || entries[0].Codename != "jarvis" || entries[0].Scope != verbScope || entries[0].Kind != "claude" {
		t.Fatalf("crew.list: %#v, want jarvis in %s", got, verbScope)
	}
}
