package tui

import (
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// elsewhereRig is messagingRig with the window switched to another workspace after agent one
// started in kb: one runs on in kb, the window shows other (docs/decisions/crew.md#the-users-own-sends).
func elsewhereRig(t *testing.T) (*agents, *identityHost, *crewWindow) {
	t.Helper()
	a, c, w := messagingRig(t)
	c.ws = wsInfo{id: 8, name: "other", root: t.TempDir()}
	c.epoch++
	if one := a.instances["one"]; one == nil || !one.running || one.workspace.Name != "kb" {
		t.Fatalf("agent one isn't running in kb: %+v", one)
	}
	return a, c, w
}

// A task for a running agent started in another workspace than the window's is rung all the same:
// one inbox.send, to its codename in the window's scope, with the task; done hears nil once, and
// nothing is typed or pressed into its tab, nor is it brought forward, since its tab works in the
// other workspace (the mutants: the early ErrAgentStale return before the doorbell, restored, ends
// it stale and unsent; a rung task presented opens the agents panel on it).
func TestAnAgentInAnotherWorkspaceIsRung(t *testing.T) {
	for _, via := range []string{"HandTask", "SendText"} {
		t.Run(via, func(t *testing.T) {
			a, c, w := elsewhereRig(t)
			scope, _, _ := crewOf(a).held()
			var sends [][]any
			w.inboxSend = func(p []any) (any, error) { sends = append(sends, p); return map[string]any{"message": "m1"}, nil }
			var heard []error
			done := func(err error) { heard = append(heard, err) }
			opened, modes := len(c.opened), len(c.panel.modes) // the start's own: only what the delivery adds counts
			var err error
			if via == "HandTask" {
				err = a.HandTask(feature.AgentRef{Codename: "one"}, feature.TaskRef{Title: "Re-sync", Path: "/cyber/.todo-list/in-progress/t1.md", ID: "t1"}, "Go.", done)
			} else {
				err = a.SendText(feature.AgentRef{Codename: "one"}, "hello one", done)
			}
			if err != nil {
				t.Fatalf("%s refused at once: %v", via, err)
			}
			settleMessaging(c, w)
			if len(heard) != 1 || heard[0] != nil {
				t.Fatalf("done heard %v, want one nil (rung)", heard)
			}
			if len(sends) != 1 || sends[0][0] != scope || !slices.Equal(sends[0][3].([]any), []any{"one"}) {
				t.Fatalf("inbox.send %v, want one send to one in scope %s", sends, scope)
			}
			if len(c.panel.pastes)+len(c.panel.keys) != 0 {
				t.Fatalf("typed %q and pressed %q into another workspace's agent, want nothing", c.panel.pastes, c.panel.keys)
			}
			if len(c.opened) != opened || len(c.panel.modes) != modes {
				t.Fatalf("brought another workspace's agent forward: opened %q, modes %q", c.opened[opened:], c.panel.modes[modes:])
			}
		})
	}
}
