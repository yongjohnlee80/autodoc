package tui

import (
	"errors"
	"slices"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// messagingRig is crewAgents with agent one started: a window holding a crew scope, one agent
// running in it, its CLI reporting its prompts unless a cell says otherwise.
func messagingRig(t *testing.T) (*agents, *identityHost, *crewWindow) {
	t.Helper()
	a, c, w := crewAgents(t, oneTwo)
	p, _ := a.profile("one")
	if instance, err := a.startInstance(p); err != nil || !instance.running {
		t.Fatalf("start: %v", err)
	}
	a.promptsReported = func(string, bool) bool { return true }
	return a, c, w
}

// settleMessaging runs what the window and the crew host left for later: posted deliveries, the
// base's Enter, the doorbell's asks again (settleRounds rounds of the crew host's timers).
func settleMessaging(c *identityHost, w *crewWindow) {
	for round := 0; ; round++ {
		c.drainPosts()
		c.fireAfters()
		if len(w.timers) == 0 || round == settleRounds {
			return
		}
		due := w.timers
		w.timers = nil
		for _, f := range due {
			f()
		}
	}
}

// A doorbell that rings: the message is mailed from the user through inbox.send with fire, to the
// agent's codename in the window's scope, kind and subject as sent; done hears nil once, and
// nothing is typed into its terminal (the mutant: the doorbell skipped, the base typing it).
func TestAMessageRingsTheAgentsDoorbell(t *testing.T) {
	a, c, w := messagingRig(t)
	scope, epoch, _ := crewOf(a).held()
	var got []any
	w.inboxSend = func(p []any) (any, error) { got = p; return map[string]any{"message": "m1"}, nil }
	var heard []error
	task := feature.TaskRef{Title: "Ship", Path: "/kb/.todo-list/in-progress/t1.md", ID: "t1"}
	if err := a.HandTask(feature.AgentRef{Codename: "one"}, task, "Go.", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(heard) != 1 || heard[0] != nil {
		t.Fatalf("done heard %v, want one nil", heard)
	}
	if len(got) != 9 || got[0] != scope || got[2] != epoch || func() bool { l, ok := got[3].([]any); return !ok || !slices.Equal(l, []any{"one"}) }() ||
		got[4] != "File: /kb/.todo-list/in-progress/t1.md\nID: t1\nInstruction: Go." || got[6] != MessageRequest ||
		got[7] != "Task: Ship" || got[8] != true || got[5] == "" {
		t.Fatalf("inbox.send %v", got)
	}
	if len(c.panel.pastes) != 0 || len(c.panel.keys) != 0 {
		t.Fatalf("typed %q and pressed %q, want nothing typed", c.panel.pastes, c.panel.keys)
	}
}

// A stopped agent is started with the user's consent and typed to: a session just registered has no
// doorbell yet, so it is never rung (S1: the doorbell runs inside the one delivery).
func TestAStartedAgentIsNotRung(t *testing.T) {
	a, c, w := crewAgents(t, oneTwo)
	a.promptsReported = func(string, bool) bool { return true }
	rang := false
	w.inboxSend = func([]any) (any, error) { rang = true; return map[string]any{"message": "m"}, nil }
	if err := a.SendText(feature.AgentRef{Codename: "two"}, "hi", func(error) {}); err != nil {
		t.Fatal(err)
	}
	if len(c.questions) != 1 {
		t.Fatalf("asked %d questions, want the consent to start two", len(c.questions))
	}
	c.questions[0].OnYes()
	settleMessaging(c, w)
	if rang {
		t.Fatal("an agent just started was rung")
	}
}

// The Enter decision asks the crew whether the agent's own session, in the window's scope, has its
// prompts reported (crew.list's prompts_reported, the daemon's rule): the same codename in another
// scope says nothing, and a session the list doesn't name is not reported (the mutants: the lookup
// skipped; the scope not matched; no session taken as reported).
func TestTheEnterAsksTheCrewWhetherPromptsAreReported(t *testing.T) {
	scopeOf := func(a *agents) string { s, _, _ := crewOf(a).held(); return s }
	for _, c := range []struct {
		name   string
		list   func(scope string) []any
		enter  bool
		notice string
	}{
		{"reported", func(scope string) []any {
			return []any{map[string]any{"scope": "autodoc:other", "codename": "one", "prompts_reported": false},
				map[string]any{"scope": scope, "codename": "one", "prompts_reported": true}}
		}, true, ""},
		{"another scope's", func(scope string) []any {
			return []any{map[string]any{"scope": "autodoc:other", "codename": "one", "prompts_reported": true},
				map[string]any{"scope": scope, "codename": "one", "prompts_reported": false}}
		}, false, "AutoDoc can't see one's permission prompts"},
		{"no session listed", func(string) []any {
			return []any{map[string]any{"scope": "autodoc:other", "codename": "one", "prompts_reported": true}}
		}, false, "AutoDoc can't see one's permission prompts"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, cw, w := messagingRig(t)
			a.promptsReported = nil // the crew's answer decides
			scope := scopeOf(a)
			w.crewList = func() (any, error) { return c.list(scope), nil }
			w.inboxSend = func([]any) (any, error) { return nil, &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "x"} }
			var heard []error
			if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
				t.Fatal(err)
			}
			settleMessaging(cw, w)
			if pressed := len(cw.panel.keys) == 1; pressed != c.enter || len(heard) != 1 {
				t.Fatalf("pressed %q heard %v: want Enter %v", cw.panel.keys, heard, c.enter)
			}
			if c.enter && heard[0] != nil || !c.enter && !errors.Is(heard[0], ErrAgentTypedNotSent) {
				t.Fatalf("heard %v", heard)
			}
			if c.notice != "" && !hasNote(cw.notes, c.notice) {
				t.Fatalf("notes %q", cw.notes)
			}
		})
	}
}

// The crew's own list answers a session of a kind with no prompt reporter (one's) as not reported.
func TestASessionWithNoReporterIsNotReported(t *testing.T) {
	a, cw, w := messagingRig(t)
	var said []bool
	done := false
	crewOf(a).promptsReported("one", func(reported bool) { said = append(said, reported); done = true })
	settleMessaging(cw, w)
	if !done || len(said) != 1 || said[0] {
		t.Fatalf("one, of no reporting kind: %v", said)
	}
}
