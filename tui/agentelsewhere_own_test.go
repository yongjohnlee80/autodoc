package tui

import (
	"errors"
	"slices"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// The same task rung into an agent in the window's own workspace is brought forward, as before:
// what the cell above refuses is the other workspace, not the presenting.
func TestARungTaskInTheWindowsWorkspaceIsPresented(t *testing.T) {
	a, c, w := messagingRig(t)
	w.inboxSend = func([]any) (any, error) { return map[string]any{"message": "m1"}, nil }
	var heard []error
	opened, modes := len(c.opened), len(c.panel.modes) // the start's own: only what the delivery adds counts
	if err := a.HandTask(feature.AgentRef{Codename: "one"}, feature.TaskRef{Title: "Ship", Path: taskFile("/kb/.todo-list/in-progress/t1.md"), ID: "t1"}, "Go.", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(heard) != 1 || heard[0] != nil || !slices.Contains(c.opened[opened:], "agent") || len(c.panel.modes) == modes {
		t.Fatalf("done heard %v, opened %q, modes %q by the delivery: want rung, the agents panel opened on it, in input mode", heard, c.opened[opened:], c.panel.modes[modes:])
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
	if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
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
		err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) })
		if err == nil {
			t.Fatal("SendText with no doorbell accepted a delivery only the base could make")
		}
		heard = append(heard, err)
		settleMessaging(c, w)
		check(t, c, heard)
	})
}
