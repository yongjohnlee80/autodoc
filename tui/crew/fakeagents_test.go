package crew

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	corecrew "github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/tui/pty"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// tui/agents' test fakes the crew mail's cells run its agents on (a window, its terminal panel,
// the program's seams): copies, each naming its original; the agents' own stay there.

// A copy of tui/agents' taskFile (agentfixtures_app_test.go).
// taskFile is a slash path from a root (/kb/.todo-list/open/t1.md) as an absolute path on this OS:
// itself on Unix; on Windows from the temporary folder's drive (C:\kb\.todo-list\open\t1.md), as a
// task's file is, where /kb/... is not absolute.
func taskFile(p string) string {
	return filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), filepath.FromSlash(p))
}

// A copy of tui/agents' oneTwo (agentfixtures_crewsession_test.go).
const oneTwo = `[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`

// A copy of tui/agents' startedEnv (agentfixtures_crewsession_test.go).
// startedEnv is the variables instance's tab starts its program with (its environment, a later
// entry winning), through the tab's own start (runAgent), its program not run; err is the start's
// refusal.
func startedEnv(a *agents.Agents, c *identityHost, instance *agents.AgentInstance) (env []string, spawned bool, err error) {
	cmd, spawned, err := startedCmd(a, c, instance)
	return cmd.Env, spawned, err
}

// A copy of tui/agents' startedCmd (agentfixtures_crewsession_test.go).
// startedCmd is the command instance's tab starts its program with, as startedEnv. Its process
// is a fake terminal, never nil: a Codex agent's server is bound to it, and closes it when it
// ends (as the cell's cleanup ends it).
func startedCmd(a *agents.Agents, c *identityHost, instance *agents.AgentInstance) (started pty.Cmd, spawned bool, err error) {
	cfg := c.panel.configs[instance.State().Tab]
	spawn := fakeSeamsOf(a).spawn
	defer func() { fakeSeamsOf(a).spawn = spawn }()
	fakeSeamsOf(a).spawn = func(cmd pty.Cmd) (terminal.TerminalProcess, error) {
		started, spawned = cmd, true
		return newFakeTerminal(), nil
	}
	_, err = cfg.Start(pty.Cmd{Path: cfg.Command, Args: cfg.Args, Dir: cfg.Dir})
	return started, spawned, err
}

// A copy of tui/agents' newFakeTerminal (agentfixtures_sharedhelpers_test.go).
func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{ended: make(chan struct{}), closed: make(chan struct{})}
}

// A copy of tui/agents' fakeTerminal (agentfixtures_sharedhelpers_test.go).
// fakeTerminal is an agent CLI's process: Wait returns once ended, Close records the terminal closed.
type fakeTerminal struct {
	terminal.TerminalProcess
	ended  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (f *fakeTerminal) Close() error { f.once.Do(func() { close(f.closed) }); return nil }

func (f *fakeTerminal) Wait() (int, error) { <-f.ended; return 0, nil }

func (f *fakeTerminal) Resize(_, _ int) error { return nil }

// A copy of tui/agents' identityHost (agentidentity_test.go).
// Exercise profile persistence/validation without starting a terminal. Other
// panel services remain absent so an invalid target cannot accidentally spawn.
type identityHost struct {
	agents.AgentHost
	p                 fakePrefs
	state             map[string]any
	notes             []string
	stopErr           error
	ws                feature.Workspace
	configuredCommand string
	panel             *modelPanel
	postMu            sync.Mutex // a held removal's retirement posts from its own goroutine
	posts             []func()
	afters            []func()
	delays            []time.Duration
	questions         []feature.Question
	epoch             int
	closed            []string // the drawers CloseDrawer closed, in order
	opened            []string // the drawers OpenDrawer opened, in order
}

// A copy of tui/agents' fakePrefs (agentidentity_test.go).
// fakePrefs is the preferences a fake host answers (feature.PrefsView): each value by its name, no
// panel's geometry.
type fakePrefs struct{ values map[string]string }

func (p fakePrefs) Value(name string) string { return p.values[name] }

func (fakePrefs) PanelGeometry(string, string) (int, int) { return 0, 0 }

// A copy of tui/agents' newIdentityHost (agentidentity_test.go).
func newIdentityHost(raw string) *identityHost {
	return &identityHost{p: fakePrefs{values: map[string]string{
		agents.PrefAgentProfiles: raw, agents.PrefAgentDefaultCodename: "",
	}}, state: map[string]any{}}
}

func (c *identityHost) Prefs() feature.PrefsView { return c.p }

func (c *identityHost) SetPref(name, value string) { c.p.values[name] = value }

func (c *identityHost) Set(name string, value any) { c.state[name] = value }
func (c *identityHost) Say(msg string)             { c.state["App.status"] = msg }

func (c *identityHost) SetFormText(name, value string) { c.Set(name, value) }

func (*identityHost) Open(string) {}

func (*identityHost) Close(string) {}

func (*identityHost) Focus(string) error { return nil }

func (*identityHost) DaemonAddress() string { return "unused" }

func (c *identityHost) ActiveWorkspace() (feature.Workspace, bool) {
	return c.ws, c.ws.Name != ""
}

func (c *identityHost) ActiveWorkspaceInfo() (feature.Workspace, bool) {
	return c.ws, c.ws.Name != ""
}

func (c *identityHost) ConfigureTerminal(_, _, _ string, args ...string) error {
	if len(args) > 0 {
		c.configuredCommand = args[len(args)-1]
	}
	return nil
}

func (c *identityHost) Notify(msg string) { c.notes = append(c.notes, msg) }

func (*identityHost) Tr(id string) string { return englishText(id) }

func (f *identityHost) Trf(id string, args ...any) string {
	return feature.Fill(englishText(id), args...)
}

func (c *identityHost) WidgetsChanged() {}

func (c *identityHost) CallView(string, string, ...any) error { return c.stopErr }

func (c *identityHost) Keep(err error) {
	if err != nil {
		c.notes = append(c.notes, err.Error())
	}
}

// A copy of tui/agents' modelRemoval (agentinstances_test.go).
type modelRemoval struct {
	done chan struct{}
	err  error
}

func (r *modelRemoval) Done() <-chan struct{} { return r.done }

func (r *modelRemoval) Wait(context.Context) error { <-r.done; return r.err }

func (*modelRemoval) Generation() int { return 1 }

// A copy of tui/agents' modelPanel (agentinstances_test.go).
type modelPanel struct {
	owner     *identityHost
	configs   map[terminal.TerminalTabID]feature.TerminalConfig
	tabs      []terminal.TerminalTab
	active    terminal.TerminalTabID
	next      int
	pastes    []string
	keys      []string                 // each SendText: the messaging base's Enter
	resumed   []terminal.TerminalTabID // each Resume's tab
	modes     []string                 // each SetMode, tab:mode
	sendErr   error
	startErr  error // what Start answers instead of starting, when set
	selectErr error // what Select answers instead of selecting, when set
	// unbracketed is how many bracketed pastes the programs refuse before they take them, a program
	// still starting; below zero, they never do
	unbracketed int
	removal     *modelRemoval
	addMenu     func() []widgetcore.MenuItemModel
	tabMenu     func(terminal.TerminalTabID) []widgetcore.MenuItemModel
	// starts runs a tab's starter at Start, as the terminal does, and keeps what each failed one
	// said on its screen (reported, tab: error); unset, Start only marks the tab running
	starts   bool
	reported []string
}

// A copy of tui/agents' fakeSeams (agentinstances_test.go).
// fakeSeams are the program's seams as the agents' cells set them (agentSeams): its environment
// (nil, AutoDoc's own), the wait for an agent server's socket, whether a first run offers the agent
// setup, how the setup finds the CLIs (nil, on the PATH) and whether a first run's Vim question is
// still to be asked.
type fakeSeams struct {
	env             procenv.Env
	serverReady     time.Duration
	offerAgentSetup bool
	lookAgentCLI    func(string) (string, error)
	vimPending      bool
	// the agents' hooks (agentSeams), each nil or "" for the program's own
	setupRun        func(kind, dir string, environ []string) (corecrew.Setup, error)
	spawn           func(pty.Cmd) (terminal.TerminalProcess, error)
	commandCheck    func(shell usershell.Shell, command string, env []string) error
	codexHere       func(environ []string, args ...string) error
	promptsReported func(kind string, reported bool) bool
	agentBin        string
}

// A copy of tui/agents' newFakeSeams (agentinstances_test.go).
// newFakeSeams are the program's seams as the agents' cells start from them, as the host's test
// program sets its own: a server's socket waited for 10 s, no first-run setup offered (a setup cell
// turns it on), no Vim question pending, and
// no command looked up on a PATH (a cell of that check sets commandOnPath).
func newFakeSeams() *fakeSeams {
	return &fakeSeams{serverReady: 10 * time.Second, commandCheck: noCommandCheck}
}

func (s *fakeSeams) Env() procenv.Env { return s.env }

func (s *fakeSeams) ServerReady() time.Duration { return s.serverReady }

func (s *fakeSeams) OfferAgentSetup() bool { return s.offerAgentSetup }

func (s *fakeSeams) LookAgentCLI() func(string) (string, error) { return s.lookAgentCLI }

func (s *fakeSeams) VimQuestionPending(feature.PrefsView) bool { return s.vimPending }

func (s *fakeSeams) SetupRun() func(kind, dir string, environ []string) (corecrew.Setup, error) {
	return s.setupRun
}

func (s *fakeSeams) Spawn() func(pty.Cmd) (terminal.TerminalProcess, error) { return s.spawn }

func (s *fakeSeams) CommandCheck() func(shell usershell.Shell, command string, env []string) error {
	return s.commandCheck
}

func (s *fakeSeams) CodexHere() func(environ []string, args ...string) error { return s.codexHere }

func (s *fakeSeams) PromptsReported() func(kind string, reported bool) bool { return s.promptsReported }

func (s *fakeSeams) AgentBin() string { return s.agentBin }

// A copy of tui/agents' noCommandCheck (agentinstances_test.go).
// noCommandCheck checks no command: the cells' profiles name programs that don't exist ("one",
// "two").
func noCommandCheck(usershell.Shell, string, []string) error { return nil }

// A copy of tui/agents' testAgents (agentinstances_test.go).
// testAgents is newAgents for cells whose profiles name programs that don't exist ("one", "two"),
// on newFakeSeams: their commands aren't looked up on a PATH.
func testAgents(c agents.AgentHost, dir, config string) *agents.Agents {
	return testAgentsWith(c, newFakeSeams(), dir, config)
}

func (p *modelPanel) SameOwner(other feature.TerminalPanel) bool {
	peer, ok := other.(*modelPanel)
	return ok && p == peer
}

func (p *modelPanel) Shutdown() feature.TerminalCompletion {
	for i := range p.tabs {
		p.tabs[i].Running = false
	}
	if p.removal == nil {
		p.removal = &modelRemoval{done: make(chan struct{})}
		close(p.removal.done)
	}
	return p.removal
}

func (p *modelPanel) Add(label string, config feature.TerminalConfig) (terminal.TerminalTabID, error) {
	p.next++
	id := terminal.TerminalTabID(fmt.Sprintf("tab-%d", p.next))
	if p.configs == nil {
		p.configs = map[terminal.TerminalTabID]feature.TerminalConfig{}
	}
	p.configs[id] = config
	p.tabs = append(p.tabs, terminal.TerminalTab{ID: id, Label: label})
	if p.owner != nil && len(config.Args) > 0 {
		p.owner.configuredCommand = config.Args[len(config.Args)-1]
	}
	return id, nil
}

func (p *modelPanel) Select(id terminal.TerminalTabID) error {
	if p.selectErr != nil {
		return p.selectErr
	}
	p.active = id
	return nil
}

func (p *modelPanel) Start(id terminal.TerminalTabID) error {
	if p.startErr != nil {
		return p.startErr
	}
	if cfg, ok := p.configs[id]; ok && p.starts && cfg.Start != nil {
		// as the terminal does: its starter's error is shown in the tab, which stays stopped
		if _, err := cfg.Start(pty.Cmd{Path: cfg.Command, Args: cfg.Args, Dir: cfg.Dir}); err != nil {
			p.reported = append(p.reported, string(id)+": "+err.Error())
			return err
		}
	}
	for i := range p.tabs {
		if p.tabs[i].ID == id {
			p.tabs[i].Running = true
			p.tabs[i].Generation++
			return nil
		}
	}
	return agents.ErrAgentUnavailable
}

// Resume keeps the tab it was asked for.
func (p *modelPanel) Resume(id terminal.TerminalTabID) error {
	p.resumed = append(p.resumed, id)
	return nil
}

// SendText records literal keys sent to a tab (the messaging base's Enter).
func (p *modelPanel) SendText(id terminal.TerminalTabID, text string) error {
	if p.sendErr != nil {
		return p.sendErr
	}
	p.keys = append(p.keys, text)
	return nil
}

// SendBracketedPaste is SendPaste, recorded as bracketed, once the programs take them (unbracketed).
func (p *modelPanel) SendBracketedPaste(id terminal.TerminalTabID, text string) error {
	if p.sendErr != nil {
		return p.sendErr
	}
	if p.unbracketed != 0 {
		if p.unbracketed > 0 {
			p.unbracketed--
		}
		return terminal.ErrTerminalPasteUnbracketed
	}
	p.pastes = append(p.pastes, string(id)+":[bracketed]"+text)
	return nil
}

func (p *modelPanel) SetMode(id terminal.TerminalTabID, mode terminal.TerminalMode) error {
	p.modes = append(p.modes, fmt.Sprintf("%s:%v", id, mode))
	return nil
}

func (p *modelPanel) Remove(id terminal.TerminalTabID) (feature.TerminalRemoval, error) {
	if p.owner != nil && p.owner.stopErr != nil {
		return nil, p.owner.stopErr
	}
	if p.removal == nil {
		p.removal = &modelRemoval{done: make(chan struct{})}
		close(p.removal.done)
	}
	return p.removal, nil
}

func (*modelPanel) ForceRemove(terminal.TerminalTabID) error { return nil }

func (p *modelPanel) Tabs() []terminal.TerminalTab {
	return append([]terminal.TerminalTab(nil), p.tabs...)
}

func (p *modelPanel) Active() terminal.TerminalTabID { return p.active }

func (*modelPanel) Actions(func(), func(terminal.TerminalTabID)) {}

func (p *modelPanel) Menus(add func() []widgetcore.MenuItemModel, tab func(terminal.TerminalTabID) []widgetcore.MenuItemModel) {
	p.addMenu, p.tabMenu = add, tab
}

func (p *modelPanel) exit(id terminal.TerminalTabID, code int) {
	for i := range p.tabs {
		if p.tabs[i].ID == id {
			p.tabs[i].Running = false
		}
	}
	if config, found := p.configs[id]; found && config.Exited != nil {
		config.Exited(code)
	}
}

func (c *identityHost) Terminals(string) (feature.TerminalPanel, error) {
	if c.panel == nil {
		c.panel = &modelPanel{owner: c}
	}
	return c.panel, nil
}

func (c *identityHost) OpenDrawer(id string) { c.opened = append(c.opened, id) }

func (c *identityHost) CloseDrawer(id string) { c.closed = append(c.closed, id) }

func (c *identityHost) Post(fn func()) {
	c.postMu.Lock()
	defer c.postMu.Unlock()
	c.posts = append(c.posts, fn)
}

func (c *identityHost) After(d time.Duration, fn func()) {
	c.delays = append(c.delays, d)
	c.afters = append(c.afters, fn)
}

// fireAfters runs the waits After scheduled, as their timers would, then what they posted.
func (c *identityHost) fireAfters() {
	for len(c.afters) > 0 {
		afters := c.afters
		c.afters = nil
		for _, f := range afters {
			f()
		}
		c.drainPosts()
	}
}

func (c *identityHost) Ticket(_ *uint64, _ feature.TicketScope) feature.Ticket {
	epoch := c.epoch
	return feature.NewTicket(func() bool { return c.epoch == epoch })
}

func (c *identityHost) Ask(q feature.Question) feature.Asked {
	c.questions = append(c.questions, q)
	return feature.Asked{}
}

func (c *identityHost) drainPosts() {
	for {
		c.postMu.Lock()
		posts := c.posts
		c.posts = nil
		c.postMu.Unlock()
		if len(posts) == 0 {
			return
		}
		for _, f := range posts {
			f()
		}
	}
}

// settle runs what a held removal's retirement posts, as the loop would, until done holds.
func (c *identityHost) settle(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s never came", what)
		}
		c.drainPosts()
	}
}

// A copy of tui/agents' modelAgents (agentinstances_test.go).
func modelAgents(t *testing.T) (*agents.Agents, *identityHost) {
	t.Helper()
	c := newIdentityHost(`[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`)
	c.ws = feature.Workspace{Name: "kb", Root: t.TempDir()}
	a := testAgents(c, t.TempDir(), "")
	return a, c
}
