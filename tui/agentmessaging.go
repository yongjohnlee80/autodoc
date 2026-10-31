package tui

import (
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// Agent messaging: how anything the user sends reaches one of the window's agents
// (docs/decisions/crew.md#the-users-own-sends). One delivery (agents.deliver: the agent resolved,
// validated, recorded until done, brought forward), and two ways through it, tried in order:
//
//   - the doorbell (agentDoorbell): the message mailed from `user` through the crew, the daemon
//     ringing the agent's CLI through that kind's own waker (Claude's socket, agy's bridge,
//     OpenCode's plugin, Codex's app-server). It answers rang, or why not: errDoorbellNotSent,
//     nothing sent, goes on to the base; any other error ends the delivery unsent, since the
//     doorbell may have rung and the message must never arrive twice.
//   - the base: the message as one bracketed paste into the agent's terminal, then Enter, only into
//     an agent whose permission prompts are reported now (crew.list's prompts_reported: a live
//     session whose reporter is there) and that shows none: an Enter into a permission prompt
//     would approve it. Otherwise it stays typed, and done hears ErrAgentTypedNotSent.
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

// ErrAgentTypedNotSent is a message the base typed into the agent's input but didn't send: the
// window can't see its permission prompts now, or it shows one. The user's Enter there sends it.
var ErrAgentTypedNotSent = errors.New("typed into its input, not sent")

// errDoorbellNotSent is a doorbell that answered, for certain, that nothing was sent: the message
// goes on to the base.
var errDoorbellNotSent = errors.New("the doorbell didn't ring")

// agentDoorbell rings an agent through its CLI's own channel. answer is called once, on the loop:
// rang, or why not.
type agentDoorbell interface {
	ring(to AgentRef, m AgentMessage, answer func(rang bool, err error))
}

// crewDoorbell is the crew's doorbell: inbox.send with fire, from the window that holds the scope.
type crewDoorbell struct{ crew *crewHost }

func (d crewDoorbell) ring(to AgentRef, m AgentMessage, answer func(rang bool, err error)) {
	d.crew.userSend(to.Codename, m.Kind, m.Subject, m.Body, func(err error) { answer(err == nil, err) })
}

// submitDelay is how long the base waits between the paste and its Enter: a program that reads the
// two in one read can take the Enter as part of the paste (auto-agents' send_slot waits the same).
// NOTE: can't announce: a program says nothing when it has consumed a paste, so the Enter follows
// it after a fixed gap.
const submitDelay = 60 * time.Millisecond

// sendMessage delivers m to the agent to through the window's one delivery: its doorbell first when
// the window has one, else the base. An immediate error rejects it without calling done; accepted,
// done is called once on the UI loop: nil when it reached the agent (rang, or typed and sent),
// ErrAgentTypedNotSent when it was typed but left for the user's Enter, else why it didn't.
func (a *agents) sendMessage(to AgentRef, m AgentMessage, done func(error)) error {
	text := m.text()
	check := func() error {
		if text == "" || len(text) > widget.TerminalInputLimit {
			return ErrAgentText
		}
		return nil
	}
	what := a.c.Tr("autodoc.text.agentinstances.this_text")
	if m.Kind == MessageRequest {
		what = a.c.Tr("autodoc.text.agentinstances.this_task")
	}
	var refused error // the doorbell's certain refusal, told once the base has typed the message
	o := deliverOpts{present: m.Kind == MessageRequest, submit: a.submitter(to, func() error { return refused })}
	if a.crew != nil {
		var bell agentDoorbell = crewDoorbell{crew: a.crew}
		o.ring = func(answer func(rang bool, err error)) {
			bell.ring(to, m, func(rang bool, err error) {
				if !rang && errors.Is(err, errDoorbellNotSent) {
					refused = err
				}
				answer(rang, err)
			})
		}
	}
	return a.deliver(to, check, what, o, func(panel TerminalPanel, tab widget.TerminalTabID) error {
		return panel.SendBracketedPaste(tab, text)
	}, done)
}

// submitter is the base's Enter decision for to, after its paste: a doorbell's refusal (refused) is
// told now that the message is typed; submitDelay later, the crew is asked whether to's prompts are
// reported (crewHost.promptsReported), and Enter is pressed only while the delivery still holds
// (still: not finished, the same workspace, instance and tab) into an agent whose prompts are
// reported and that shows none; otherwise the message stays typed, a notice says why, and the
// delivery finishes ErrAgentTypedNotSent (or stale, pressing nothing).
func (a *agents) submitter(to AgentRef, refused func() error) func(TerminalPanel, widget.TerminalTabID, func() error, func(error)) {
	return func(panel TerminalPanel, tab widget.TerminalTabID, still func() error, finish func(error)) {
		if err := refused(); err != nil { // typed now: the doorbell's refusal is told with it
			a.c.Notify(a.c.Trf("autodoc.notice.agentmessaging.doorbell_not_fired_typed", to.Codename, err.Error()))
		}
		kind := a.kindOf(to.Codename)
		decide := func(reported bool) {
			if err := still(); err != nil { // the wait let it go stale: shut down, switched, restarted
				finish(err)
				return
			}
			if !a.promptsSeen(kind, reported) {
				a.c.Notify(a.c.Trf("autodoc.notice.agentmessaging.typed_not_sent_no_prompt_reporting", to.Codename))
				finish(ErrAgentTypedNotSent)
				return
			}
			if a.inbox.awaitsUser(to.Codename) { // an Enter would answer its permission or blocked prompt
				a.c.Notify(a.c.Trf("autodoc.notice.agentmessaging.typed_not_sent_it_waits_on_a_prompt", to.Codename))
				finish(ErrAgentTypedNotSent)
				return
			}
			finish(panel.SendText(tab, "\r"))
		}
		look := func() {
			if a.crew == nil {
				decide(false)
				return
			}
			a.crew.promptsReported(to.Codename, decide)
		}
		if c, ok := a.c.(agentRuntimeHost); ok {
			c.After(submitDelay, look)
			return
		}
		look()
	}
}

// promptsSeen is whether the window sees the agent's permission prompts now: the crew's answer
// (reported), or a cell's promptsReported in its place.
func (a *agents) promptsSeen(kind string, reported bool) bool {
	if a.promptsReported != nil {
		return a.promptsReported(kind, reported)
	}
	return reported
}

// kindOf is the CLI kind of codename's agent (agentKind of its command), "" when it has none here.
func (a *agents) kindOf(codename string) string {
	if p, ok := a.profile(codename); ok {
		return agentKind(p.Command)
	}
	if in := a.instances[codename]; in != nil {
		return agentKind(in.profile.Command)
	}
	return ""
}

// taskMessage is a task as a request: its title in the subject, its file, id and the instruction
// in the body.
func taskMessage(task TaskRef, instruction string) AgentMessage {
	return AgentMessage{Kind: MessageRequest, Subject: "Task: " + task.Title,
		Body: fmt.Sprintf("File: %s\nID: %s\nInstruction: %s", task.Path, task.ID, instruction)}
}
