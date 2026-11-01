package tui

import (
	"errors"
	"slices"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/inbox"
)

// elsewhereRig is messagingRig with the window switched to another workspace after agent one
// started in kb: one runs on in kb, the window shows other (docs/decisions/crew.md#the-users-own-sends).
func elsewhereRig(t *testing.T) (*agents, *identityHost, *crewWindow) {
	t.Helper()
	a, c, w := messagingRig(t)
	c.ws = wsInfo{id: 8, name: "other", root: t.TempDir()}
	c.epoch++
	if one := a.instances["one"]; one == nil || !one.running || one.workspace.name != "kb" {
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
			scope, _, _ := a.crew.held()
			var sends [][]any
			w.inboxSend = func(p []any) (any, error) { sends = append(sends, p); return map[string]any{"message": "m1"}, nil }
			var heard []error
			done := func(err error) { heard = append(heard, err) }
			var err error
			if via == "HandTask" {
				err = a.HandTask(AgentRef{Codename: "one"}, TaskRef{Title: "Re-sync", Path: "/cyber/.todo-list/in-progress/t1.md", ID: "t1"}, "Go.", done)
			} else {
				err = a.SendText(AgentRef{Codename: "one"}, "hello one", done)
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
			if len(c.opened)+len(c.panel.modes) != 0 {
				t.Fatalf("brought another workspace's agent forward: opened %q, modes %q", c.opened, c.panel.modes)
			}
		})
	}
}

// The same task rung into an agent in the window's own workspace is brought forward, as before:
// what the cell above refuses is the other workspace, not the presenting.
func TestARungTaskInTheWindowsWorkspaceIsPresented(t *testing.T) {
	a, c, w := messagingRig(t)
	w.inboxSend = func([]any) (any, error) { return map[string]any{"message": "m1"}, nil }
	var heard []error
	if err := a.HandTask(AgentRef{Codename: "one"}, TaskRef{Title: "Ship", Path: "/kb/.todo-list/in-progress/t1.md", ID: "t1"}, "Go.", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(heard) != 1 || heard[0] != nil || !slices.Contains(c.opened, "agent") {
		t.Fatalf("done heard %v, opened %q: want rung and the agents panel opened on it", heard, c.opened)
	}
}

// A doorbell refused for certain for an agent in another workspace doesn't fall back: the base
// never types into a tab working in another workspace. done hears ErrAgentElsewhere naming kb, with
// the doorbell's refusal, never ErrAgentStale (the agent runs), and nothing is typed (the mutants:
// the fallback typing it; the early stale return).
func TestAnAgentInAnotherWorkspaceIsNeverTyped(t *testing.T) {
	a, c, w := elsewhereRig(t)
	w.inboxSend = func([]any) (any, error) {
		return nil, &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "no wake reaches it"}
	}
	var heard []error
	if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatalf("SendText refused at once: %v", err)
	}
	settleMessaging(c, w)
	if len(c.panel.pastes)+len(c.panel.keys) != 0 {
		t.Fatalf("typed %q and pressed %q into another workspace's agent, want nothing", c.panel.pastes, c.panel.keys)
	}
	var e *agentElsewhere
	if len(heard) != 1 || !errors.As(heard[0], &e) || !errors.Is(heard[0], ErrAgentElsewhere) || errors.Is(heard[0], ErrAgentStale) ||
		e.workspace != "kb" || !errors.Is(e.why, errDoorbellNotSent) {
		t.Fatalf("done heard %v, want one ErrAgentElsewhere (kb) carrying the doorbell's refusal, not stale", heard)
	}
	if agentErr(heard[0]) != "elsewhere" {
		t.Errorf("a script reads %q, want elsewhere", agentErr(heard[0]))
	}
}

// What only the base delivers (a fallback job, or a window with no crew) is refused at once for an
// agent in another workspace, as ErrAgentElsewhere with no doorbell, never stale; nothing is typed.
func TestTheBaseAloneRefusesAnotherWorkspacesAgent(t *testing.T) {
	check := func(t *testing.T, c *identityHost, heard []error) {
		t.Helper()
		var e *agentElsewhere
		if len(heard) != 1 || !errors.As(heard[0], &e) || errors.Is(heard[0], ErrAgentStale) || e.workspace != "kb" || !errors.Is(e.why, errNoDoorbell) {
			t.Fatalf("done heard %v, want one ErrAgentElsewhere (kb, no doorbell)", heard)
		}
		if len(c.panel.pastes)+len(c.panel.keys) != 0 {
			t.Fatalf("typed %q and pressed %q, want nothing", c.panel.pastes, c.panel.keys)
		}
	}
	t.Run("fallback job", func(t *testing.T) {
		a, c, w := elsewhereRig(t)
		var heard []error
		a.deliverFallback("one", "from two: hello", func(err error) { heard = append(heard, err) })
		settleMessaging(c, w)
		check(t, c, heard)
	})
	t.Run("no crew", func(t *testing.T) {
		a, c, w := elsewhereRig(t)
		a.crew = nil
		var heard []error
		err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) })
		if err == nil {
			t.Fatal("SendText with no doorbell accepted a delivery only the base could make")
		}
		heard = append(heard, err)
		settleMessaging(c, w)
		check(t, c, heard)
	})
}
