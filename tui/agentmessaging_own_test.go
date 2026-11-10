package tui

import (
	"errors"
	"io"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// settleRounds bounds settleMessaging's rounds of the crew host's timers: the doorbell's asks again
// need userSendTries of them, and the scope's renewal re-arms itself every round, so the timers
// never run out on their own.
const settleRounds = userSendTries + 2

func hasNote(notes []string, prefix string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
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
			if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
				t.Fatal(err)
			}
			c.drainPosts()
			if len(c.panel.pastes) != 1 || !strings.Contains(c.panel.pastes[0], "hello one") || len(c.panel.keys) != 0 {
				t.Fatalf("pasted %q, pressed %q: want the text once, the Enter not with it", c.panel.pastes, c.panel.keys)
			}
			settleMessaging(c, w)
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
// key, userSendTries times in all, and never typed; done hears the failure. A later
// ask that answers rang ends it sent (the mutants: a fallback on any error; a fresh key per ask).
func TestAnUnknownDoorbellIsAskedAgainNeverTyped(t *testing.T) {
	a, c, w := messagingRig(t)
	var keys []string
	w.inboxSend = func(p []any) (any, error) { keys = append(keys, p[5].(string)); return nil, io.ErrUnexpectedEOF }
	var heard []error
	if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
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
	if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(keys) != 2 || keys[0] != keys[1] || len(c.panel.pastes) != 0 || len(heard) != 1 || heard[0] != nil {
		t.Fatalf("keys %q pasted %q heard %v: want a second ask with the same key, rung, nothing typed", keys, c.panel.pastes, heard)
	}
}

// The production rule for a kind with no prompt adapter: an agent whose permission prompts the
// window can't see is typed to but never sent, since an Enter could approve a prompt it shows.
// done hears ErrAgentTypedNotSent, with a notice.
func TestACLIThatDoesntReportPromptsIsTypedNotSent(t *testing.T) {
	a, c, w := messagingRig(t)
	a.promptsReported = nil // promptsSeen: one's command is no kind with an adapter
	w.inboxSend = func([]any) (any, error) { return nil, &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "x"} }
	var heard []error
	if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(c.panel.pastes) != 1 || len(c.panel.keys) != 0 || len(heard) != 1 || !errors.Is(heard[0], ErrAgentTypedNotSent) {
		t.Fatalf("pasted %q pressed %q heard %v: want typed, no Enter, ErrAgentTypedNotSent", c.panel.pastes, c.panel.keys, heard)
	}
	if !hasNote(c.notes, "AutoDoc can't see one's permission prompts") {
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
	if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(c.panel.pastes) != 1 || len(c.panel.keys) != 0 || len(heard) != 1 || !errors.Is(heard[0], ErrAgentTypedNotSent) {
		t.Fatalf("pasted %q pressed %q heard %v: want typed, no Enter", c.panel.pastes, c.panel.keys, heard)
	}
	if !hasNote(c.notes, "one is waiting on a prompt") {
		t.Fatalf("notes %q", c.notes)
	}
}

// The Enter waits (submitDelay, then the crew's answer), and the delivery may go stale meanwhile: a
// shutdown, or the agent closing. Nothing is pressed then, and done hears it once (the mutant: the
// Enter not checked again).
func TestNoEnterIsPressedIntoADeliveryThatWentStale(t *testing.T) {
	for _, c := range []struct {
		name  string
		stale func(a *agents)
	}{
		{"shut down", func(a *agents) { a.Stopping() }},
		{"the agent closing", func(a *agents) { a.instances["one"].closing = true }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, cw, w := messagingRig(t)
			w.inboxSend = func([]any) (any, error) { return nil, &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "x"} }
			w.crewList = func() (any, error) { c.stale(a); return []any{}, nil } // stale while the crew answers
			var heard []error
			if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
				t.Fatal(err)
			}
			settleMessaging(cw, w)
			if len(cw.panel.pastes) != 1 || len(cw.panel.keys) != 0 || len(heard) != 1 || heard[0] == nil {
				t.Fatalf("pasted %q pressed %q heard %v: want typed, no Enter, one stale answer", cw.panel.pastes, cw.panel.keys, heard)
			}
		})
	}
}
