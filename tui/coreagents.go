package tui

import (
	"fmt"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// THE CORE'S AGENTS: what a feature asks of the window's agents and its crew scope through its Core
// (todos' A, the commands): the host hands each to the agents (agentinstances.go) or the crew host
// (crewhost.go).

// AgentEntries are the window's agents, as a picker lists them.
func (c core) AgentEntries() []feature.AgentEntry { return c.h.agents.AgentEntries() }

// windowScope is the crew scope the window holds, "" for none (todos_assign.go).
func (c core) windowScope() string {
	if c.h.crew == nil {
		return ""
	}
	scope, _, _ := c.h.crew.held()
	return scope
}

// peerAgents are the live crew agents of every other scope, for todos' A (todos_assign.go): none
// while the window holds no scope, since it couldn't send them anything.
func (c core) peerAgents(then func([]feature.AgentEntry)) {
	if c.h.crew == nil {
		then(nil)
		return
	}
	c.h.crew.peerAgents(then)
}

// sendTask sends another host's agent a task as the user's request (todos_assign.go).
func (c core) sendTask(codename, scope, subject, body, key string, then func(error)) {
	if c.h.crew == nil {
		then(fmt.Errorf("%w: the window holds no crew scope", errDoorbellNotSent))
		return
	}
	c.h.crew.sendTask(codename, scope, subject, body, key, then)
}

// HandTask hands an agent a task.
func (c core) HandTask(to feature.AgentRef, task feature.TaskRef, instruction string, done func(error)) error {
	return c.h.agents.HandTask(to, task, instruction, done)
}

// SendText types text into an agent.
func (c core) SendText(to feature.AgentRef, text string, done func(error)) error {
	return c.h.agents.SendText(to, text, done)
}
