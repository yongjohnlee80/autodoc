package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

var (
	ErrAgentStale       = errs.Sentinel(errs.ErrPrecondition, "agent instance is stale")
	ErrAgentUnavailable = errs.Sentinel(errs.ErrPrecondition, "agent is unavailable")
	// ErrAgentElsewhere is a running agent started in another workspace than the window's: its crew
	// mail reaches it in any workspace, but the base never types into it (agentElsewhere).
	ErrAgentElsewhere    = errs.Sentinel(errs.ErrPrecondition, "agent runs in another workspace")
	ErrAgentCancelled    = errs.Sentinel(errs.ErrPrecondition, "agent delivery was cancelled")
	ErrAgentShuttingDown = errs.Sentinel(errs.ErrPrecondition, "agent owner is shutting down")
	ErrAgentTask         = errs.Sentinel(errs.ErrInvalidArgument, "task needs a title and absolute file path")
	ErrAgentText         = errs.Sentinel(errs.ErrInvalidArgument, "text to send must be non-empty and fit the terminal's input")
)

var agentIncarnation atomic.Uint64

type agentInstance struct {
	profile agentProfile
	dir     string // the folder its tab's process runs in (agentFolder)
	// refusal is why its last start was refused before its process ran (its command not on its
	// PATH, its crew registration refused): its tab's start answers it, so the terminal shows it,
	// until the next start
	refusal error
	// starting is a start whose crew session is being registered: the process starts once the
	// session's context is written. session is the crew session it runs under, when it has one;
	// crewed whether its starts are crew sessions' (its window held a scope when it was started);
	// context the file of the session its next process runs under, told it at its start
	// (AUTODOC_CREW_CONTEXT, runAgent), "" while there is none.
	starting bool
	session  crewSession
	crewed   bool
	context  string
	setup    crew.Setup // its crew start's setup (crew setup's answer): what its process starts with
	// started is the session past its CLI's startup, as the crew last said (startedOf), and told
	// the session the user was told hadn't finished starting: each a session id, "" for none
	started, told string
	// env is what its process is started with over AutoDoc's own environment: its profile's env,
	// then AutoDoc's (agentEnv), in its environment (agentEnviron); its setup's server is started
	// with the same (runAgent)
	env              []string
	tab              terminal.TerminalTabID
	token            string
	workspace        wsInfo
	running, closing bool
	restart          bool // Restart closed it: its profile starts again, in a new tab, once it is gone
	cleanupError     error
	owner            feature.TerminalPanel
	generation       int
	retirement       feature.TerminalCompletion
	// seen is the workspace this incarnation was last told of (takeSwitch, agentswitch.go), its own
	// at first; seenToken the incarnation it belongs to, so a restart starts again from its own
	seen      wsRef
	seenToken string
}

type agentRuntimeHost interface {
	agentHost
	Terminals(string) (feature.TerminalPanel, error)
	Post(func())
	After(time.Duration, func())
	Ticket(*uint64, feature.TicketScope) feature.Ticket
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

func (a *agents) runtime() (feature.TerminalPanel, error) {
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
func (a *agents) AgentEntries() []feature.AgentEntry {
	a.syncInstances()
	entries := []feature.AgentEntry{}
	saved := map[string]bool{}
	for _, p := range a.profiles() {
		saved[p.Codename] = true
		e := feature.AgentEntry{Ref: feature.AgentRef{Codename: p.Codename}, Name: p.Name, Command: p.Command}
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
		ref := feature.AgentRef{Codename: code}
		if instance.running {
			ref.Instance = instance.token
		}
		entries = append(entries, feature.AgentEntry{Ref: ref, Name: instance.profile.Name, Command: instance.profile.Command,
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
	ws, ok := a.c.ActiveWorkspaceInfo()
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
	if instance != nil && !instance.profile.sameLaunch(p) {
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
		crewKind = p.kind()
	}
	dir, err := a.writeAgentGuide(ws, p.Codename, crewKind, crew.MailViaOf(crewKind)) // a crew start's is written again in its setup's mode
	if err != nil {
		return nil, err
	}
	env := append(p.envList(a.seams.env), a.agentEnv(ws, dir, p)...) // AutoDoc's own after the profile's: AutoDoc's win
	path, args := agentArgv(a.seams.env, fillAgentCommand(a.seams.env, p.Command, ws))
	if instance == nil {
		instance = &agentInstance{profile: p, workspace: ws, owner: panel, dir: dir}
		id, err := panel.Add(p.Codename, feature.TerminalConfig{Command: path, Args: args, Dir: dir,
			Exited: func(code int) { a.instanceExited(instance, code) },
			Start:  func(c pty.Cmd) (terminal.TerminalProcess, error) { return a.runAgent(instance, c) }})
		if err != nil {
			return nil, err
		}
		instance.tab = id
		a.instances[p.Codename] = instance
		a.instanceOrder = append(a.instanceOrder, p.Codename)
	}
	instance.profile, instance.workspace, instance.crewed, instance.env = p, ws, crewed, env
	instance.refusal = nil
	if a.commandCheck != nil {
		if err := a.commandCheck(usershell.Commands(a.seams.env), fillAgentCommand(a.seams.env, p.Command, ws), env); err != nil { // nothing set up or registered for it
			a.c.Notify(a.c.Trf("autodoc.crew.did_not_start", p.Name, err.Error()))
			a.refuse(panel, instance, err)
			return instance, nil
		}
	}
	if crewed {
		// registered first (docs/decisions/crew.md#sessions-and-the-guard): the process starts only
		// once its session's context is written, and a refused registration starts nothing. The
		// answer is checked before either: a tab closed or restarted meanwhile, or a window whose
		// scope, epoch or daemon changed, ends the session instead
		instance.token = fmt.Sprintf("agent-%d", agentIncarnation.Add(1))
		instance.starting = true
		token := instance.token
		stale := func() bool {
			return a.stopping || a.instances[p.Codename] != instance || instance.closing || instance.token != token
		}
		// its setup first (crew setup, the agent's own build, as every host asks): how its session
		// reads its mail is what the setup delivered, so the guide, the folder's settings and files,
		// and the registration all follow it
		a.askSetup(crewKind, dir, p.environ(a.seams.env), func(setup crew.Setup, err error) {
			if stale() {
				instance.starting = false
				a.listAgents()
				return
			}
			prompts := false
			if err == nil {
				prompts, err = a.applySetup(ws, p, dir, setup)
			}
			if err != nil {
				instance.starting = false
				a.c.Notify(a.c.Trf("autodoc.crew.did_not_start", p.Name, err.Error()))
				a.listAgents()
				return
			}
			instance.setup = setup
			a.registerCrew(panel, instance, p, ws, dir, token, prompts, stale)
		})
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

// refuse keeps why instance's start was refused and shows it in its tab: the tab's start answers it
// (runAgent), which the terminal writes on its screen, and the tab is selected; nothing runs.
func (a *agents) refuse(panel feature.TerminalPanel, instance *agentInstance, why error) {
	instance.refusal, instance.starting = why, false
	_ = panel.Start(instance.tab) // the terminal shows the refusal, and the tab stays stopped
	a.c.Keep(a.selectInstance(instance))
	a.listAgents()
}

// commandOnPath refuses a command whose program isn't on the PATH in env (the agent's: its own
// autodoc's link, then its profile's), naming it. AutoDoc runs the command with shell (/bin/sh -c;
// PowerShell's -NoProfile -Command on Windows), which reads none of the user's shell aliases or
// functions: an alias for another account's Claude Code (e.g. claude-work) is not found there. Only
// what shell looks up as it is written is judged (usershell.Shell.LooksUp): a bare name, found as
// the OS finds a program (PATHEXT on Windows), and an absolute path. The shell resolves the rest
// itself, and they pass: its own syntax, a leading ~, a relative path (from the agent's folder, its
// process's working directory, never AutoDoc's), a glob; so does a name found only through a
// relative PATH entry, which is the agent's folder's to resolve too.
func commandOnPath(shell usershell.Shell, command string, env []string) error {
	word, judged := shell.LooksUp(command)
	if !judged {
		return nil
	}
	if _, err := procenv.LookPath(procenv.Env(env), word); err == nil || errors.Is(err, exec.ErrDot) {
		return nil
	}
	if filepath.IsAbs(word) {
		return fmt.Errorf("its command %s is not an executable file", word)
	}
	return fmt.Errorf("its command %q isn't a program on its PATH. AutoDoc runs the command with %s, which sees none of your shell's aliases or functions. "+
		"For another account's Claude Code, use the program itself: command claude, with CLAUDE_CONFIG_DIR=<that account's folder> in the profile's environment (Space G; tutorial 12, the agents panel)", word, shell.Describe())
}

// registerCrew registers instance's crew session, as its setup said it reads its mail
// (crew.register's mail_via) and with prompts (the window wrote the hooks that report them), then
// starts its process once the session's context is written; a refused registration starts nothing.
func (a *agents) registerCrew(panel feature.TerminalPanel, instance *agentInstance, p agentProfile, ws wsInfo, dir, token string, prompts bool, stale func() bool) {
	a.crew.register(p.Codename, p.kind(), prompts, instance.setup.MailVia, ws.id, token, dir, func(s crewSession, err error) {
		instance.starting = false
		if stale() {
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
			a.c.Notify(a.c.Trf("autodoc.crew.not_registered", p.Name, feature.WireMessage(err)))
			a.refuse(panel, instance, fmt.Errorf("its crew session was not registered: %s", feature.WireMessage(err)))
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
	})
}

// applySetup puts a crew start's setup into the agent's folder dir before its session registers:
// the guide in its mail mode's form (crew.GuideOfVia), its folder files, and for Claude Code the
// wake's hooks (the window's, from what its Claude Code knows) with the setup's allowance, which on
// MCP mail is its tools alone. prompts is that the hooks that report its prompts were written.
func (a *agents) applySetup(ws wsInfo, p agentProfile, dir string, setup crew.Setup) (prompts bool, err error) {
	kind := p.kind()
	for _, n := range setup.Notices { // what the host tells the user, once: the setup's own words
		a.c.Notify(n)
	}
	if _, err := a.writeAgentGuide(ws, p.Codename, kind, setup.MailVia); err != nil {
		return false, err
	}
	if kind == crew.KindOpenCode && setup.Env["OPENCODE_CONFIG_CONTENT"] == "" {
		// its plugin registers its session and wakes it: without it no mail could reach it
		return false, errors.New("AutoDoc's OpenCode plugin: crew setup gave no OPENCODE_CONFIG_CONTENT")
	}
	if err := writeFolderFiles(dir, setup.FolderFiles); err != nil {
		return false, err
	}
	if kind == crew.KindClaude {
		// a wake reaches it only with these: without them it still starts, and receives when it asks
		var err error
		prompts, err = writeWakeAcceptance(dir, kind, a.claudeHooks(p), setup.Allow)
		a.c.Keep(err)
	}
	return prompts, nil
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
func (a *agents) runAgent(instance *agentInstance, c pty.Cmd) (terminal.TerminalProcess, error) {
	if instance.refusal != nil {
		return nil, instance.refusal
	}
	if instance.crewed {
		if instance.context == "" {
			return nil, errCrewStartHere
		}
		// its setup's: AutoDoc's env (OpenCode's OPENCODE_CONFIG_CONTENT, its plugin and its MCP
		// server), and its arguments (Claude Code's --mcp-config) after the program's own
		if instance.profile.kind() == crew.KindOpenCode && instance.setup.Env["OPENCODE_CONFIG_CONTENT"] == "" {
			return nil, errors.New("AutoDoc's OpenCode plugin: crew setup gave no OPENCODE_CONFIG_CONTENT")
		}
		c.Args = withCommandArgs(a.seams.env, c.Args, instance.setup.Argv)
	}
	c.Env = agentEnviron(a.seams.env, instance)
	// its setup's server (a Codex agent's own app-server), started before the CLI with the CLI's
	// environment, and bound to it: stopped if the CLI fails to start or when it ends, and closing
	// the CLI's terminal if it ends first (agentserver.go). A Codex shell runs under this server,
	// not the CLI, so `autodoc --editor` there needs it.
	var server *agentServer
	if instance.crewed && len(instance.setup.Server) > 0 {
		srv, err := startAgentServer(instance.setup.Server, instance.setup.ServerSocket, c.Dir, c.Env, a.seams.serverReady)
		if err != nil {
			return nil, err
		}
		server = srv
	}
	proc, err := a.spawn(c)
	if server != nil {
		if err != nil {
			server.stop() // the CLI didn't start: nothing of its server is left
		} else {
			proc = bindServer(proc, server)
		}
	}
	if err == nil && instance.crewed {
		// the CLI's environment, its setup's included: a Codex agent's own app-server
		// (crew.EnvCodexServer) is where its registration step looks
		environ := instance.profile.environ(a.seams.env)
		for _, k := range slices.Sorted(maps.Keys(instance.setup.Env)) {
			environ = append(environ, k+"="+instance.setup.Env[k])
		}
		a.runAfterSteps(instance.setup.After, instance.context, c.Dir, environ, time.Now())
	}
	return proc, err
}

// runAfterSteps runs a crew agent's after-steps (its setup's: a Codex agent's registration of the
// folder's thread on its app-server), once its process has started at started in folder dir under
// the session context file ctxFile with environ: each one its own autodoc's command
// (runAgentAutodoc), detached and off the loop, crew.SetupSince given as the start's Unix seconds.
// What a step must wait for, it waits for itself (`crew endpoint --codex-here --wait`), raising the
// session's attention when it can't, so the window shows it.
func (a *agents) runAfterSteps(after [][]string, ctxFile, dir string, environ []string, started time.Time) {
	if len(after) == 0 {
		return
	}
	run := func(args ...string) error {
		return runAgentAutodoc(a.agentBin(), a.agentConfig, ctxFile, dir, environ, args...)
	}
	if a.codexHere != nil {
		run = func(args ...string) error { return a.codexHere(environ, args...) }
	}
	since := strconv.FormatInt(started.Unix()-2, 10) // the thread may be dated a moment before the spawn returns
	for _, step := range after {
		args := slices.Clone(step)
		for i, arg := range args {
			args[i] = strings.ReplaceAll(arg, crew.SetupSince, since)
		}
		go func() { _ = run(args...) }()
	}
}

// runAgentAutodoc runs the agent's own autodoc (bin, on config) with args in folder dir, as its
// session (ctxFile, $AUTODOC_CREW_CONTEXT) with environ, its profile's (agentProfile.environ: a
// CODEX_HOME there is the one it reads), bounded by agentAutodocBound; its error carries what it
// said.
func runAgentAutodoc(bin, config, ctxFile, dir string, environ []string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), agentAutodocBound(args))
	defer cancel()
	full := []string{}
	if config != "" {
		full = append(full, "--config", config)
	}
	cmd := exec.CommandContext(ctx, bin, append(full, args...)...)
	cmd.Dir = dir
	cmd.Env = append(slices.Clone(environ), "AUTODOC_CREW_CONTEXT="+ctxFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// agentAutodocWait bounds an agent's autodoc command that waits for nothing.
const agentAutodocWait = 15 * time.Second

// agentAutodocBound is how long an agent's autodoc command (args) may run: agentAutodocWait, plus
// the wait the command asks for itself (--wait <duration>, as `crew endpoint --codex-here` takes).
// A step that waits ends at its own deadline, and does what it does there (raises the session's
// attention), before the window would kill it.
func agentAutodocBound(args []string) time.Duration {
	bound := agentAutodocWait
	for i, arg := range args {
		w, ok := strings.CutPrefix(arg, "--wait=")
		if !ok && arg == "--wait" && i+1 < len(args) {
			w, ok = args[i+1], true
		}
		if d, err := time.ParseDuration(w); ok && err == nil && d > 0 {
			bound += d
		}
	}
	return bound
}

// start starts instance's process in its tab: whether it started is this, never whether it is shown.
func (a *agents) start(panel feature.TerminalPanel, instance *agentInstance) error {
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

// endSession ends instance's crew session as the window takes the agent away (Close, Restart, the
// strip's stop-all), and answers its id ("" for none). A removed tab never reports its process's
// exit (the terminal's pump stops with the tab), so instanceExited may never run for it: without
// this the session stays live, its codename taken, with no process behind it.
func (a *agents) endSession(instance *agentInstance) string {
	id := instance.session.ID
	if id == "" || a.stopping { // stopping: the window's release ends every session
		return ""
	}
	instance.context = ""
	a.crew.exit(instance.session, crew.EndedExit)
	instance.session = crewSession{}
	return id
}

// mayBeStarting is whether instance runs a crew session that may not have got past its CLI's
// startup: of a kind that starts with its endpoint (crew.StartsWithItsEndpoint), not yet known to
// have started.
func (a *agents) mayBeStarting(instance *agentInstance) bool {
	return a.crew != nil && instance.crewed && instance.session.ID != "" &&
		crew.StartsWithItsEndpoint(instance.profile.kind()) && instance.started != instance.session.ID
}

// tellNotStarted tells the user, once for instance's session, that it hasn't finished starting, in
// its folder: nothing is typed into it, and its mail waits for it.
func (a *agents) tellNotStarted(instance *agentInstance) {
	if instance.told == instance.session.ID {
		return
	}
	instance.told = instance.session.ID
	a.c.Notify(a.c.Trf("autodoc.crew.not_started", instance.profile.Name, instance.dir))
}

func (a *agents) instanceExited(instance *agentInstance, code int) {
	instance.context = ""                         // its session ends with it: a next start registers a new one
	if instance.session.ID != "" && !a.stopping { // stopping: the window's release ends every session
		s := instance.session
		if a.mayBeStarting(instance) {
			// a Claude that exits before it started may have declined its folder-trust question: the
			// crew says whether it started before its exit ends the session (and its endpoint)
			name, dir := instance.profile.Name, instance.dir
			a.crew.startedOf(instance.profile.Codename, func(started bool) {
				if !started {
					a.c.Notify(a.c.Trf("autodoc.crew.exited_before_start", name, code, dir))
				}
				a.crew.exit(s, crew.EndedExit)
			})
		} else {
			a.crew.exit(s, crew.EndedExit)
		}
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
func (a *agents) awaitInstanceRetirement(instance *agentInstance, result feature.TerminalCompletion) {
	instance.running, instance.closing, instance.retirement = false, true, result
	code := instance.profile.Codename
	ended := a.endSession(instance)
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
			if ended == "" {
				a.startAgent(p)
			} else {
				a.crew.whenExited(ended, func() { a.startAgent(p) }) // the codename is free once the old session has ended
			}
		}
	}
	select {
	case <-result.Done():
		complete(result.Wait(context.Background()))
	default:
		go func() { err := result.Wait(context.Background()); c.Post(func() { complete(err) }) }()
	}
}

// HandTask hands a task to one exact agent incarnation, as a request through the window's agent
// messaging (agentmessaging.go): rung through the crew when the agent's CLI has a doorbell, else
// typed and sent. An immediate error rejects it without calling done; accepted calls complete done
// once on the UI loop, including cancellation and orderly owner shutdown.
func (a *agents) HandTask(to feature.AgentRef, task feature.TaskRef, instruction string, done func(error)) error {
	if task.Title == "" || !filepath.IsAbs(task.Path) || strings.ContainsAny(instruction, "\r\n") {
		return ErrAgentTask
	}
	return a.sendMessage(to, taskMessage(task, instruction), done)
}

// SendText sends text to one agent incarnation as a plain message through the window's agent
// messaging, as HandTask sends a task: the same refusals, the same consent to start a stopped agent.
func (a *agents) SendText(to feature.AgentRef, text string, done func(error)) error {
	return a.sendMessage(to, AgentMessage{Kind: MessagePlain, Body: text}, done)
}

// SendRequest sends one agent incarnation a request, subject (one line) and body, through the
// window's agent messaging, as HandTask sends a task: what the user hands an agent with an
// instruction (agents.lua's :agent-buffer and :agent-forward), which it answers.
func (a *agents) SendRequest(to feature.AgentRef, subject, body string, done func(error)) error {
	if subject == "" || strings.ContainsAny(subject, "\r\n") || body == "" {
		return ErrAgentText
	}
	return a.sendMessage(to, AgentMessage{Kind: MessageRequest, Subject: subject, Body: body}, done)
}

// present brings instance forward for a task typed into it, so the user sees it start
// (docs/decisions/todos.md#a-message-to-an-agent-is-sent-never-left-for-enter): the agents panel
// open, its tab selected, the keyboard in it, in input mode. The task is typed either way: what fails here is
// kept, never the delivery's error.
func (a *agents) present(panel feature.TerminalPanel, instance *agentInstance) {
	a.c.OpenDrawer("agent")
	a.c.Keep(a.selectInstance(instance))
	a.c.Keep(panel.SetMode(instance.tab, terminal.TerminalInput))
}

// Resume is autodoc.agents.resume: a SIGCONT to agent to's tab, to as SendText takes it, or with
// no codename the agents panel's tab now. A stopped, closing or unknown agent is ErrAgentUnavailable,
// an incarnation that is gone ErrAgentStale. A running agent is not disturbed.
func (a *agents) Resume(to feature.AgentRef) error {
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

// agentElsewhere is ErrAgentElsewhere for one delivery: the workspace the agent runs in, and why
// its doorbell didn't ring (errNoDoorbell when the window has none), so nothing could reach it.
type agentElsewhere struct {
	workspace string
	why       error
}

// errNoDoorbell is a delivery that had no doorbell to ring: the window has no crew, or the
// delivery is the base's own (a fallback job).
var errNoDoorbell = errors.New("no doorbell to ring")

func (e *agentElsewhere) Error() string {
	return fmt.Sprintf("%v (%s): %v", ErrAgentElsewhere, e.workspace, e.why)
}

func (e *agentElsewhere) Unwrap() []error { return []error{ErrAgentElsewhere, e.why} }

// deliverOpts are a delivery's choices.
type deliverOpts struct {
	present bool // once sent, the agent's panel is brought forward, so the user sees it start (present)
	// ring, when set, tries the agent's doorbell before anything is typed, for an agent already
	// running (agentmessaging.go). Its answer comes on the loop: rang ends the delivery sent;
	// errDoorbellNotSent goes on to send; any other error ends it unsent, since it may have rung.
	ring func(answer func(rang bool, err error))
	// submit, when set, follows a send that succeeded, and the delivery finishes with its answer:
	// the messaging base's Enter decision (agentmessaging.go). still checks everything again, as an
	// attempt does (finished, the workspace, the instance, its tab's owner): what submit waits for
	// lets any of them change, so it presses nothing unless still answers nil.
	submit func(panel feature.TerminalPanel, tab terminal.TerminalTabID, still func() error, finish func(error))
	// queue, when set, mails the delivery instead, queued for the agent's first receive, when nothing
	// may be typed into it because it hasn't started (ErrAgentNotStarted), and the delivery finishes
	// with its answer
	queue func(finish func(error))
}

// deliver is HandTask's and SendText's one path: to resolved, check's refusal
// after the owner's own, a stopped agent started once the user agrees (what names what is sent),
// everything checked again before send runs on the agent's tab, and done completed once on the
// loop. An agent deliver started refusing a bracketed paste is tried again, the same checks first,
// until agentReadyWait. An agent running in another workspace than the window's is rung all the
// same (its crew mail needs no tab), but never typed into: its tab works in that workspace, so a
// doorbell that didn't ring, or none, ends the delivery agentElsewhere.
func (a *agents) deliver(to feature.AgentRef, check func() error, what string, o deliverOpts, send func(feature.TerminalPanel, terminal.TerminalTabID) error, done func(error)) error {
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
	ws, ok := a.c.ActiveWorkspaceInfo()
	if !ok {
		return ErrAgentUnavailable
	}
	elsewhere := instance != nil && instance.running && (instance.workspace.name != ws.name || instance.workspace.root != ws.root)
	if elsewhere && o.ring == nil { // nothing reaches it but the base, which types only in its own workspace
		return &agentElsewhere{workspace: instance.workspace.name, why: errNoDoorbell}
	}
	ticket := c.Ticket(nil, feature.TicketWorkspace)
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
		currentWS, exists := a.c.ActiveWorkspaceInfo()
		if !exists || !ticket.Current() || currentWS.name != ws.name || currentWS.root != ws.root {
			return ErrAgentStale
		}
		current := a.instances[to.Codename]
		if instance != current || (instance != nil && (instance.closing || (to.Instance != "" && ((!instance.running && !instance.starting) || instance.token != to.Instance)))) {
			return ErrAgentStale
		}
		if saved {
			if currentProfile, exists := a.profile(to.Codename); !exists || !currentProfile.same(p) {
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
		if err == nil && a.mayBeStarting(instance) {
			// nothing is typed into a CLI that may be showing its startup question: the crew says
			// first whether it has started, asked again while an agent just started comes up
			session := instance.session.ID
			a.crew.startedOf(instance.profile.Codename, func(started bool) {
				switch {
				case accepted.finished:
				case instance.session.ID != session:
					finish(ErrAgentStale)
				case started:
					instance.started = session
					attempt(tries)
				case tries > 0:
					c.After(agentReadyPoll, func() { attempt(tries - 1) })
				case o.queue != nil:
					a.tellNotStarted(instance)
					o.queue(finish)
				default:
					a.tellNotStarted(instance)
					finish(ErrAgentNotStarted)
				}
			})
			return
		}
		if err == nil {
			err = send(panel, instance.tab)
		}
		if tries > 0 && errors.Is(err, terminal.ErrTerminalPasteUnbracketed) {
			c.After(agentReadyPoll, func() { attempt(tries - 1) })
			return
		}
		if err == nil && o.present {
			a.present(panel, instance)
		}
		if err == nil && o.submit != nil {
			tab := instance.tab
			still := func() error {
				if accepted.finished {
					return ErrAgentStale
				}
				a.syncInstances()
				if err := validate(); err != nil {
					return err
				}
				now, err := a.runtime()
				if err != nil {
					return err
				}
				if !instance.running || instance.tab != tab || !instance.owner.SameOwner(now) {
					return ErrAgentStale // another incarnation, or its tab the old parent's
				}
				return nil
			}
			o.submit(panel, tab, still, finish)
			return
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
		if started || o.ring == nil { // an agent just started has no doorbell registered yet
			attempt(tries)
			return
		}
		o.ring(func(rang bool, err error) {
			switch {
			case accepted.finished: // shutdown finished it while the doorbell rang
			case rang:
				// a task rung into an agent in another workspace isn't brought forward: its tab works
				// there, and the window shows this one
				if err := validate(); err == nil && o.present && !elsewhere {
					if panel, perr := a.runtime(); perr == nil && instance.running && instance.owner.SameOwner(panel) {
						a.present(panel, instance)
					}
				}
				finish(nil)
			case errors.Is(err, errDoorbellNotSent) && elsewhere: // nothing was sent, and the base never types there
				finish(&agentElsewhere{workspace: instance.workspace.name, why: err})
			case errors.Is(err, errDoorbellNotSent):
				attempt(tries) // nothing was sent: the base, checked again first
			default:
				finish(err)
			}
		})
	}
	if instance != nil && instance.running {
		to.Instance = instance.token // capture the resolved current incarnation before the next loop turn
		c.Post(deliver)
	} else {
		a.c.Ask(feature.Question{Title: a.c.Tr("autodoc.question.agentinstances.start_the_agent"), Text: a.c.Trf("autodoc.question.agentinstances.start_and_send", p.Name, what), Yes: a.c.Tr("autodoc.question.agentinstances.yes_start_and_send"),
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

func (c core) HandTask(to feature.AgentRef, task feature.TaskRef, instruction string, done func(error)) error {
	return c.h.agents.HandTask(to, task, instruction, done)
}
func (c core) SendText(to feature.AgentRef, text string, done func(error)) error {
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

// agentTerminalEnv is the terminal an agent runs in: AutoDoc's own (golib's Terminal emulates an
// xterm with 256 colours and truecolor). It is never inherited: AutoDoc started from the Dock or by
// launchd has no TERM at all, so a CLI that reads it (Claude Code, Antigravity) drew no colour, and
// AutoDoc started from tmux has tmux's, which names another terminal than the agent's.
var agentTerminalEnv = []string{"TERM=xterm-256color", "COLORTERM=truecolor"}

// agentEnviron is the environment an agent's CLI and its setup's server start with, layer for
// layer, a later one winning: AutoDoc's own without its spawner's identity (ownEnviron), the
// terminal it runs in (agentTerminalEnv), the profile's and AutoDoc's (instance.env: the daemon,
// the agent's PATH, the editor socket and the agent's token there), then a crewed start's session
// context and its setup's. All of it is the process's environment, readable by the user alone,
// never its command line.
func agentEnviron(env procenv.Env, instance *agentInstance) []string {
	environ := slices.Concat(feature.OwnEnviron(env), agentTerminalEnv, instance.env)
	if instance.crewed {
		environ = append(environ, crew.EnvContext+"="+instance.context)
		for _, k := range slices.Sorted(maps.Keys(instance.setup.Env)) {
			environ = append(environ, k+"="+instance.setup.Env[k])
		}
	}
	return environ
}

// agentHost adds only the services needed to configure an agent terminal and
// populate its profile form. Agent-specific state remains in the agents feature.
type agentHost interface {
	feature.PanelHost
	// SetFormText resets edited form text even if its source value is unchanged.
	SetFormText(name, value string)
	ConfigureTerminal(id, command, dir string, args ...string) error
	DaemonAddress() string
	// ActiveWorkspaceInfo is the workspace in use, all the agents read of it (its id, its folder
	// settings); PanelHost's ActiveWorkspace is the kit's Workspace.
	ActiveWorkspaceInfo() (wsInfo, bool)
}
