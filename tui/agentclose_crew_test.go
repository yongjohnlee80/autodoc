package tui

import (
	"context"
	"testing"
)

// presenceOf is codename's crew session state in the window's scope, as crew.list has it ("" for
// no row).
func presenceOf(t *testing.T, a *agents, w *crewWindow, codename string) string {
	t.Helper()
	scope, _, _ := a.crew.held()
	entries, err := w.d.m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Codename == codename && e.Scope == scope {
			return e.Presence
		}
	}
	return ""
}

// An agent the window takes away while it runs ends its crew session, as one whose process exits
// does: Close (answered yes), Restart (the old session ends, the new one is live) and the strip's
// stop-all. A removed tab never reports its process's exit (the terminal's pump stops with the tab),
// so the window can't wait for one: without crew.exit here the session stays live and reachable
// with no process behind it, its codename taken (a ghost, cloud-strife's finding 4, 2026-11-01).
func TestAnAgentTheWindowTakesAwayEndsItsCrewSession(t *testing.T) {
	for _, how := range []string{"close", "restart", "stop all"} {
		t.Run(how, func(t *testing.T) {
			a, c, w := messagingRig(t)
			before := a.instances["one"].session.ID
			if before == "" || presenceOf(t, a, w, "one") != "live" {
				t.Fatalf("agent one has no live crew session: %q, %q", before, presenceOf(t, a, w, "one"))
			}
			asked := len(c.questions)
			switch how {
			case "close":
				a.closeAgent("one")
			case "restart":
				a.restartAgent("one")
			case "stop all":
				a.stopAllAgents()
			}
			if len(c.questions) != asked+1 {
				t.Fatalf("%s of a running agent asked %d questions, want one", how, len(c.questions)-asked)
			}
			c.questions[asked].OnYes()
			settleMessaging(c, w)
			if calledTimes(w, "crew.exit") == 0 {
				t.Errorf("%s: no crew.exit sent for session %s", how, before)
			}
			got := presenceOf(t, a, w, "one")
			switch how {
			case "restart":
				fresh := a.instances["one"]
				if fresh == nil || fresh.session.ID == "" || fresh.session.ID == before || got != "live" {
					t.Errorf("restart: the new instance %+v, presence %q; want a new live session, not %s", fresh, got, before)
				}
				if ended := w.d.sessionEnded(t, before); !ended {
					t.Errorf("restart: the old session %s is still live", before)
				}
			default:
				if got == "live" {
					t.Errorf("%s: agent one's session is still %q with no process behind it, want it ended", how, got)
				}
			}
		})
	}
}

// A Restart registers its new session only once the daemon has answered the old one's crew.exit:
// registered before, it would find the codename still taken (crew.codename_busy) and start nothing.
// Every answer but the exit's is let through (the restart's own setup among them): nothing is
// registered until the exit's answer comes, and then the new session is.
func TestARestartRegistersAfterTheOldSessionsExit(t *testing.T) {
	a, c, w := messagingRig(t)
	before := a.instances["one"].session.ID
	registered := calledTimes(w, "crew.register")
	a.restartAgent("one")
	w.hold, w.late = true, true // calls run at once, their answers wait
	callsBefore := len(w.calls)
	c.questions[len(c.questions)-1].OnYes()
	c.drainPosts()
	if len(w.held) == 0 || len(w.calls) == callsBefore || w.calls[callsBefore] != "crew.exit" {
		t.Fatalf("Restart's first call %v, want crew.exit first", w.calls[callsBefore:])
	}
	exitAnswer, rest := w.held[0], w.held[1:]
	w.held = nil
	for len(rest) > 0 { // every other answer, and what each sets off, while the exit's waits
		for _, f := range rest {
			f()
		}
		c.drainPosts()
		rest, w.held = w.held, nil
	}
	if calledTimes(w, "crew.register") != registered {
		t.Fatalf("a new session was registered while the old one's exit had no answer: calls %v", w.calls[callsBefore:])
	}
	exitAnswer()
	w.hold, w.late = false, false
	for len(w.held) > 0 {
		h := w.held
		w.held = nil
		for _, f := range h {
			f()
		}
	}
	settleMessaging(c, w)
	fresh := a.instances["one"]
	if calledTimes(w, "crew.register") != registered+1 || fresh == nil || fresh.session.ID == "" || fresh.session.ID == before ||
		!w.d.sessionEnded(t, before) || presenceOf(t, a, w, "one") != "live" {
		t.Fatalf("after the exit's answer: registers %d (want %d), instance %+v; the old session ended %v, presence %q",
			calledTimes(w, "crew.register"), registered+1, fresh, w.d.sessionEnded(t, before), presenceOf(t, a, w, "one"))
	}
}
