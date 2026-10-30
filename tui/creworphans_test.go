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

// shown is the question the card shows, "" when none is open.
func (w *crewWindow) shown() string {
	if w.card == nil || !w.card.pending {
		return ""
	}
	return w.card.current.Title + ": " + w.card.current.Text
}

// TestTheOrphansAreOfferedOnceAtTheFirstClaim
// (docs/decisions/crew.md#orphan-scopes-are-offered-to-the-user): once the window holds its scope,
// each orphan scope is offered in turn: Adopt moves its mail here (crew.adopt from it to the
// window's scope, with the run and epoch), Keep does nothing, Discard fails it (crew.discard); each
// answer says how it went, and the next is offered. A reconnect offers nothing again.
func TestTheOrphansAreOfferedOnceAtTheFirstClaim(t *testing.T) {
	w := &crewWindow{d: newCrewDaemonForHost()}
	w.d.orphans = []any{orphan("autodoc:dead1", 3, "gold", "jarvis"), orphan("autodoc:dead2", 1, "lector"), orphan("autodoc:dead3", 2, "zen")}
	h := newCrewHost(w, t.TempDir())
	h.Connected()
	scope, epoch, _ := h.held()
	if q := w.card.current; !strings.Contains(w.shown(), "3 unhandled message(s) wait for gold, jarvis") ||
		q.Yes != "&Adopt" || q.No != "&Keep" || q.Other != "&Discard" {
		t.Fatalf("the card shows %q (%+v); want the first orphan offered, Adopt, Keep or Discard", w.shown(), q)
	}
	w.card.accepted()
	w.card.closed()
	if len(w.d.settled) != 1 || !slices.Equal(w.d.settled[0], []any{"crew.adopt", "autodoc:dead1", scope, h.run, epoch}) {
		t.Fatalf("the adopt %d calls; want dead1's mail moved to %s", len(w.d.settled), scope)
	}
	if !strings.Contains(w.notices[len(w.notices)-1], "Adopted 3") || !strings.Contains(w.shown(), "autodoc:dead2") {
		t.Fatalf("after the adopt: notices %v, the card shows %q", w.notices, w.shown())
	}
	w.card.rejected() // Keep
	w.card.closed()
	if len(w.d.settled) != 1 || !strings.Contains(w.shown(), "autodoc:dead3") {
		t.Fatalf("Keep: %d settled, the card shows %q; want nothing settled and the next offered", len(w.d.settled), w.shown())
	}
	w.card.otherChosen()
	w.card.closed()
	if len(w.d.settled) != 2 || w.d.settled[1][0] != "crew.discard" || w.d.settled[1][1] != "autodoc:dead3" || w.shown() != "" {
		t.Fatalf("the discard: %d settled, the card shows %q", len(w.d.settled), w.shown())
	}
	asked := slices.Index(w.calls, "crew.orphans")
	h.Connected() // a reconnect
	if slices.Index(w.calls[asked+1:], "crew.orphans") >= 0 || w.shown() != "" {
		t.Fatalf("a reconnect offered the orphans again: calls %v", w.calls)
	}
}

// TestAnOfferWaitsForTheUsersQuestion: orphans listed while the user answers another
// question wait for it; it is never displaced.
func TestAnOfferWaitsForTheUsersQuestion(t *testing.T) {
	w := &crewWindow{d: newCrewDaemonForHost(), card: newAsker(newScripted("kb"))}
	w.d.orphans = []any{orphan("autodoc:dead1", 1, "gold")}
	yes, cancelled := 0, 0
	w.card.ask(Question{Title: "remove the provider?", OnYes: func() { yes++ }, OnCancel: func() { cancelled++ }})
	h := newCrewHost(w, t.TempDir())
	h.Connected() // its orphans arrive while the user's question is open
	if !strings.HasPrefix(w.shown(), "remove the provider?") || cancelled != 0 {
		t.Fatalf("the card shows %q, the user's question cancelled %d times; want it left open", w.shown(), cancelled)
	}
	w.card.accepted()
	w.card.closed()
	if yes != 1 || !strings.Contains(w.shown(), "autodoc:dead1") {
		t.Fatalf("after the user's answer: yes %d, the card shows %q; want the orphan offered then", yes, w.shown())
	}
}

// TestAQuestionOverAnOfferPutsItBack: a question asked while orphan A is offered and
// B waits is shown and survives; A is not answered (nothing settled, no Keep run), and A and then B
// are offered again once the question is answered.
func TestAQuestionOverAnOfferPutsItBack(t *testing.T) {
	w := &crewWindow{d: newCrewDaemonForHost()}
	w.d.orphans = []any{orphan("autodoc:deadA", 1, "gold"), orphan("autodoc:deadB", 1, "lector")}
	h := newCrewHost(w, t.TempDir())
	h.Connected()
	if !strings.Contains(w.shown(), "autodoc:deadA") {
		t.Fatalf("the card shows %q, want A", w.shown())
	}
	yes, cancelled := 0, 0
	w.card.ask(Question{Title: "save the page?", OnYes: func() { yes++ }, OnCancel: func() { cancelled++ }})
	if !strings.HasPrefix(w.shown(), "save the page?") || len(w.d.settled) != 0 {
		t.Fatalf("the card shows %q, %d settled; want the user's question, A untouched", w.shown(), len(w.d.settled))
	}
	w.card.accepted()
	w.card.closed()
	if yes != 1 || cancelled != 0 || !strings.Contains(w.shown(), "autodoc:deadA") {
		t.Fatalf("after the user's answer: yes %d cancelled %d, the card shows %q; want A offered again", yes, cancelled, w.shown())
	}
	w.card.rejected() // Keep A
	w.card.closed()
	if !strings.Contains(w.shown(), "autodoc:deadB") || len(w.d.settled) != 0 {
		t.Fatalf("after A: the card shows %q, %d settled; want B, nothing settled", w.shown(), len(w.d.settled))
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
	if w.shown() != "" {
		t.Fatalf("the window's own scope was offered: %q", w.shown())
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
	w.card.accepted()
	w.card.closed()
	if !strings.Contains(w.notices[len(w.notices)-1], "was not settled") || !strings.Contains(w.shown(), "autodoc:dead2") {
		t.Fatalf("notices %v, the card shows %q; want the failure said and the next offered", w.notices, w.shown())
	}
}
