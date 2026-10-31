package tui

import (
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/inbox"
)

// messagingRig is crewAgents with agent one started: a window holding a crew scope, one agent
// running in it.
func messagingRig(t *testing.T) (*agents, *identityHost, *crewWindow, *agentInstance) {
	t.Helper()
	a, c, w := crewAgents(t, oneTwo)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil || !instance.running {
		t.Fatalf("start: %v", err)
	}
	return a, c, w, instance
}

// A doorbell that fires: the message is mailed from the user through inbox.send, with fire, to the
// agent's codename in the window's scope, kind and subject as sent; done hears nil, and nothing is
// typed into its terminal (the mutant: the doorbell skipped, the base typing it).
func TestAMessageRingsTheAgentsDoorbell(t *testing.T) {
	a, c, w, _ := messagingRig(t)
	scope, epoch, _ := a.crew.held()
	var got []any
	w.inboxSend = func(p []any) (any, error) { got = p; return map[string]any{"message": "m1"}, nil }
	var heard []error
	task := TaskRef{Title: "Ship", Path: "/kb/.todo-list/in-progress/t1.md", ID: "t1"}
	if err := a.HandTask(AgentRef{Codename: "one"}, task, "Go.", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	c.fireAfters()
	if len(heard) != 1 || heard[0] != nil {
		t.Fatalf("done heard %v, want one nil", heard)
	}
	if len(got) != 9 || got[0] != scope || got[2] != epoch || len(got[3].([]string)) != 1 || got[3].([]string)[0] != "one" ||
		got[4] != "File: /kb/.todo-list/in-progress/t1.md\nID: t1\nInstruction: Go." || got[6] != MessageRequest ||
		got[7] != "Task: Ship" || got[8] != true || got[5] == "" {
		t.Fatalf("inbox.send %v", got)
	}
	if len(c.panel.pastes) != 0 || len(c.panel.keys) != 0 {
		t.Fatalf("typed %q and pressed %q, want nothing typed", c.panel.pastes, c.panel.keys)
	}
}

// A doorbell that doesn't fire (inbox.unreachable, or any other refusal) falls back to the base: a
// notice says it didn't ring and why, the text is pasted, and Enter follows after submitDelay; done
// hears nil once. The mutants: no fallback (done hears the error); no Enter.
func TestADoorbellThatDoesntFireIsTypedAndSent(t *testing.T) {
	for name, refusal := range map[string]error{
		"unreachable": &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "no wake reaches it"},
		"stale epoch": &golibrpc.Error{Code: -32091, Message: "stale epoch"},
	} {
		t.Run(name, func(t *testing.T) {
			a, c, w, _ := messagingRig(t)
			w.inboxSend = func([]any) (any, error) { return nil, refusal }
			var heard []error
			if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
				t.Fatal(err)
			}
			if len(c.panel.pastes) != 1 || !strings.Contains(c.panel.pastes[0], "hello one") {
				t.Fatalf("pasted %q, want the text once", c.panel.pastes)
			}
			if len(c.panel.keys) != 0 {
				t.Fatalf("Enter pressed with the paste, not after it: %q", c.panel.keys)
			}
			c.fireAfters()
			if len(c.panel.keys) != 1 || c.panel.keys[0] != "\r" {
				t.Fatalf("keys %q, want one Enter after the paste", c.panel.keys)
			}
			if len(heard) != 1 || heard[0] != nil {
				t.Fatalf("done heard %v, want one nil", heard)
			}
			if !slicesContainsPrefix(c.notes, "one's doorbell didn't ring") {
				t.Fatalf("notes %q, want the doorbell's notice", c.notes)
			}
		})
	}
}

// An agent the crew says waits on a permission or blocked prompt is typed to but never sent: an
// Enter would answer its prompt. A notice asks the user to answer it. The mutant: the guard dropped.
func TestNoEnterIsPressedIntoAPrompt(t *testing.T) {
	a, c, w, _ := messagingRig(t)
	w.inboxSend = func([]any) (any, error) { return nil, &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "x"} }
	a.inbox = &crewInbox{waiting: map[string]bool{"one": true}}
	if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(error) {}); err != nil {
		t.Fatal(err)
	}
	c.fireAfters()
	if len(c.panel.pastes) != 1 || len(c.panel.keys) != 0 {
		t.Fatalf("pasted %q, pressed %q: want typed, no Enter", c.panel.pastes, c.panel.keys)
	}
	if !slicesContainsPrefix(c.notes, "one is waiting on a prompt") {
		t.Fatalf("notes %q", c.notes)
	}
}

// An agent not running in the window (stopped) never rings: the base delivers as before, asking to
// start it. The window without a crew scope types as well.
func TestAStoppedAgentIsNotRung(t *testing.T) {
	a, _, w := crewAgents(t, oneTwo)
	rang := false
	w.inboxSend = func([]any) (any, error) { rang = true; return map[string]any{"message": "m"}, nil }
	if a.crewRoutable(AgentRef{Codename: "two"}) {
		t.Fatal("a stopped agent is routable")
	}
	_ = a.SendText(AgentRef{Codename: "two"}, "hi", func(error) {})
	if rang {
		t.Fatal("a stopped agent's doorbell was rung")
	}
}

func slicesContainsPrefix(notes []string, prefix string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}
