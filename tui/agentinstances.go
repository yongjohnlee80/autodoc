package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/crew"
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
	profile agentProfile
	dir     string // the folder its tab's process runs in (agentFolder)
	// starting is a start whose crew session is being registered: the process starts once the
	// session's context is written. session is the crew session it runs under, when it has one;
	// crewed whether its starts are crew sessions' (its window held a scope when it was started);
	// context the file of the session its next process runs under, told it at its start
	// (AUTODOC_CREW_CONTEXT, runAgent), "" while there is none.
	starting         bool
	session          crewSession
	crewed           bool
	context          string
	tab              widget.TerminalTabID
	token            string
	workspace        wsInfo
	running, closing bool
	restart          bool // Restart closed it: its profile starts again, in a new tab, once it is gone
	cleanupError     error
	owner            TerminalPanel
	generation       int
	retirement       TerminalCompletion
	// seen is the workspace this incarnation was last told of (takeSwitch, agentswitch.go), its own
	// at first; seenToken the incarnation it belongs to, so a restart starts again from its own
	seen      wsRef
	seenToken string
}

type agentRuntimeHost interface {
	agentHost
	Terminals(string) (TerminalPanel, error)
	Post(func())
	After(time.Duration, func())
	Ticket(*uint64, TicketScope) Ticket
}

// agentReadyWait is how long a delivery to an agent it just started waits for the agent's program
// to take bracketed pastes, trying again agentReadyPoll apart
// (docs/decisions/todos.md#text-reaches-an-agent-only-as-a-bracketed-paste): a starting program
// hasn't asked for them yet.
// NOTE: can't announce: a program says nothing when it enables bracketed paste, so the paste is
// tried again until it is taken.
const (
	agentReadyWait = 10 * time.Second
	agentReadyPoll = 100 * time.Millisecond
)

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
	a.c.Set("App.agentTitle", a.c.Trf("autodoc.text.agentinstances.agent", instance.profile.Name))
	return nil
}

func (a *agents) agentTabsChanged() {
	defer a.c.WidgetsChanged() // an agent started or gone: the dock lists the agents while one runs
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
		a.c.Set("App.agentTitle", a.c.Trf("autodoc.text.agentinstances.agent", instance.profile.Name))
		return
	}
	a.selectedAgent, a.agentRunning, a.agentRunningLabel = "", "", ""
	a.c.Set("App.agentTitle", a.c.Tr("autodoc.text.agentinstances.agent_2"))
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
	if instance != nil && (instance.running || instance.starting) {
		return instance, a.selectInstance(instance)
	}
	if instance != nil && instance.profile.Command != p.Command {
		return nil, fmt.Errorf("close the old agent tab before starting its edited command: %w", ErrAgentStale)
	}
	if instance != nil && instance.dir != a.agentFolder(ws, p.Codename) {
		// the window's crew scope changed since the tab was made: its process would start in the old one's folder
		return nil, fmt.Errorf("close the old agent tab before starting it under this window's crew scope: %w", ErrAgentStale)
	}
	// a crew session needs the window's scope and the workspace's id (a daemon before it has none)
	crewed := false
	if a.crew != nil {
		_, _, held := a.crew.held()
		crewed = held && ws.id > 0
	}
	crewKind := ""
	if crewed {
		crewKind = agentKind(p.Command)
	}
	dir, err := a.writeAgentGuide(ws, p.Codename, crewKind)
	if err != nil {
		return nil, err
	}
	env := a.agentEnv(ws, dir, p.Codename)
	if crewed {
		// a wake reaches it only with these: without them it still starts, and receives when it asks
		a.c.Keep(writeWakeAcceptance(dir, agentKind(p.Command)))
	}
	path, args, err := agentArgv(fillAgentCommand(p.Command, ws), env)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		instance = &agentInstance{profile: p, workspace: ws, owner: panel, dir: dir}
		id, err := panel.Add(p.Codename, TerminalConfig{Command: path, Args: args, Dir: dir,
			Exited: func(code int) { a.instanceExited(instance, code) },
			Start:  func(c pty.Cmd) (widget.TerminalProcess, error) { return a.runAgent(instance, c) }})
		if err != nil {
			return nil, err
		}
		instance.tab = id
		a.instances[p.Codename] = instance
		a.instanceOrder = append(a.instanceOrder, p.Codename)
	}
	instance.profile, instance.workspace, instance.crewed = p, ws, crewed
	if crewed {
		// registered first (docs/decisions/crew.md#sessions-and-the-guard): the process starts only
		// once its session's context is written, and a refused registration starts nothing. The
		// answer is checked before either: a tab closed or restarted meanwhile, or a window whose
		// scope, epoch or daemon changed, ends the session instead
		instance.token = fmt.Sprintf("agent-%d", agentIncarnation.Add(1))
		instance.starting = true
		token := instance.token
		a.crew.register(p.Codename, agentKind(p.Command), ws.id, token, dir, a.whenStartable(crewKind, func(s crewSession, err error) {
			instance.starting = false
			if a.stopping || a.instances[p.Codename] != instance || instance.closing || instance.token != token {
				if err == nil {
					a.crew.exit(s, crew.EndedStartFailed) // nobody starts it now
				}
				a.listAgents()
				return
			}
			if err == nil {
				if instance.context, err = a.crew.publish(s); err != nil {
					a.crew.exit(s, crew.EndedStartFailed)
				}
			}
			if err != nil {
				a.c.Notify(a.c.Trf("autodoc.crew.not_registered", p.Name, wireMessage(err)))
				a.listAgents()
				return
			}
			if err := a.start(panel, instance); err != nil {
				instance.context = ""
				a.crew.exit(s, crew.EndedStartFailed)
				a.c.Notify(a.c.Trf("autodoc.crew.did_not_start", p.Name, err.Error()))
				a.listAgents()
				return
			}
			instance.session = s // it runs: its exit ends the session, whatever showing it does
			if err := a.showStarted(instance); err != nil {
				a.c.Notify(a.c.Trf("autodoc.crew.tab_not_shown", p.Name, err.Error()))
			}
		}))
		a.listAgents()
		return instance, nil
	}
	instance.token = fmt.Sprintf("agent-%d", agentIncarnation.Add(1))
	if err := a.start(panel, instance); err != nil {
		return nil, err
	}
	if err := a.showStarted(instance); err != nil {
		return nil, err
	}
	return instance, nil
}

// errCrewStartHere is a crew agent's tab restarted from the tab itself (Enter after its program
// exited): its crew session is registered by the agents panel's start, never without one.
var errCrewStartHere = errors.New("this agent runs as a crew session: start it from the agents panel, which registers one")

// runAgent is an agent tab's TerminalConfig.Start, on the loop: its program, told the context
// file of the crew session it starts under (AUTODOC_CREW_CONTEXT), that file being this session's
// own (crew.ContextPath). A crew agent's start with no session published for it is refused. A crew
// OpenCode agent also loads AutoDoc's OpenCode plugin (OPENCODE_CONFIG_CONTENT, opencodeEnv), which
// registers its session and wakes it: without it the session is never reachable, so a plugin that
// can't be had refuses the start.
func (a *agents) runAgent(instance *agentInstance, c pty.Cmd) (widget.TerminalProcess, error) {
	if instance.crewed {
		if instance.context == "" {
			return nil, errCrewStartHere
		}
		c.Args = withEnvArg(c.Args, "AUTODOC_CREW_CONTEXT="+instance.context)
		if agentKind(instance.profile.Command) == "opencode" {
			env, err := a.opencodePluginEnv()
			if err != nil {
				return nil, fmt.Errorf("AutoDoc's OpenCode plugin: %w", err)
			}
			c.Args = withEnvArg(c.Args, env)
		}
	}
	proc, err := a.spawn(c)
	if err == nil && instance.crewed && agentKind(instance.profile.Command) == "codex" {
		a.registerCodex(instance.context, c.Dir, time.Now())
	}
	return proc, err
}

// codexRegisterWait and codexRegisterEvery bound the window's registration of a Codex agent: looks
// 2 s apart, as many as a minute holds, none started a minute or more after its spawn.
// NOTE: can't announce: Codex starts its thread on its app-server without telling anyone, so the
// window looks again on a timer (the loop's After) until it is found, the session ends, or the
// minute is up.
const (
	codexRegisterWait  = time.Minute
	codexRegisterEvery = 2 * time.Second
)

// registerCodex registers a crew Codex agent's session, started at started in folder dir under
// the session context file ctxFile, so it is reachable before anyone types: its own autodoc's
// `crew endpoint --codex-here`, which finds the folder's thread on Codex's app-server, run off the
// loop and again until it is found. A session that ended (its context gone) stops it; one never
// found raises the session's attention, saying why, so the window shows it.
func (a *agents) registerCodex(ctxFile, dir string, started time.Time) {
	c, ok := a.c.(agentRuntimeHost)
	if !ok {
		return
	}
	run := a.codexHere
	if run == nil {
		bin, config := a.agentBin(), a.agentConfig
		run = func(args ...string) error { return runAgentAutodoc(bin, config, ctxFile, dir, args...) }
	}
	since := strconv.FormatInt(started.Unix()-2, 10) // the thread may be dated a moment before the spawn returns
	var attempt func(tries int)
	attempt = func(tries int) {
		go func() {
			err := run("crew", "endpoint", "--codex-here", "--since", since)
			_, gone := os.Stat(ctxFile) // here, never on the loop
			c.Post(func() {
				switch {
				case err == nil, gone != nil: // found, or the session ended: nothing more to register
				case tries > 1 && time.Since(started)+codexRegisterEvery < codexRegisterWait: // looks slow to answer count too
					c.After(codexRegisterEvery, func() { attempt(tries - 1) })
				default:
					go func() {
						_ = run("crew", "attention", "raise", "blocked", "AutoDoc can't wake this Codex session: "+attentionText(err.Error()))
					}()
				}
			})
		}()
	}
	attempt(int(codexRegisterWait / codexRegisterEvery))
}

// runAgentAutodoc runs the agent's own autodoc (bin, on config) with args in folder dir, as its
// session (ctxFile, $AUTODOC_CREW_CONTEXT), bounded at 15 s; its error carries what it said.
func runAgentAutodoc(bin, config, ctxFile, dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	full := []string{}
	if config != "" {
		full = append(full, "--config", config)
	}
	cmd := exec.CommandContext(ctx, bin, append(full, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "AUTODOC_CREW_CONTEXT="+ctxFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// attentionText is a reason as the attention's text: one line (oneLine), at most 300 bytes.
func attentionText(s string) string {
	s = strings.TrimSpace(oneLine(s))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// opencodeTimeout bounds the plugin's setup: one run of the agent's own autodoc, which writes a
// file and prints a line.
const opencodeTimeout = 10 * time.Second

// pluginEnvAnswer is opencodeEnv's answer for this process, on its way until done closes: its
// output depends only on the agent's build and config, so it runs once, off the loop, and every
// crew OpenCode spawn takes the same answer.
type pluginEnvAnswer struct {
	done chan struct{}
	env  string
	err  error
}

// warmPluginEnv starts opencodeEnv off the loop, once (on the loop: the agents feature's start, and
// a spawn that finds none started): the spawn, which runs on the loop, then only reads its answer.
func (a *agents) warmPluginEnv() *pluginEnvAnswer {
	if a.pluginAnswer == nil {
		f := &pluginEnvAnswer{done: make(chan struct{})}
		a.pluginAnswer = f
		run, bin, config := a.pluginEnv, a.agentBin(), a.agentConfig // read on the loop
		go func() {
			if run != nil {
				f.env, f.err = run()
			} else {
				f.env, f.err = opencodeEnv(bin, config)
			}
			close(f.done)
		}()
	}
	return a.pluginAnswer
}

// whenStartable is then, for a crew session of kind whose start needs what the window warms off
// the loop: an OpenCode agent's, the plugin's env (warmPluginEnv). Its registration's answer runs
// then at once when the env's answer is in, and otherwise when that answer posts, so the start
// (runAgent, which takes the env) never waits on the loop. The instance stays starting meanwhile.
func (a *agents) whenStartable(kind string, then func(crewSession, error)) func(crewSession, error) {
	c, ok := a.c.(agentRuntimeHost)
	if kind != "opencode" || !ok {
		return then
	}
	f := a.warmPluginEnv()
	return func(s crewSession, err error) {
		select {
		case <-f.done:
			then(s, err)
		default:
			go func() {
				<-f.done
				c.Post(func() { then(s, err) })
			}()
		}
	}
}

// opencodePluginEnv is the spawn's OPENCODE_CONFIG_CONTENT=…: the warmed answer. A crew start
// reaches it only once that answer is in (whenStartable), so it waits on the loop only for a start
// that came another way. A failure is not kept: the next spawn runs it again.
func (a *agents) opencodePluginEnv() (string, error) {
	f := a.warmPluginEnv()
	<-f.done
	if f.err != nil && a.pluginAnswer == f {
		a.pluginAnswer = nil
	}
	return f.env, f.err
}

// opencodeEnv is the OPENCODE_CONFIG_CONTENT=… that loads AutoDoc's OpenCode plugin, merged with
// any the TUI was started with: what the agent's own autodoc (bin, on config) prints (`autodoc crew
// plugin --opencode --print-env`), so the plugin is the build's that the agent runs, the one it
// calls back into to register. A cell replaces it (agents.pluginEnv).
func opencodeEnv(bin, config string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opencodeTimeout)
	defer cancel()
	args := []string{}
	if config != "" {
		args = append(args, "--config", config)
	}
	args = append(args, "crew", "plugin", "--opencode", "--print-env")
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if !strings.HasPrefix(line, "OPENCODE_CONFIG_CONTENT=") || strings.Contains(line, "\n") {
		return "", fmt.Errorf("unexpected output from crew plugin --print-env: %q", line)
	}
	return line, nil
}

// start starts instance's process in its tab: whether it started is this, never whether it is shown.
func (a *agents) start(panel TerminalPanel, instance *agentInstance) error {
	if err := panel.Start(instance.tab); err != nil {
		return err
	}
	instance.running = true
	for _, tab := range panel.Tabs() {
		if tab.ID == instance.tab {
			instance.generation = tab.Generation
		}
	}
	return nil
}

// showStarted shows a started instance running and selected; failing to select it stops nothing.
func (a *agents) showStarted(instance *agentInstance) error {
	err := a.selectInstance(instance)
	a.listAgents()
	return err
}

func (a *agents) instanceExited(instance *agentInstance, code int) {
	instance.context = ""                         // its session ends with it: a next start registers a new one
	if instance.session.ID != "" && !a.stopping { // stopping: the window's release ends every session
		a.crew.exit(instance.session, crew.EndedExit)
		instance.session = crewSession{}
	}
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
		a.c.Notify(a.c.Trf("autodoc.notice.agentinstances.the_agent_exited_spc_g_starts_the_default_again", name))
	case 127, 126:
		if code == 127 {
			a.c.Notify(a.c.Trf("autodoc.notice.agentinstances.the_agent_s_command_was_not_found_check_it_in_sy", name, code))
		} else {
			a.c.Notify(a.c.Trf("autodoc.notice.agentinstances.the_agent_s_command_could_not_run_check_it_in_sy", name, code))
		}
	default:
		a.c.Notify(a.c.Trf("autodoc.notice.agentinstances.the_agent_exited_choose_its_tab_to_inspect_it_or", name, code))
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
			a.c.Notify(a.c.Trf("autodoc.notice.agentinstances.the_agent_did_not_finish_cleanup", err.Error()))
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
// The task goes as one bracketed paste, or not at all: a program that takes none
// gets nothing, and done hears widget.ErrTerminalPasteUnbracketed. No Enter
// follows (docs/decisions/todos.md#autodoc-never-presses-enter-for-an-agent): the task waits in the
// agent's input, its panel brought forward ready to type, and the user's Enter sends it. Success
// means the task typed, not submitted.
func (a *agents) HandTask(to AgentRef, task TaskRef, instruction string, done func(error)) error {
	check := func() error {
		if task.Title == "" || !filepath.IsAbs(task.Path) || strings.ContainsAny(instruction, "\r\n") {
			return ErrAgentTask
		}
		return nil
	}
	payload := fmt.Sprintf("Task: %s\nFile: %s\nID: %s\nInstruction: %s", task.Title, task.Path, task.ID, instruction)
	return a.deliver(to, check, a.c.Tr("autodoc.text.agentinstances.this_task"), deliverOpts{present: true}, func(panel TerminalPanel, tab widget.TerminalTabID) error {
		return panel.SendBracketedPaste(tab, payload)
	}, done)
}

// SendText accepts text for one agent incarnation, as HandTask accepts a task, through the same
// delivery: the same refusals, the same consent to start a stopped agent, the same checks before
// anything is sent. The text goes as one bracketed paste, whole, waiting at the prompt for Enter;
// to a program that takes none nothing is sent, and done hears widget.ErrTerminalPasteUnbracketed
// (docs/decisions/todos.md#text-reaches-an-agent-only-as-a-bracketed-paste). No Enter is added
// after it. Success means typed delivery.
func (a *agents) SendText(to AgentRef, text string, done func(error)) error {
	check := func() error {
		if text == "" || len(text) > widget.TerminalInputLimit {
			return ErrAgentText
		}
		return nil
	}
	return a.deliver(to, check, a.c.Tr("autodoc.text.agentinstances.this_text"), deliverOpts{}, func(panel TerminalPanel, tab widget.TerminalTabID) error {
		return panel.SendBracketedPaste(tab, text)
	}, done)
}

// present brings instance forward for the user's Enter
// (docs/decisions/todos.md#autodoc-never-presses-enter-for-an-agent): the agents panel open, its
// tab selected, the keyboard in it, in input mode. The task is typed either way: what fails here is
// kept, never the delivery's error.
func (a *agents) present(panel TerminalPanel, instance *agentInstance) {
	a.c.OpenDrawer("agent")
	a.c.Keep(a.selectInstance(instance))
	a.c.Keep(panel.SetMode(instance.tab, widget.TerminalInput))
}

// Resume is autodoc.agents.resume: a SIGCONT to agent to's tab, to as SendText takes it, or with
// no codename the agents panel's tab now. A stopped, closing or unknown agent is ErrAgentUnavailable,
// an incarnation that is gone ErrAgentStale. A running agent is not disturbed.
func (a *agents) Resume(to AgentRef) error {
	a.syncInstances()
	panel, err := a.runtime()
	if err != nil {
		return err
	}
	if to.Codename == "" {
		if panel.Active() == "" {
			return ErrAgentUnavailable
		}
		return panel.Resume(panel.Active())
	}
	instance := a.instances[to.Codename]
	if to.Instance != "" && (instance == nil || !instance.running || instance.token != to.Instance) {
		return ErrAgentStale
	}
	if instance == nil || !instance.running || instance.closing {
		return ErrAgentUnavailable
	}
	return panel.Resume(instance.tab)
}

// deliverOpts are a delivery's choices.
type deliverOpts struct {
	present bool // once sent, the agent's panel is brought forward for the user's Enter (present)
}

// deliver is HandTask's and SendText's one path: to resolved, check's refusal
// after the owner's own, a stopped agent started once the user agrees (what names what is sent),
// everything checked again before send runs on the agent's tab, and done completed once on the
// loop. An agent deliver started refusing a bracketed paste is tried again, the same checks first,
// until agentReadyWait.
func (a *agents) deliver(to AgentRef, check func() error, what string, o deliverOpts, send func(TerminalPanel, widget.TerminalTabID) error, done func(error)) error {
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
		if instance != current || (instance != nil && (instance.closing || (to.Instance != "" && ((!instance.running && !instance.starting) || instance.token != to.Instance)))) {
			return ErrAgentStale
		}
		if saved {
			if currentProfile, exists := a.profile(to.Codename); !exists || currentProfile != p {
				return ErrAgentStale
			}
		}
		return nil
	}
	// attempt checks everything again and runs send on the agent's tab. A refused bracketed paste is
	// tried again agentReadyPoll later, tries more times: an agent deliver just started is still
	// starting its program.
	var attempt func(tries int)
	attempt = func(tries int) {
		if accepted.finished {
			return // shutdown finished it in the wait: nothing is sent
		}
		a.syncInstances()
		if err := validate(); err != nil {
			finish(err)
			return
		}
		if instance.starting { // its crew session is being registered: its process starts after
			if tries > 0 {
				c.After(agentReadyPoll, func() { attempt(tries - 1) })
				return
			}
			finish(ErrAgentStale)
			return
		}
		panel, err := a.runtime()
		if err == nil && (!instance.running || !instance.owner.SameOwner(panel)) {
			err = ErrAgentStale // its tab is the old parent's: never sent to the new one
		}
		if err == nil {
			err = send(panel, instance.tab)
		}
		if tries > 0 && errors.Is(err, widget.ErrTerminalPasteUnbracketed) {
			c.After(agentReadyPoll, func() { attempt(tries - 1) })
			return
		}
		if err == nil && o.present {
			a.present(panel, instance)
		}
		finish(err)
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
		started := false
		if instance == nil || !instance.running {
			fresh, err := a.startInstance(p)
			if err != nil {
				finish(err)
				return
			}
			instance, started = fresh, true
			// the incarnation just started is the one sent to: a restart in its tab while the
			// paste waits for it (a new generation, a new token) makes the delivery stale, as a
			// running target's captured token does
			to.Instance = instance.token
		}
		tries := 0
		if started {
			tries = int(agentReadyWait / agentReadyPoll)
		}
		attempt(tries)
	}
	if instance != nil && instance.running {
		to.Instance = instance.token // capture the resolved current incarnation before the next loop turn
		c.Post(deliver)
	} else {
		a.c.Ask(Question{Title: a.c.Tr("autodoc.question.agentinstances.start_the_agent"), Text: a.c.Trf("autodoc.question.agentinstances.start_and_send", p.Name, what), Yes: a.c.Tr("autodoc.question.agentinstances.yes_start_and_send"),
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

// raiseSession opens the agent drawer on this window's agent whose crew session is session (a crew
// toast's click, an inbox focus), and answers false when no agent here has it. One here whose tab
// can't be selected is still here: that failure is kept, not taken for another window's.
func (a *agents) raiseSession(session string) bool {
	if session == "" {
		return false
	}
	for _, instance := range a.instances {
		if instance.session.ID == session {
			a.c.OpenDrawer("agent")
			a.c.Keep(a.selectInstance(instance))
			return true
		}
	}
	return false
}
