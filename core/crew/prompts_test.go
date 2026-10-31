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
// prompts being reported now: Claude's whenever it is live, if its host wrote the hooks that report
// them (crew.register's prompts), never assumed without; OpenCode's and
// agy's only while their reporter's heartbeat (crew.attention alive) is within 15 s, by the store's
// clock, and never before one; Codex's never. A session that ended reports none.
func TestPromptsAreReportedWhileTheirReporterIsThere(t *testing.T) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	m := crew.New(crewtest.NewMem(clock.Now))
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
	if _, err := m.Register(ctx, crew.Registration{Scope: "autodoc:w", Run: "host", Epoch: epoch, Codename: "claude-bare",
		Kind: crew.KindClaude, Workspace: "w", Instance: "i-bare", Secret: strings.Repeat("s", 40)}); err != nil {
		t.Fatal(err)
	}
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
	check("just after a heartbeat", map[string]bool{crew.KindClaude + "-one": true, crew.KindOpenCode + "-one": true, crew.KindAgy + "-one": true, crew.KindCodex + "-one": false, "claude-bare": false})
	clock.Advance(15 * time.Second)
	check("15 s on", map[string]bool{crew.KindOpenCode + "-one": true, crew.KindAgy + "-one": true, "claude-bare": false})
	clock.Advance(time.Second)
	if err := m.ReporterAlive(ctx, creds[crew.KindAgy]); err != nil {
		t.Fatal(err)
	}
	check("16 s on, agy's heartbeat again", map[string]bool{crew.KindClaude + "-one": true, crew.KindOpenCode + "-one": false, crew.KindAgy + "-one": true, "claude-bare": false})
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
