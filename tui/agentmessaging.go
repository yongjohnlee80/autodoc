package tui

import (
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// Agent messaging: how anything the user sends reaches one of the window's agents
// (docs/decisions/crew.md#the-users-own-sends). One contract, two implementations:
//
//   - textMessenger, the base: the message pasted into the agent's terminal, then Enter, so it is
//     sent with no key pressed by the user. Every CLI takes it.
//   - doorbellMessenger: the message mailed from `user` through the crew, the daemon ringing the
//     agent's CLI through that kind's own waker (Claude's socket, agy's bridge, OpenCode's plugin,
//     Codex's app-server). A doorbell that doesn't fire answers an error, and the message falls
//     back to the base.
//
// Every user-to-agent path sends through it: SendText (and the Lua autodoc.agents.send), HandTask
// (a todo's assignment, a candidate's audit). None of them chooses a mechanism.

// The kinds of AgentMessage: a plain message, or a request the agent answers (a task).
const (
	MessagePlain   = "message"
	MessageRequest = "request"
)

// AgentMessage is one message from the user to an agent: its kind, a subject (a request's), and its
// body.
type AgentMessage struct {
	Kind, Subject, Body string
}

// text is the message as the base types it: a request's subject heads its body.
func (m AgentMessage) text() string {
	if m.Subject == "" {
		return m.Body
	}
	return m.Subject + "\n" + m.Body
}

// agentMessenger delivers one AgentMessage to an agent of the window. An immediate error rejects
// it without calling done; accepted, done is called once, on the UI loop: nil when it reached the
// agent (rang, or typed and sent), else why it didn't.
type agentMessenger interface {
	send(to AgentRef, m AgentMessage, done func(error)) error
}

// submitDelay is how long the base waits between the paste and its Enter: a program that reads the
// two in one read can take the Enter as part of the paste (auto-agents' send_slot waits the same).
// NOTE: can't announce: a program says nothing when it has consumed a paste, so the Enter follows
// it after a fixed gap.
const submitDelay = 60 * time.Millisecond

// textMessenger is the base: the message as one bracketed paste into the agent's terminal, then
// Enter. A program that takes no bracketed paste gets nothing (widget.ErrTerminalPasteUnbracketed),
// so a message never arrives as keys a prompt could take one by one. No Enter is pressed into an
// agent the crew says waits on a permission or blocked prompt: it would answer the prompt. The
// message stays typed there, and a notice says so. An agent with no crew session reports no prompt.
type textMessenger struct{ a *agents }

func (t textMessenger) send(to AgentRef, m AgentMessage, done func(error)) error {
	text := m.text()
	check := func() error {
		if text == "" || len(text) > widget.TerminalInputLimit {
			return ErrAgentText
		}
		return nil
	}
	what := t.a.c.Tr("autodoc.text.agentinstances.this_text")
	if m.Kind == MessageRequest {
		what = t.a.c.Tr("autodoc.text.agentinstances.this_task")
	}
	return t.a.deliver(to, check, what, deliverOpts{present: m.Kind == MessageRequest}, func(panel TerminalPanel, tab widget.TerminalTabID) error {
		if err := panel.SendBracketedPaste(tab, text); err != nil {
			return err
		}
		enter := func() {
			if t.a.inbox.awaitsUser(to.Codename) { // an Enter would answer its permission or blocked prompt
				t.a.c.Notify(t.a.c.Trf("autodoc.notice.agentmessaging.typed_not_sent_it_waits_on_a_prompt", to.Codename))
				return
			}
			if err := panel.SendText(tab, "\r"); err != nil {
				t.a.c.Notify(t.a.c.Trf("autodoc.notice.agentmessaging.typed_but_not_sent", to.Codename, err.Error()))
			}
		}
		if c, ok := t.a.c.(agentRuntimeHost); ok {
			c.After(submitDelay, enter)
		} else {
			enter()
		}
		return nil
	}, done)
}

// doorbellMessenger mails the message from `user` through the crew and rings the agent's CLI
// (inbox.send: fired, or nothing sent). An agent with no session here, or a doorbell that doesn't
// fire, falls back to next, the base, with a notice saying why.
type doorbellMessenger struct {
	a    *agents
	next agentMessenger
}

func (d doorbellMessenger) send(to AgentRef, m AgentMessage, done func(error)) error {
	if d.a.crew == nil || !d.a.crewRoutable(to) {
		return d.next.send(to, m, done)
	}
	if m.text() == "" || len(m.text()) > widget.TerminalInputLimit {
		return ErrAgentText
	}
	d.a.crew.userSend(to.Codename, m.Kind, m.Subject, m.Body, func(err error) {
		if err == nil {
			done(nil)
			return
		}
		why := err.Error()
		if errors.Is(err, errCrewUnreachable) {
			why = d.a.c.Tr("autodoc.notice.agentmessaging.no_doorbell_reaches_it")
		}
		d.a.c.Notify(d.a.c.Trf("autodoc.notice.agentmessaging.doorbell_not_fired_typed", to.Codename, why))
		if err := d.next.send(to, m, done); err != nil {
			done(err)
		}
	})
	return nil
}

// crewRoutable reports whether to is an agent running in this window now, the only one a crew
// session can answer for: a stopped or closing one, or another incarnation than to names, goes to
// the base, which starts it with the user's consent or refuses it as before.
func (a *agents) crewRoutable(to AgentRef) bool {
	a.syncInstances()
	if a.stopping {
		return false
	}
	in := a.instances[to.Codename]
	return in != nil && in.running && !in.closing && (to.Instance == "" || in.token == to.Instance)
}

// messenger is the window's agent messaging: the doorbell first, the base behind it.
func (a *agents) messenger() agentMessenger {
	return doorbellMessenger{a: a, next: textMessenger{a: a}}
}

// taskMessage is a task as a request: its title in the subject, its file, id and the instruction
// in the body.
func taskMessage(task TaskRef, instruction string) AgentMessage {
	return AgentMessage{Kind: MessageRequest, Subject: "Task: " + task.Title,
		Body: fmt.Sprintf("File: %s\nID: %s\nInstruction: %s", task.Path, task.ID, instruction)}
}
