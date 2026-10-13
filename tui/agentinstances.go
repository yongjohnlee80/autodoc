package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

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
)

var agentIncarnation atomic.Uint64

type agentInstance struct {
	profile          agentProfile
	tab              widget.TerminalTabID
	token            string
	workspace        wsInfo
	running, closing bool
	cleanupError     error
	owner            TerminalPanel
	generation       int
}

type agentRuntimeHost interface {
	agentHost
	Terminals(string) (TerminalPanel, error)
	Post(func())
	Ticket(*uint64, TicketScope) Ticket
}

type agentDelivery struct {
	done     func(error)
	finished bool
}

func (a *agents) runtime() (TerminalPanel, error) {
	c, ok := a.c.(agentRuntimeHost)
	if !ok {
		return nil, ErrAgentUnavailable
	}
	return c.Terminals("agentView")
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
	for _, instance := range a.instances {
		if instance.owner == nil {
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
	path, args, err := agentArgv(fillAgentCommand(p.Command, ws), a.agentEnv(ws, dir))
	if err != nil {
		return nil, err
	}
	if instance == nil {
		instance = &agentInstance{profile: p, workspace: ws, owner: panel}
		id, err := panel.Add(p.Name, TerminalConfig{Command: path, Args: args, Dir: dir, VimKeys: a.c.Prefs().keymap == "vim",
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
	case 127, 126:
		why := map[int]string{127: "was not found", 126: "could not run"}[code]
		a.c.Notify(fmt.Sprintf("the agent %s's command %s (%d): check it in System › Agent profiles…", name, why, code))
	default:
		a.c.Notify(fmt.Sprintf("the agent %s exited (%d): choose its tab to inspect it or start it again", name, code))
	}
}

func (a *agents) removeInstance(code string) error {
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
	instance.running, instance.closing = false, true
	a.listAgents()
	c := a.c.(agentRuntimeHost)
	go func() {
		err := result.Wait(context.Background())
		c.Post(func() {
			if a.stopping || a.instances[code] != instance {
				return
			}
			if err != nil {
				instance.cleanupError = err
				a.c.Notify("the agent did not finish cleanup: " + err.Error())
				return
			}
			delete(a.instances, code)
			if a.selectedAgent == code {
				a.selectedAgent, a.agentRunning, a.agentRunningLabel = "", "", ""
			}
			a.listAgents()
		})
	}()
	return nil
}

// HandTask accepts a task for one exact agent incarnation. An immediate error
// rejects it without calling done; accepted calls return nil and complete done
// once on the UI loop, including cancellation and orderly owner shutdown.
// Success means typed delivery, not agent completion.
func (a *agents) HandTask(to AgentRef, task TaskRef, instruction string, done func(error)) error {
	a.syncInstances()
	if a.stopping {
		return ErrAgentShuttingDown
	}
	if task.Title == "" || !filepath.IsAbs(task.Path) || strings.ContainsAny(instruction, "\r\n") {
		return ErrAgentTask
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
	payload := fmt.Sprintf("Task: %s\nFile: %s\nID: %s\nInstruction: %s\n", task.Title, task.Path, task.ID, instruction)
	deliver := func() {
		if accepted.finished {
			return
		}
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
		}
		if err := validate(); err != nil {
			finish(err)
			return
		}
		panel, err := a.runtime()
		if err == nil {
			err = panel.SendText(instance.tab, payload)
		}
		finish(err)
	}
	if instance != nil && instance.running {
		to.Instance = instance.token // capture the resolved current incarnation before the next loop turn
		c.Post(deliver)
	} else {
		a.c.Ask(Question{Title: "start the agent?", Text: "Start " + p.Name + " and send this task?", Yes: "&Yes, start and send",
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
