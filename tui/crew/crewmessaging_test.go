package crew

import (
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// The window's agents typed to, never sent, while the crew says they wait on a prompt: a cell from
// the host's agentmessaging_test.go, here so the inbox the agents ask is the crew's own, fed by its
// snapshot.

// An agent the crew says waits on a permission or blocked prompt is typed to but never sent: an
// Enter would answer its prompt (the mutant: the guard dropped).
func TestNoEnterIsPressedIntoAPrompt(t *testing.T) {
	a, c, w := messagingRig(t)
	w.inboxSend = func([]any) (any, error) { return nil, &golibrpc.Error{Code: inbox.CodeUnreachable, Message: "x"} }
	// the crew says one waits on a permission prompt: an attention raised, in the inbox's snapshot
	iw := newInboxWindow()
	iw.answers = []any{snapshot(1, "", nil, []any{map[string]any{"session": "s1", "codename": "one", "kind": "permission", "text": "edit main.go?"}})}
	x := newCrewInbox(iw, iw.raise, nil)
	x.Connected()
	iw.step(t)
	if !x.AwaitsUser("one") {
		t.Fatal("the inbox's snapshot raised no attention for one")
	}
	a.Inbox = x
	var heard []error
	if err := a.SendText(feature.AgentRef{Codename: "one"}, "hello one", func(err error) { heard = append(heard, err) }); err != nil {
		t.Fatal(err)
	}
	settleMessaging(c, w)
	if len(c.panel.pastes) != 1 || len(c.panel.keys) != 0 || len(heard) != 1 || !errors.Is(heard[0], agents.ErrAgentTypedNotSent) {
		t.Fatalf("pasted %q pressed %q heard %v: want typed, no Enter", c.panel.pastes, c.panel.keys, heard)
	}
	if !hasNote(c.notes, "one is waiting on a prompt") {
		t.Fatalf("notes %q", c.notes)
	}
}

// A copy of the host's hasNote (agentmessaging_test.go).
func hasNote(notes []string, prefix string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}
