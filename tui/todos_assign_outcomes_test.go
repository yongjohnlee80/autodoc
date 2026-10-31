package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// TestTheHandOverSaysHowItWent (docs/decisions/todos.md#a-message-to-an-agent-is-sent-never-left-for-enter):
// the assignment stands whatever came of its hand-over, and the notice says which: sent; typed into
// the agent's input for the user's Enter (ErrAgentTypedNotSent, wrapped too); refused by a program
// that takes no bracketed paste; or not delivered, saying why (the user not starting the agent in
// the catalog's words, any other refusal as it is).
func TestTheHandOverSaysHowItWent(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{nil, "todos: assigned to Scribe, and sent to it"},
		{ErrAgentTypedNotSent, "todos: assigned to Scribe, and typed into its input: press Enter there to send it"},
		{fmt.Errorf("deliver: %w", ErrAgentTypedNotSent), "todos: assigned to Scribe, and typed into its input: press Enter there to send it"},
		{widget.ErrTerminalPasteUnbracketed, "todos: assigned to Scribe, but not handed over: its program takes no bracketed paste; open its panel and paste the task"},
		{ErrAgentCancelled, "todos: assigned to autodoc:scribe, but not delivered (you didn't start it): start the agent, then A on the task to deliver it again"},
		{errors.New("the tab closed"), "todos: assigned to autodoc:scribe, but not delivered (the tab closed): start the agent, then A on the task to deliver it again"},
	} {
		s := newScripted("kb")
		newTodos(s).handedOver("Scribe", "autodoc:scribe", c.err)
		if !slices.Equal(s.notes, []string{c.want}) {
			t.Errorf("%v: notices %q, want %q", c.err, s.notes, c.want)
		}
	}
}

// TestAnAgentChosenWithNoAssignOpenIsNothing: an agent chosen when no assign is open (a late
// picker answer) asks for no Instructions: line and sets nothing.
func TestAnAgentChosenWithNoAssignOpenIsNothing(t *testing.T) {
	s := newScripted("kb")
	newTodos(s).assignTo(AgentEntry{Ref: AgentRef{Codename: "scribe"}, Name: "Scribe"})
	if len(s.opened) != 0 || s.set["App.todoAssignTitle"] != nil {
		t.Errorf("opened %v, title %v: want nothing asked, nothing set", s.opened, s.set["App.todoAssignTitle"])
	}
}

// TestAnOrphansPaneSaysItsProfileIsGone: the picker's preview of an agent whose profile is gone (a
// running orphan) says it takes the task only while it runs; one with a profile never says so.
func TestAnOrphansPaneSaysItsProfileIsGone(t *testing.T) {
	const gone = "Its profile is gone; it takes the task while it runs."
	s := newScripted("kb")
	orphan := agentPane(s, AgentEntry{Ref: AgentRef{Codename: "old"}, Name: "Old", Command: "old", Running: true, Orphan: true})
	kept := agentPane(s, AgentEntry{Ref: AgentRef{Codename: "one"}, Name: "One", Command: "one", Running: true})
	if !strings.Contains(orphan.text, gone) || strings.Contains(kept.text, gone) {
		t.Errorf("the orphan's pane %q, a profiled agent's %q: want the profile gone said for the orphan alone", orphan.text, kept.text)
	}
}
