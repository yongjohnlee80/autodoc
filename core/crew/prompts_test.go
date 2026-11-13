package crew_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// TestPromptsAreReportedWhileTheirReporterIsThere: crew.list's prompts_reported is a live session's
// prompts being reported now: Claude's whenever it is live and started (its endpoint registered:
// TestAClaudesPromptsAreReportedOnlyOnceItStarted), if its host wrote the hooks that report them
// (crew.register's prompts), never assumed without; OpenCode's, agy's and Codex's only while
// their reporter's heartbeat (crew.attention alive) is within 15 s, by the store's clock, and never
// before one. A session that ended reports none.
func TestPromptsAreReportedWhileTheirReporterIsThere(t *testing.T) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	st := crewtest.NewMem(clock.Now)
	m := crew.New(st)
	epoch, err := m.Claim(ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{}
	for _, kind := range []string{crew.KindClaude, crew.KindOpenCode, crew.KindAgy, crew.KindCodex} {
		secret := strings.Repeat("s", 40)
		r, err := m.Register(ctx, crew.Registration{Scope: "autodoc:w", Run: "host", Epoch: epoch, Codename: kind + "-one",
			Kind: kind, Workspace: "w", Instance: "i-" + kind, Secret: secret, Prompts: true})
		if err != nil {
			t.Fatal(err)
		}
		creds[kind] = crew.Credential(r.Session, secret)
	}
	// a Claude Code too old for the prompt hooks: its host wrote SessionStart alone
	bare, err := m.Register(ctx, crew.Registration{Scope: "autodoc:w", Run: "host", Epoch: epoch, Codename: "claude-bare",
		Kind: crew.KindClaude, Workspace: "w", Instance: "i-bare", Secret: strings.Repeat("s", 40)})
	if err != nil {
		t.Fatal(err)
	}
	started(t, st, sessionOf(t, creds[crew.KindClaude]), bare.Session) // both Claudes past their startup
	reported := func() map[string]bool {
		t.Helper()
		es, err := m.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, e := range es {
			out[e.Codename] = e.PromptsReported
		}
		return out
	}
	check := func(when string, want map[string]bool) {
		t.Helper()
		got := reported()
		for codename, w := range want {
			if got[codename] != w {
				t.Errorf("%s: %s's prompts reported %v, want %v", when, codename, got[codename], w)
			}
		}
	}
	check("before any heartbeat", map[string]bool{crew.KindClaude + "-one": true, crew.KindOpenCode + "-one": false, crew.KindAgy + "-one": false, crew.KindCodex + "-one": false, "claude-bare": false})
	for _, kind := range []string{crew.KindOpenCode, crew.KindAgy, crew.KindCodex} {
		if err := m.ReporterAlive(ctx, creds[kind]); err != nil {
			t.Fatal(err)
		}
	}
	check("just after a heartbeat", map[string]bool{crew.KindClaude + "-one": true, crew.KindOpenCode + "-one": true, crew.KindAgy + "-one": true, crew.KindCodex + "-one": true, "claude-bare": false})
	clock.Advance(15 * time.Second)
	check("15 s on", map[string]bool{crew.KindOpenCode + "-one": true, crew.KindAgy + "-one": true, crew.KindCodex + "-one": true, "claude-bare": false})
	clock.Advance(time.Second)
	if err := m.ReporterAlive(ctx, creds[crew.KindAgy]); err != nil {
		t.Fatal(err)
	}
	check("16 s on, agy's heartbeat again", map[string]bool{crew.KindClaude + "-one": true, crew.KindOpenCode + "-one": false, crew.KindAgy + "-one": true, crew.KindCodex + "-one": false, "claude-bare": false})
	if err := m.ReporterAlive(ctx, "nobody.secret"); err == nil {
		t.Fatal("a heartbeat with no session's credential was taken")
	}
	// a session that ends takes its heartbeat with it, and gives no more
	if n := crew.AliveReporters(m); n != 3 {
		t.Fatalf("%d heartbeats held, want the three given", n)
	}
	if err := m.Exit(ctx, "autodoc:w", "host", epoch, sessionOf(t, creds[crew.KindAgy]), crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	if n := crew.AliveReporters(m); n != 2 {
		t.Fatalf("%d heartbeats held after a session ended, want its own gone", n)
	}
	if err := m.ReporterAlive(ctx, creds[crew.KindAgy]); err == nil || crew.AliveReporters(m) != 2 {
		t.Fatal("an ended session's reporter was heard")
	}
}

// sessionOf is the session id of credential "<session>.<secret>".
func sessionOf(t *testing.T, credential string) string {
	t.Helper()
	id, _, ok := strings.Cut(credential, ".")
	if !ok {
		t.Fatalf("credential %q", credential)
	}
	return id
}

// started gives each session an endpoint, as a Claude's SessionStart hook registers once Claude
// Code is past its startup.
func started(t *testing.T, st crew.Store, sessions ...string) {
	t.Helper()
	if err := st.Write(context.Background(), func(tx crew.Tx) error {
		for _, s := range sessions {
			if err := tx.PutEndpoint(crew.Endpoint{Session: s, Generation: 1, Kind: crew.KindClaude}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAClaudesPromptsAreReportedOnlyOnceItStarted: a Claude session's hooks report its prompts
// only once Claude Code runs them, past its startup: before its SessionStart hook registered its
// endpoint it may be showing its folder-trust question, which no hook reports, so a host pressing
// Enter there would answer it ("No, exit" is its default). Its registration's prompts alone report
// nothing; its endpoint registered, they are reported; its session ended, none again.
func TestAClaudesPromptsAreReportedOnlyOnceItStarted(t *testing.T) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	st := crewtest.NewMem(clock.Now)
	// the endpoint row is written to the store (started): what is checked is the start, the same on
	// every OS, so the crew can wake through a socket here whatever this OS offers
	m := crew.New(st, crew.WithSocketWakes(true))
	epoch, err := m.Claim(ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Register(ctx, crew.Registration{Scope: "autodoc:w", Run: "host", Epoch: epoch, Codename: "gold",
		Kind: crew.KindClaude, Workspace: "w", Instance: "i1", Secret: strings.Repeat("s", 40), Prompts: true})
	if err != nil {
		t.Fatal(err)
	}
	reported := func() (bool, string) {
		t.Helper()
		es, err := m.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			if e.Codename == "gold" {
				return e.PromptsReported, e.Why
			}
		}
		t.Fatal("gold isn't listed")
		return false, ""
	}
	if got, why := reported(); got || why != crew.WhyNoEndpoint {
		t.Fatalf("registered with its hooks, not started: prompts reported %v (why %q), want false (no_endpoint)", got, why)
	}
	started(t, st, r.Session)
	if got, why := reported(); !got || why != "" {
		t.Fatalf("started: prompts reported %v (why %q), want true", got, why)
	}
	if err := m.Exit(ctx, "autodoc:w", "host", epoch, r.Session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	if got, _ := reported(); got {
		t.Fatal("an ended session's prompts are reported")
	}
}
