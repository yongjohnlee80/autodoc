package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func orphan(scope string, unread int, profiles ...any) map[string]any {
	return map[string]any{"scope": scope, "profiles": profiles, "unread": int64(unread)}
}

// TestTheOrphansAreOfferedOnceAtTheFirstClaim (§4.3, Q2 b): once the window holds its scope, each
// orphan scope is offered in turn: Adopt moves its mail here (crew.adopt from it to the window's
// scope, with the run and epoch), Keep does nothing, Discard fails it (crew.discard); each answer
// says how it went, and the next is offered. A reconnect offers nothing again.
func TestTheOrphansAreOfferedOnceAtTheFirstClaim(t *testing.T) {
	w := &crewWindow{d: newCrewDaemonForHost()}
	w.d.orphans = []any{orphan("autodoc:dead1", 3, "gold", "jarvis"), orphan("autodoc:dead2", 1, "lector"), orphan("autodoc:dead3", 2, "zen")}
	h := newCrewHost(w, t.TempDir())
	h.Connected()
	scope, epoch, _ := h.held()
	if len(w.questions) != 1 || !strings.Contains(w.questions[0].Text, "3 unhandled message(s) wait for gold, jarvis") ||
		w.questions[0].Yes != "&Adopt" || w.questions[0].No != "&Keep" || w.questions[0].Other != "&Discard" {
		t.Fatalf("questions %+v; want the first orphan offered, Adopt, Keep or Discard", w.questions)
	}
	w.questions[0].OnYes()
	if len(w.d.settled) != 1 || !slices.Equal(w.d.settled[0], []any{"crew.adopt", "autodoc:dead1", scope, h.run, epoch}) {
		t.Fatalf("the adopt %v; want dead1's mail moved to %s", w.d.settled, scope)
	}
	if len(w.questions) != 2 || !strings.Contains(w.notices[len(w.notices)-1], "Adopted 3") {
		t.Fatalf("after the adopt: %d questions, notices %v", len(w.questions), w.notices)
	}
	w.questions[1].OnCancel() // Keep
	if len(w.d.settled) != 1 || len(w.questions) != 3 {
		t.Fatalf("Keep: settled %v, %d questions; want nothing settled and the next offered", w.d.settled, len(w.questions))
	}
	w.questions[2].OnOther()
	if len(w.d.settled) != 2 || w.d.settled[1][0] != "crew.discard" || w.d.settled[1][1] != "autodoc:dead3" {
		t.Fatalf("the discard %v", w.d.settled)
	}
	asked := slices.Index(w.calls, "crew.orphans")
	h.Connected() // a reconnect
	if slices.Index(w.calls[asked+1:], "crew.orphans") >= 0 || len(w.questions) != 3 {
		t.Fatalf("a reconnect offered the orphans again: calls %v", w.calls)
	}
}

// TestTheWindowsOwnScopeIsNoOrphan: a crash successor that claims back its predecessor's scope
// holds it: its mail is its own, never offered.
func TestTheWindowsOwnScopeIsNoOrphan(t *testing.T) {
	dir := t.TempDir()
	const own = "autodoc:00000000000000aa"
	scopes := filepath.Join(agentBase(dir), ".crew-scopes")
	if err := os.MkdirAll(scopes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scopes, own), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w := &crewWindow{d: newCrewDaemonForHost()}
	w.d.orphans = []any{orphan(own, 4, "gold")}
	h := newCrewHost(w, dir)
	h.Connected()
	if scope, _, _ := h.held(); scope != own {
		t.Fatalf("the window claimed %s, want its predecessor's %s", scope, own)
	}
	if len(w.questions) != 0 {
		t.Fatalf("the window's own scope was offered: %+v", w.questions)
	}
}

// TestAnOrphanNotSettledSaysSoAndGoesOn: an adopt the daemon refuses says why, and the next orphan
// is still offered.
func TestAnOrphanNotSettledSaysSoAndGoesOn(t *testing.T) {
	w := &crewWindow{d: newCrewDaemonForHost()}
	w.d.orphans = []any{orphan("autodoc:dead1", 1, "gold"), orphan("autodoc:dead2", 1, "lector")}
	w.d.settleErr = errors.New("the store is unavailable")
	h := newCrewHost(w, t.TempDir())
	h.Connected()
	w.questions[0].OnYes()
	if !strings.Contains(w.notices[len(w.notices)-1], "was not settled") || len(w.questions) != 2 {
		t.Fatalf("notices %v, %d questions; want the failure said and the next offered", w.notices, len(w.questions))
	}
}
