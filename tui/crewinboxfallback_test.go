package tui

import (
	"strings"
	"testing"
)

// fbChange is a feed answer with one change of kind naming job.
func fbChange(cursor int64, kind, job string) map[string]any {
	return map[string]any{"cursor": cursor, "changes": []any{map[string]any{"seq": cursor, "kind": kind, "ref": job}}}
}

func fbJob(state string, present bool) map[string]any {
	return map[string]any{"job": "j1", "state": state, "recipient": "gold", "scope": "autovim:alpha", "host_present": present,
		"from": "jarvis", "text": "Mail from jarvis (by your host: the crew couldn't deliver it). Answer with autodoc mail send jarvis.\nhello gold"}
}

// TestAJobWaitingForItsAbsentHostShowsUntilItsHostTakesIt (docs/decisions/crew.md#the-fallback-exactly-once):
// a fallback change for an open job whose host is away shows a lasting toast naming the sender, the
// recipient and its scope; a snapshot read again keeps it; the job's fallback_done ends it (the
// mutants: a snapshot closing it; never ended).
func TestAJobWaitingForItsAbsentHostShowsUntilItsHostTakesIt(t *testing.T) {
	w := newInboxWindow()
	w.jobs = map[string]map[string]any{"j1": fbJob("open", false)}
	w.answers = []any{snapshot(5, "", nil, nil), fbChange(6, "fallback", "j1"), changes(7, "answered"), snapshot(7, "", nil, nil),
		fbChange(8, "fallback_done", "j1")}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t) // the snapshot
	w.step(t) // the fallback change
	w.step(t) // its job read
	text := w.shown[fallbackToast("j1")]
	if !strings.Contains(text, "jarvis") || !strings.Contains(text, "gold") || !strings.Contains(text, "autovim:alpha") {
		t.Fatalf("toasts %v, want j1 waiting for its host", w.shown)
	}
	w.step(t) // a change that reads the snapshot again
	w.step(t) // the snapshot, without the job
	if _, open := w.shown[fallbackToast("j1")]; !open {
		t.Fatalf("after a snapshot: toasts %v, want j1 still waiting", w.shown)
	}
	w.jobs["j1"] = fbJob("done", true)
	w.step(t) // the fallback_done change
	w.step(t) // its job read
	if _, open := w.shown[fallbackToast("j1")]; open || w.ended[fallbackToast("j1")] == "" {
		t.Fatalf("after fallback_done: shown %v, ended %v; want j1's wait ended", w.shown, w.ended)
	}
}

// A job whose host holds its scope shows nothing: its host delivers it.
func TestAJobWhoseHostIsHereShowsNothing(t *testing.T) {
	w := newInboxWindow()
	w.jobs = map[string]map[string]any{"j1": fbJob("open", true)}
	w.answers = []any{snapshot(5, "", nil, nil), fbChange(6, "fallback", "j1")}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	w.step(t)
	w.step(t)
	if len(w.shown) != 0 || len(w.notes) != 0 {
		t.Fatalf("shown %v, notes %v; want nothing", w.shown, w.notes)
	}
}

// An unknown outcome gives the user the job's text, in every window but the one whose crew host
// took the job (which told the user itself) (the mutant: the text dropped).
func TestAnUnknownJobGivesTheUserItsText(t *testing.T) {
	for _, took := range []bool{false, true} {
		w := newInboxWindow()
		w.jobs = map[string]map[string]any{"j1": fbJob("unknown", true)}
		w.answers = []any{snapshot(5, "", nil, nil), fbChange(6, "fallback_unknown", "j1")}
		x := newCrewInbox(w, w.raise)
		x.took = func(job string) bool { return took && job == "j1" }
		x.Connected()
		w.step(t)
		w.step(t)
		w.step(t)
		given := len(w.notes) == 1 && strings.Contains(w.notes[0], "hello gold") && strings.Contains(w.notes[0], "gold")
		if took && len(w.notes) != 0 || !took && !given {
			t.Fatalf("took %v: notes %v; want the text given only by a window that didn't take the job", took, w.notes)
		}
	}
}

// A reconnected feed, or a scope that changed hands, reads every waiting job again: one its host
// took meanwhile ends (the mutants: no re-read on reconnect; none on a claim).
func TestAWaitingJobIsReadAgainOnReconnectAndClaim(t *testing.T) {
	for _, how := range []string{"reconnect", "claim"} {
		w := newInboxWindow()
		w.jobs = map[string]map[string]any{"j1": fbJob("leased", false)}
		w.answers = []any{snapshot(5, "", nil, nil), fbChange(6, "fallback", "j1"), snapshot(6, "", nil, nil)}
		x := newCrewInbox(w, w.raise)
		x.Connected()
		w.step(t)
		w.step(t)
		w.step(t)
		if _, open := w.shown[fallbackToast("j1")]; !open {
			t.Fatalf("%s: toasts %v, want j1 waiting", how, w.shown)
		}
		w.jobs["j1"] = fbJob("done", true) // taken while the feed was away: no change reached this window
		w.pending = nil                    // the old connection's watch is gone
		w.answers = []any{snapshot(6, "", nil, nil), snapshot(6, "", nil, nil)}
		if how == "reconnect" {
			x.Connected() // its watch, then the re-read
			w.step(t)     // the snapshot: the wait is kept (a snapshot doesn't carry it)
			w.step(t)     // the re-read
		} else {
			x.recheckFallbacks()
			w.step(t) // the re-read
		}
		if _, open := w.shown[fallbackToast("j1")]; open {
			t.Fatalf("%s: toasts %v, want j1's wait read again and ended", how, w.shown)
		}
	}
}

// A daemon before inbox.fallback shows nothing for a fallback change.
func TestAnOlderDaemonShowsNoFallback(t *testing.T) {
	w := newInboxWindow()
	w.answers = []any{snapshot(5, "", nil, nil), fbChange(6, "fallback", "j1")}
	x := newCrewInbox(w, w.raise)
	x.Connected()
	w.step(t)
	w.step(t)
	w.step(t)
	if len(w.shown) != 0 || len(w.notes) != 0 {
		t.Fatalf("shown %v, notes %v; want nothing", w.shown, w.notes)
	}
}

// Each claim that holds tells the inbox (crewHost.onClaim), so what waited for a host is read again
// when the scope changes hands (the mutant: the claim not told).
func TestAClaimTellsTheInboxToReadWaitingJobsAgain(t *testing.T) {
	w := &crewWindow{d: newCrewDaemonForHost()}
	h := newCrewHost(w, t.TempDir())
	told := 0
	h.onClaim = func() { told++ }
	h.Connected()
	if _, _, ok := h.held(); !ok || told != 1 {
		t.Fatalf("held %v, told %d; want the claim told once", ok, told)
	}
}
