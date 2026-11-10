package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

func claudeRig(t *testing.T) (*agents, *identityHost, *crewWindow, *agentInstance) {
	t.Helper()
	a, c, w := crewAgents(t, claudeOne)
	p, _ := a.profile("cl")
	instance, err := a.startInstance(p)
	if err != nil || !instance.running {
		t.Fatalf("start: %v", err)
	}
	a.promptsReported = func(string, bool) bool { return true }
	return a, c, w, instance
}

// started gives codename's live session an endpoint, as its SessionStart hook registers once
// Claude Code is past its startup.
func (d *crewDaemon) started(t *testing.T, codename string) {
	t.Helper()
	if err := d.mem.Write(context.Background(), func(tx crew.Tx) error {
		profiles, err := tx.ActiveProfiles()
		if err != nil {
			return err
		}
		for _, p := range profiles {
			if p.Codename != codename {
				continue
			}
			s, live, err := tx.LiveSession(p.ID)
			if err != nil || !live {
				return err
			}
			return tx.PutEndpoint(crew.Endpoint{Session: s.ID, Generation: 1, Kind: p.Kind})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A Claude the crew can't vouch for is never typed into: when crew.list fails, or lists no session
// of its codename in the window's scope (another scope's agent of that name says nothing), it isn't
// known to have started, so nothing is pasted or pressed (the mutants: a failed list, or a row of
// another scope, taken for started).
func TestAClaudeTheCrewCantVouchForIsNotTypedInto(t *testing.T) {
	for _, c := range []struct {
		name string
		list func(w *crewWindow, scope string)
	}{
		{"crew.list fails", func(w *crewWindow, _ string) { w.down = map[string]int{"crew.list": 1} }},
		{"only another scope's cl", func(w *crewWindow, _ string) {
			w.crewList = func() (any, error) {
				return []any{map[string]any{"scope": "autodoc:other", "codename": "cl", "reachable": true}}, nil
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, cw, w, _ := claudeRig(t)
			scope, _, _ := crewOf(a).held()
			c.list(w, scope)
			var heard []error
			a.deliverFallback("cl", "hello cl", func(err error) { heard = append(heard, err) })
			settleMessaging(cw, w)
			if len(cw.panel.pastes) != 0 || len(cw.panel.keys) != 0 || len(heard) != 1 || heard[0] == nil {
				t.Fatalf("pasted %q, pressed %q, heard %v: want nothing typed, the delivery ended unsent", cw.panel.pastes, cw.panel.keys, heard)
			}
		})
	}
}

// A window that holds no crew scope (lost with its connection) can't ask the crew whether its Claude
// has started, nor mail it: the user's send types nothing, and ends unsent, saying why (the
// mutants: no scope taken for started; the send queued with no scope to send from).
func TestAWindowWithNoScopeTypesNothingIntoItsClaude(t *testing.T) {
	a, c, w, _ := claudeRig(t)
	crewOf(a).scope = "" // the scope lost; the agent's session is still the window's record of it
	var heard []error
	if err := a.SendText(feature.AgentRef{Codename: "cl"}, "hello cl", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(c.panel.pastes) != 0 || len(c.panel.keys) != 0 {
		t.Fatalf("pasted %q, pressed %q into a Claude with no scope to ask: want nothing", c.panel.pastes, c.panel.keys)
	}
	if len(heard) != 1 || !errors.Is(heard[0], errDoorbellNotSent) {
		t.Fatalf("heard %v: want the send ended unsent, no crew scope to mail it from", heard)
	}
}
