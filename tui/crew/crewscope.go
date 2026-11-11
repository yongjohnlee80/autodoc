package crew

import (
	"context"

	corecrew "github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// THE AGENTS' CREW SCOPE: the crew host and the inbox are what the agents ask of the window's crew
// (agent.go CrewScope, CrewAttention), through these, each the crew host's or the inbox's own.

var (
	_ agents.CrewScope     = (*CrewHost)(nil)
	_ agents.CrewAttention = (*Inbox)(nil)
)

// Held is the scope the window holds (held).
func (h *CrewHost) Held() (scope string, epoch int64, ok bool) { return h.held() }

// Register registers an agent's session under the window's scope (register).
func (h *CrewHost) Register(codename, kind string, prompts bool, via corecrew.MailVia, workspace int64, instance, dir string, then func(agents.CrewSession, error)) {
	h.register(codename, kind, prompts, via, workspace, instance, dir, then)
}

// Publish writes a session's context for the process about to start (publish).
func (h *CrewHost) Publish(s agents.CrewSession) (string, error) { return h.publish(s) }

// Exit ends a session (exit).
func (h *CrewHost) Exit(s agents.CrewSession, why string) { h.exit(s, why) }

// PromptsReported is whether the crew says an agent's permission prompts are reported (promptsReported).
func (h *CrewHost) PromptsReported(codename string, then func(reported bool)) {
	h.promptsReported(codename, then)
}

// StartedOf is whether the crew says an agent has started (startedOf).
func (h *CrewHost) StartedOf(codename string, then func(started bool)) { h.startedOf(codename, then) }

// WhenExited runs fn once the session id's exit is told (whenExited).
func (h *CrewHost) WhenExited(id string, fn func()) { h.whenExited(id, fn) }

// QueueSend sends an agent a task through the crew (queueSend).
func (h *CrewHost) QueueSend(codename, kind, subject, body string, then func(error)) {
	h.queueSend(codename, kind, subject, body, then)
}

// UserSend sends an agent the user's own message through the crew (userSend).
func (h *CrewHost) UserSend(codename, kind, subject, body string, then func(error)) {
	h.userSend(codename, kind, subject, body, then)
}

// AwaitsUser is whether an agent waits on the user (awaitsUser).
func (x *Inbox) AwaitsUser(codename string) bool { return x.awaitsUser(codename) }

// Go runs work off the loop through the crew host's core.
func (h *CrewHost) Go(work func(context.Context) any, then func(any)) { h.c.Go(work, then) }
