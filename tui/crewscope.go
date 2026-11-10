package tui

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// THE AGENTS' CREW SCOPE: the crew host and the inbox are what the agents ask of the window's crew
// (agent.go CrewScope, CrewAttention), through these, each the crew host's or the inbox's own.

var (
	_ CrewScope     = (*crewHost)(nil)
	_ CrewAttention = (*crewInbox)(nil)
)

// Held is the scope the window holds (held).
func (h *crewHost) Held() (scope string, epoch int64, ok bool) { return h.held() }

// Register registers an agent's session under the window's scope (register).
func (h *crewHost) Register(codename, kind string, prompts bool, via crew.MailVia, workspace int64, instance, dir string, then func(crewSession, error)) {
	h.register(codename, kind, prompts, via, workspace, instance, dir, then)
}

// Publish writes a session's context for the process about to start (publish).
func (h *crewHost) Publish(s crewSession) (string, error) { return h.publish(s) }

// Exit ends a session (exit).
func (h *crewHost) Exit(s crewSession, why string) { h.exit(s, why) }

// PromptsReported is whether the crew says an agent's permission prompts are reported (promptsReported).
func (h *crewHost) PromptsReported(codename string, then func(reported bool)) {
	h.promptsReported(codename, then)
}

// StartedOf is whether the crew says an agent has started (startedOf).
func (h *crewHost) StartedOf(codename string, then func(started bool)) { h.startedOf(codename, then) }

// WhenExited runs fn once the session id's exit is told (whenExited).
func (h *crewHost) WhenExited(id string, fn func()) { h.whenExited(id, fn) }

// QueueSend sends an agent a task through the crew (queueSend).
func (h *crewHost) QueueSend(codename, kind, subject, body string, then func(error)) {
	h.queueSend(codename, kind, subject, body, then)
}

// UserSend sends an agent the user's own message through the crew (userSend).
func (h *crewHost) UserSend(codename, kind, subject, body string, then func(error)) {
	h.userSend(codename, kind, subject, body, then)
}

// AwaitsUser is whether an agent waits on the user (awaitsUser).
func (x *crewInbox) AwaitsUser(codename string) bool { return x.awaitsUser(codename) }

// Go runs work off the loop through the crew host's core.
func (h *crewHost) Go(work func(context.Context) any, then func(any)) { h.c.Go(work, then) }
