package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// AgentRef identifies a profile and, optionally, its exact running incarnation.
// An empty Instance resolves the current incarnation at acceptance.
type AgentRef struct{ Codename, Instance string }

// TaskRef is a detached task identity; Path is the absolute task-file path.
type TaskRef struct{ Title, Path, ID string }

// AgentEntry is a value snapshot. Labels may repeat; Ref never routes by label.
type AgentEntry struct {
	Ref                      AgentRef
	Name, Command            string
	Running, Closing, Orphan bool
}

var (
	ErrAgentStale        = errs.Sentinel(errs.ErrPrecondition, "agent instance is stale")
	ErrAgentUnavailable  = errs.Sentinel(errs.ErrPrecondition, "agent is unavailable")
	ErrAgentCancelled    = errs.Sentinel(errs.ErrPrecondition, "agent delivery was cancelled")
	ErrAgentShuttingDown = errs.Sentinel(errs.ErrPrecondition, "agent owner is shutting down")
	ErrAgentTask         = errs.Sentinel(errs.ErrInvalidArgument, "task needs a title and absolute file path")
	ErrAgentText         = errs.Sentinel(errs.ErrInvalidArgument, "text to send must be non-empty and fit the terminal's input")
)

var agentIncarnation atomic.Uint64

type agentInstance struct {
	profile          agentProfile
	tab              widget.TerminalTabID
	token            string
	workspace        wsInfo
	running, closing bool
	restart          bool // Restart closed it: its profile starts again, in a new tab, once it is gone
	cleanupError     error
	owner            TerminalPanel
	generation       int
	retirement       TerminalCompletion
}

type agentRuntimeHost interface {
	agentHost
	Terminals(string) (TerminalPanel, error)
	Post(func())
	After(time.Duration, func())
	Ticket(*uint64, TicketScope) Ticket
}

// agentSubmitDelay is how long a task's paste has before its Enter, as auto-agents gives a notice
// (send_slot's submit_delay_ms). A CLI that gets the Enter in the same read as the paste can take
// it as part of the paste and not submit.
// NOTE: can't announce: an agent CLI says nothing when it has taken in a paste, so the Enter waits
// a fixed pause after it.
const agentSubmitDelay = 60 * time.Millisecond

type agentDelivery struct {
	done     func(error)
	finished bool
}

func (a *agents) runtime() (TerminalPanel, error) {
	c, ok := a.c.(agentRuntimeHost)
	if !ok {
		return nil, ErrAgentUnavailable
	}
	panel, err := c.Terminals("agentView")
	if err == nil {
		// a rebuilt parent has none of the old one's menus: every lookup gives them again
		panel.Menus(a.addRows, a.tabRows)
	}
	return panel, err
}

// AgentEntries lists saved profiles first and retained orphan instances after
// them, without exposing mutable records, the Host or terminal components.
func (a *agents) AgentEntries() []AgentEntry {
	a.syncInstances()
	entries := []AgentEntry{}
	saved := map[string]bool{}
	for _, p := range a.profiles() {
		saved[p.Codename] = true
		e := AgentEntry{Ref: AgentRef{Codename: p.Codename}, Name: p.Name, Command: p.Command}
		if instance := a.instances[p.Codename]; instance != nil {
			e.Running, e.Closing = instance.running, instance.closing
			if instance.running {
				e.Ref.Instance = instance.token
			}
		}
		entries = append(entries, e)
	}
	for _, code := range a.instanceOrder {
		instance := a.instances[code]
		if instance == nil || saved[code] {
			continue
		}
		ref := AgentRef{Codename: code}
		if instance.running {
			ref.Instance = instance.token
		}
		entries = append(entries, AgentEntry{Ref: ref, Name: instance.profile.Name, Command: instance.profile.Command,
			Running: instance.running, Closing: instance.closing, Orphan: true})
	}
	return entries
}

// syncInstances observes explicit terminal-side restarts and owner destruction.
// A new process generation gets a new token even when its native tab survives.
func (a *agents) syncInstances() {
	current, err := a.runtime()
	if err != nil {
		for _, instance := range a.instances {
			instance.running = false
		}
		return
	}
	for _, instance := range a.instances {
		if instance.owner == nil {
			continue
		}
		if !instance.owner.SameOwner(current) {
			if instance.retirement == nil {
				a.awaitInstanceRetirement(instance, instance.owner.Shutdown())
			}
			continue
		}
		found := false
		for _, tab := range instance.owner.Tabs() {
			if tab.ID != instance.tab {
				continue
			}
			found = true
			if tab.Running && tab.Generation != instance.generation {
				instance.token = fmt.Sprintf("agent-%d", agentIncarnation.Add(1))
			}
			instance.running, instance.generation = tab.Running && !instance.closing, tab.Generation
		}
		if !found {
			instance.running = false
		}
	}
}

func (a *agents) selectInstance(instance *agentInstance) error {
	panel, err := a.runtime()
	if err != nil {
		return err
	}
	if err := panel.Select(instance.tab); err != nil {
		return err
	}
	a.selectedAgent = instance.profile.Codename
	a.agentRunning, a.agentRunningLabel = "", ""
	if instance.running {
		a.agentRunning, a.agentRunningLabel = instance.profile.Codename, instance.profile.Name
	}
	a.c.Set("App.agentTitle", "agent · "+instance.profile.Name)
	return nil
}

func (a *agents) agentTabsChanged() {
	panel, err := a.runtime()
	if err != nil {
		return
	}
	for _, instance := range a.instances {
		if instance.tab != panel.Active() {
			continue
		}
		a.selectedAgent = instance.profile.Codename
		a.agentRunning, a.agentRunningLabel = "", ""
		if instance.running {
			a.agentRunning, a.agentRunningLabel = instance.profile.Codename, instance.profile.Name
		}
		a.c.Set("App.agentTitle", "agent · "+instance.profile.Name)
		return
	}
	a.selectedAgent, a.agentRunning, a.agentRunningLabel = "", "", ""
	a.c.Set("App.agentTitle", "agent")
}

func (a *agents) startInstance(p agentProfile) (*agentInstance, error) {
	a.syncInstances()
	if a.stopping {
		return nil, ErrAgentShuttingDown
	}
	if why := a.profileProblem(p); why != "" {
		return nil, fmt.Errorf("%s: %w", why, ErrAgentUnavailable)
	}
	if existing := a.instances[p.Codename]; existing != nil && existing.closing {
		return nil, ErrAgentUnavailable
	}
	ws, ok := a.c.ActiveWorkspace()
	if !ok {
		return nil, ErrAgentUnavailable
	}
	panel, err := a.runtime()
	if err != nil {
		return nil, err
	}
	a.c.OpenDrawer("agent") // Start must return the actual startup outcome, not a pre-mount promise
	instance := a.instances[p.Codename]
	if instance != nil && (instance.workspace.name != ws.name || instance.workspace.root != ws.root) {
		return nil, fmt.Errorf("close the old workspace's agent tab before starting in another workspace: %w", ErrAgentStale)
	}
	if instance != nil && instance.running {
		return instance, a.selectInstance(instance)
	}
	if instance != nil && instance.profile.Command != p.Command {
		return nil, fmt.Errorf("close the old agent tab before starting its edited command: %w", ErrAgentStale)
	}
	dir, err := a.writeAgentGuide(ws)
	if err != nil {
		return nil, err
	}
	path, args, err := agentArgv(fillAgentCommand(p.Command, ws), a.agentEnv(ws, dir, p.Codename))
	if err != nil {
		return nil, err
	}
	if instance == nil {
		instance = &agentInstance{profile: p, workspace: ws, owner: panel}
		id, err := panel.Add(p.Codename, TerminalConfig{Command: path, Args: args, Dir: dir,
			Exited: func(code int) { a.instanceExited(instance, code) }})
		if err != nil {
			return nil, err
		}
		instance.tab = id
		a.instances[p.Codename] = instance
		a.instanceOrder = append(a.instanceOrder, p.Codename)
	}
	if err := panel.Start(instance.tab); err != nil {
		return nil, err
	}
	instance.profile, instance.workspace = p, ws
	instance.token = fmt.Sprintf("agent-%d", agentIncarnation.Add(1))
	instance.running = true
	for _, tab := range panel.Tabs() {
		if tab.ID == instance.tab {
			instance.generation = tab.Generation
		}
	}
	if err := a.selectInstance(instance); err != nil {
		return nil, err
	}
	a.listAgents()
	return instance, nil
}

func (a *agents) instanceExited(instance *agentInstance, code int) {
	if a.stopping || a.instances[instance.profile.Codename] != instance {
		return
	}
	instance.running = false
	if a.selectedAgent == instance.profile.Codename {
		a.agentRunning, a.agentRunningLabel = "", ""
	}
	a.listAgents()
	name := instance.profile.Name
	switch code {
	case 0:
		anyRunning := false
		for _, other := range a.instances {
			if other != instance && other.running {
				anyRunning = true
			}
		}
		if !anyRunning {
			a.c.CloseDrawer("agent")
		}
		a.c.Notify(fmt.Sprintf("the agent %s exited: SPC g starts the default again", name))
	case 127, 126:
		why := map[int]string{127: "was not found", 126: "could not run"}[code]
		a.c.Notify(fmt.Sprintf("the agent %s's command %s (%d): check it in System › Agent profiles…", name, why, code))
	default:
		a.c.Notify(fmt.Sprintf("the agent %s exited (%d): choose its tab to inspect it or start it again", name, code))
	}
}

func (a *agents) removeInstance(code string) error {
	a.syncInstances()
	instance := a.instances[code]
	if instance == nil {
		return ErrAgentUnavailable
	}
	if instance.closing {
		return nil
	}
	panel, err := a.runtime()
	if err != nil {
		return err
	}
	result, err := panel.Remove(instance.tab)
	if err != nil {
		return err
	}
	a.awaitInstanceRetirement(instance, result)
	return nil
}

// Retire through the captured owner's cleanup result, not by sending old IDs
// to a new parent. Failed cleanup keeps the incarnation and codename reserved.
func (a *agents) awaitInstanceRetirement(instance *agentInstance, result TerminalCompletion) {
	instance.running, instance.closing, instance.retirement = false, true, result
	code := instance.profile.Codename
	a.listAgents()
	c := a.c.(agentRuntimeHost)
	complete := func(err error) {
		if a.stopping || a.instances[code] != instance {
			return
		}
		if err != nil {
			instance.cleanupError = err
			a.c.Notify("the agent did not finish cleanup: " + err.Error())
			return
		}
		delete(a.instances, code)
		a.instanceOrder = slices.DeleteFunc(a.instanceOrder, func(value string) bool { return value == code })
		if a.selectedAgent == code {
			a.selectedAgent, a.agentRunning, a.agentRunningLabel = "", "", ""
		}
		a.listAgents()
		if instance.restart {
			p, saved := a.profile(code)
			if !saved {
				p = instance.profile
			}
			a.startAgent(p)
		}
	}
	select {
	case <-result.Done():
		complete(result.Wait(context.Background()))
	default:
		go func() { err := result.Wait(context.Background()); c.Post(func() { complete(err) }) }()
	}
}

// HandTask accepts a task for one exact agent incarnation. An immediate error
// rejects it without calling done; accepted calls return nil and complete done
// once on the UI loop, including cancellation and orderly owner shutdown.
// The task goes as one paste, then an Enter of its own agentSubmitDelay later
// (a carriage return, the key's byte), so the agent starts on it, as AutoVim's
// assign has auto-agents do. Success means the task typed and submitted, not
// the agent's completion.
func (a *agents) HandTask(to AgentRef, task TaskRef, instruction string, done func(error)) error {
	check := func() error {
		if task.Title == "" || !filepath.IsAbs(task.Path) || strings.ContainsAny(instruction, "\r\n") {
			return ErrAgentTask
		}
		return nil
	}
	payload := fmt.Sprintf("Task: %s\nFile: %s\nID: %s\nInstruction: %s", task.Title, task.Path, task.ID, instruction)
	return a.deliver(to, check, "this task", true, func(panel TerminalPanel, tab widget.TerminalTabID) error {
		return panel.SendPaste(tab, payload)
	}, done)
}

// SendText accepts text for one agent incarnation, as HandTask accepts a task, through the same
// delivery: the same refusals, the same consent to start a stopped agent, the same checks before
// anything is sent. The text goes as one paste: to a CLI that asked for bracketed paste it is
// whole and waits at the prompt for Enter; elsewhere its newlines are Enter, so its earlier lines
// are submitted as they arrive and only its last waits. No Enter is added after it. Success means
// typed delivery.
func (a *agents) SendText(to AgentRef, text string, done func(error)) error {
	check := func() error {
		if text == "" || len(text) > widget.TerminalInputLimit {
			return ErrAgentText
		}
		return nil
	}
	return a.deliver(to, check, "this text", false, func(panel TerminalPanel, tab widget.TerminalTabID) error {
		return panel.SendPaste(tab, text)
	}, done)
}

// deliver is HandTask's and SendText's one path: to resolved, check's refusal after the owner's
// own, a stopped agent started once the user agrees (what names what is sent), everything checked
// again before send runs on the agent's tab, and done completed once on the loop. With submit, an
// Enter follows send agentSubmitDelay later, after the same checks again, and done waits for it.
func (a *agents) deliver(to AgentRef, check func() error, what string, submit bool, send func(TerminalPanel, widget.TerminalTabID) error, done func(error)) error {
	a.syncInstances()
	if a.stopping {
		return ErrAgentShuttingDown
	}
	if err := check(); err != nil {
		return err
	}
	instance := a.instances[to.Codename]
	if to.Instance != "" && (instance == nil || !instance.running || instance.token != to.Instance) {
		return ErrAgentStale
	}
	p, saved := a.profile(to.Codename)
	if !saved {
		if instance == nil || !instance.running {
			return ErrAgentUnavailable
		}
		p = instance.profile
	}
	if instance != nil && instance.closing {
		return ErrAgentUnavailable
	}
	c, ok := a.c.(agentRuntimeHost)
	if !ok {
		return ErrAgentUnavailable
	}
	ws, ok := a.c.ActiveWorkspace()
	if !ok {
		return ErrAgentUnavailable
	}
	if instance != nil && instance.running && (instance.workspace.name != ws.name || instance.workspace.root != ws.root) {
		return ErrAgentStale
	}
	ticket := c.Ticket(nil, TicketWorkspace)
	accepted := &agentDelivery{done: done}
	a.deliveries[accepted] = true
	finish := func(err error) {
		if accepted.finished {
			return
		}
		accepted.finished = true
		delete(a.deliveries, accepted)
		if accepted.done != nil {
			accepted.done(err)
		}
	}
	validate := func() error {
		if a.stopping {
			return ErrAgentShuttingDown
		}
		currentWS, exists := a.c.ActiveWorkspace()
		if !exists || !ticket.Current() || currentWS.name != ws.name || currentWS.root != ws.root {
			return ErrAgentStale
		}
		current := a.instances[to.Codename]
		if instance != current || (instance != nil && (instance.closing || (to.Instance != "" && (!instance.running || instance.token != to.Instance)))) {
			return ErrAgentStale
		}
		if saved {
			if currentProfile, exists := a.profile(to.Codename); !exists || currentProfile != p {
				return ErrAgentStale
			}
		}
		return nil
	}
	deliver := func() {
		if accepted.finished {
			return
		}
		// the terminal's parent may have been rebuilt since the task was accepted: its instances are
		// retired before anything is checked, so an old-parent instance reads as stale here
		a.syncInstances()
		if err := validate(); err != nil {
			finish(err)
			return
		}
		if instance == nil || !instance.running {
			started, err := a.startInstance(p)
			if err != nil {
				finish(err)
				return
			}
			instance = started
			// the incarnation just started is the one sent to: a restart in its tab before the
			// Enter (a new generation, a new token) makes the delivery stale, as a running
			// target's captured token does
			to.Instance = instance.token
		}
		if err := validate(); err != nil {
			finish(err)
			return
		}
		panel, err := a.runtime()
		if err == nil && !instance.owner.SameOwner(panel) {
			err = ErrAgentStale // its tab is the old parent's: never sent to the new one
		}
		if err == nil {
			err = send(panel, instance.tab)
		}
		if err != nil || !submit {
			finish(err)
			return
		}
		tab := instance.tab
		c.After(agentSubmitDelay, func() {
			if accepted.finished {
				return // shutdown finished it in the pause: nothing more is typed
			}
			a.syncInstances()
			if err := validate(); err != nil {
				finish(err)
				return
			}
			panel, err := a.runtime()
			if err == nil && (!instance.running || instance.tab != tab || !instance.owner.SameOwner(panel)) {
				err = ErrAgentStale
			}
			if err == nil {
				err = panel.SendText(tab, "\r")
			}
			finish(err)
		})
	}
	if instance != nil && instance.running {
		to.Instance = instance.token // capture the resolved current incarnation before the next loop turn
		c.Post(deliver)
	} else {
		a.c.Ask(Question{Title: "start the agent?", Text: "Start " + p.Name + " and send " + what + "?", Yes: "&Yes, start and send",
			OnYes: deliver, OnCancel: func() {
				if a.stopping {
					finish(ErrAgentShuttingDown)
				} else {
					finish(ErrAgentCancelled)
				}
			}})
	}
	return nil
}

func (a *agents) Stopping() {
	a.stopping = true
	for delivery := range a.deliveries {
		if delivery.finished {
			continue
		}
		delivery.finished = true
		delete(a.deliveries, delivery)
		if delivery.done != nil {
			delivery.done(ErrAgentShuttingDown)
		}
	}
}

func (c core) AgentEntries() []AgentEntry { return c.h.agents.AgentEntries() }
func (c core) HandTask(to AgentRef, task TaskRef, instruction string, done func(error)) error {
	return c.h.agents.HandTask(to, task, instruction, done)
}
func (c core) SendText(to AgentRef, text string, done func(error)) error {
	return c.h.agents.SendText(to, text, done)
}
