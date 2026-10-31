package tui

import (
	"errors"
	"io"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/inbox"
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
	a.promptsReported = func(string) bool { return true }
	return a, c, w
}

// settle runs what the window and the crew host left for later: posted deliveries, the base's Enter,
// the doorbell's asks again.
func settle(c *identityHost, w *crewWindow) {
	for {
		c.drainPosts()
		c.fireAfters()
		if len(w.timers) == 0 {
			return
		}
		due := w.timers
		w.timers = nil
		for _, f := range due {
			f()
		}
	}
}

func hasNote(notes []string, prefix string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// A doorbell that rings: the message is mailed from the user through inbox.send with fire, to the
// agent's codename in the window's scope, kind and subject as sent; done hears nil once, and
// nothing is typed into its terminal (the mutant: the doorbell skipped, the base typing it).
func TestAMessageRingsTheAgentsDoorbell(t *testing.T) {
	a, c, w := messagingRig(t)
	scope, epoch, _ := a.crew.held()
	var got []any
	w.inboxSend = func(p []any) (any, error) { got = p; return map[string]any{"message": "m1"}, nil }
	var heard []error
	task := TaskRef{Title: "Ship", Path: "/kb/.todo-list/in-progress/t1.md", ID: "t1"}
	if err := a.HandTask(AgentRef{Codename: "one"}, task, "Go.", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settle(c, w)
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

// A doorbell refused for certain (nothing sent: no wake reaches the agent, or a stale epoch) goes on
// to the base: a notice says it didn't ring, the text is pasted, Enter follows submitDelay later,
// and done hears nil once (the mutants: no fallback; no Enter).
func TestADoorbellRefusedForCertainIsTypedAndSent(t *testing.T) {
	for name, refusal := range map[string]error{
		"unreachable": &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "no wake reaches it"},
		"stale epoch": &golibrpc.Error{Code: crew.CodeStaleEpoch, Message: "stale epoch"},
	} {
		t.Run(name, func(t *testing.T) {
			a, c, w := messagingRig(t)
			w.inboxSend = func([]any) (any, error) { return nil, refusal }
			var heard []error
			if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
				t.Fatal(err)
			}
			c.drainPosts()
			if len(c.panel.pastes) != 1 || !strings.Contains(c.panel.pastes[0], "hello one") || len(c.panel.keys) != 0 {
				t.Fatalf("pasted %q, pressed %q: want the text once, the Enter not with it", c.panel.pastes, c.panel.keys)
			}
			settle(c, w)
			if len(c.panel.keys) != 1 || c.panel.keys[0] != "\r" {
				t.Fatalf("keys %q, want one Enter after the paste", c.panel.keys)
			}
			if len(heard) != 1 || heard[0] != nil {
				t.Fatalf("done heard %v, want one nil", heard)
			}
			if !hasNote(c.notes, "one's doorbell didn't ring") {
				t.Fatalf("notes %q, want the doorbell's notice", c.notes)
			}
		})
	}
}

// A doorbell whose outcome is unknown (a lost answer) may have rung: it is asked again with the same
// key, userSendTries times in all, and never typed; done hears the failure (Zen #124 B2). A later
// ask that answers rang ends it sent (the mutants: a fallback on any error; a fresh key per ask).
func TestAnUnknownDoorbellIsAskedAgainNeverTyped(t *testing.T) {
	a, c, w := messagingRig(t)
	var keys []string
	w.inboxSend = func(p []any) (any, error) { keys = append(keys, p[5].(string)); return nil, io.ErrUnexpectedEOF }
	var heard []error
	if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settle(c, w)
	if len(keys) != userSendTries || keys[0] != keys[len(keys)-1] {
		t.Fatalf("asked with keys %q, want %d asks with one key", keys, userSendTries)
	}
	if len(c.panel.pastes) != 0 || len(heard) != 1 || !errors.Is(heard[0], io.ErrUnexpectedEOF) {
		t.Fatalf("pasted %q, done heard %v: want nothing typed and the failure", c.panel.pastes, heard)
	}

	a, c, w = messagingRig(t)
	keys = nil
	w.inboxSend = func(p []any) (any, error) {
		keys = append(keys, p[5].(string))
		if len(keys) == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		return map[string]any{"message": "m1"}, nil
	}
	heard = nil
	if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settle(c, w)
	if len(keys) != 2 || keys[0] != keys[1] || len(c.panel.pastes) != 0 || len(heard) != 1 || heard[0] != nil {
		t.Fatalf("keys %q pasted %q heard %v: want a second ask with the same key, rung, nothing typed", keys, c.panel.pastes, heard)
	}
}

// The production default until a kind's adapter lands: a CLI that doesn't report its permission
// prompts is typed to but never sent, since an Enter could approve a prompt it shows (Johno's
// "build prompt detection, then Enter"). done hears ErrAgentTypedNotSent, with a notice.
func TestACLIThatDoesntReportPromptsIsTypedNotSent(t *testing.T) {
	a, c, w := messagingRig(t)
	a.promptsReported = nil // crew.ReportsPrompts: no kind yet
	w.inboxSend = func([]any) (any, error) { return nil, &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "x"} }
	var heard []error
	if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settle(c, w)
	if len(c.panel.pastes) != 1 || len(c.panel.keys) != 0 || len(heard) != 1 || !errors.Is(heard[0], ErrAgentTypedNotSent) {
		t.Fatalf("pasted %q pressed %q heard %v: want typed, no Enter, ErrAgentTypedNotSent", c.panel.pastes, c.panel.keys, heard)
	}
	if !hasNote(c.notes, "one's CLI doesn't report its permission prompts") {
		t.Fatalf("notes %q", c.notes)
	}
}

// An agent the crew says waits on a permission or blocked prompt is typed to but never sent: an
// Enter would answer its prompt (the mutant: the guard dropped).
func TestNoEnterIsPressedIntoAPrompt(t *testing.T) {
	a, c, w := messagingRig(t)
	w.inboxSend = func([]any) (any, error) { return nil, &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "x"} }
	a.inbox = &crewInbox{waiting: map[string]bool{"one": true}}
	var heard []error
	if err := a.SendText(AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settle(c, w)
	if len(c.panel.pastes) != 1 || len(c.panel.keys) != 0 || len(heard) != 1 || !errors.Is(heard[0], ErrAgentTypedNotSent) {
		t.Fatalf("pasted %q pressed %q heard %v: want typed, no Enter", c.panel.pastes, c.panel.keys, heard)
	}
	if !hasNote(c.notes, "one is waiting on a prompt") {
		t.Fatalf("notes %q", c.notes)
	}
}

// A stopped agent is started with the user's consent and typed to: a session just registered has no
// doorbell yet, so it is never rung (S1: the doorbell runs inside the one delivery).
func TestAStartedAgentIsNotRung(t *testing.T) {
	a, c, w := crewAgents(t, oneTwo)
	a.promptsReported = func(string) bool { return true }
	rang := false
	w.inboxSend = func([]any) (any, error) { rang = true; return map[string]any{"message": "m"}, nil }
	if err := a.SendText(AgentRef{Codename: "two"}, "hi", func(error) {}); err != nil {
		t.Fatal(err)
	}
	if len(c.questions) != 1 {
		t.Fatalf("asked %d questions, want the consent to start two", len(c.questions))
	}
	c.questions[0].OnYes()
	settle(c, w)
	if rang {
		t.Fatal("an agent just started was rung")
	}
}
