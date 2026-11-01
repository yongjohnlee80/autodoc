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
