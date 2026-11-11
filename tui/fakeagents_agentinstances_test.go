package tui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/tui/pty"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// Copies of the agents' test fakes agentinstances_test.go declares, for the host's cells.

type modelRemoval struct {
	done chan struct{}
	err  error
}

func (r *modelRemoval) Done() <-chan struct{} { return r.done }

func (r *modelRemoval) Wait(context.Context) error { <-r.done; return r.err }

func (*modelRemoval) Generation() int { return 1 }

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
	setupRun        func(kind, dir string, environ []string) (crew.Setup, error)
	spawn           func(pty.Cmd) (terminal.TerminalProcess, error)
	commandCheck    func(shell usershell.Shell, command string, env []string) error
	codexHere       func(environ []string, args ...string) error
	promptsReported func(kind string, reported bool) bool
	agentBin        string
}

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

func (s *fakeSeams) SetupRun() func(kind, dir string, environ []string) (crew.Setup, error) {
	return s.setupRun
}

func (s *fakeSeams) Spawn() func(pty.Cmd) (terminal.TerminalProcess, error) { return s.spawn }

func (s *fakeSeams) CommandCheck() func(shell usershell.Shell, command string, env []string) error {
	return s.commandCheck
}

func (s *fakeSeams) CodexHere() func(environ []string, args ...string) error { return s.codexHere }

func (s *fakeSeams) PromptsReported() func(kind string, reported bool) bool {
	return s.promptsReported
}

func (s *fakeSeams) AgentBin() string { return s.agentBin }

// noCommandCheck checks no command: the cells' profiles name programs that don't exist ("one",
// "two").
func noCommandCheck(usershell.Shell, string, []string) error { return nil }

// testAgents is newAgents for cells whose profiles name programs that don't exist ("one", "two"),
// on newFakeSeams: their commands aren't looked up on a PATH.
func testAgents(c agents.AgentHost, dir, config string) *agents.Agents {
	s := defaultSeams
	return testAgentsWith(c, &s, dir, config)
}

// testAgentsWith is testAgents with the seams s (a test's tune, its environment's included).
func testAgentsWith(c agents.AgentHost, s agents.AgentSeams, dir, config string) *agents.Agents {
	hs, ok := s.(*seams)
	if ok && hs.agentCommandCheck == nil {
		hs.agentCommandCheck = noCommandCheck // as the agents' own testAgents: no command looked up
	}
	a := agents.NewAgents(c, s, dir, config)
	if ok {
		agentsSeams.Lock()
		agentsSeams.m[a] = hs // seamsOf: the cell sets its hooks there
		agentsSeams.Unlock()
	}
	return a
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

// StartAsync is Start, answered at once: the model spawns nothing.
func (p *modelPanel) StartAsync(id terminal.TerminalTabID, done func(error)) {
	err := p.Start(id)
	if done != nil {
		done(err)
	}
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

func modelAgents(t *testing.T) (*agents.Agents, *identityHost) {
	t.Helper()
	c := newIdentityHost(`[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`)
	c.ws = wsInfo{name: "kb", root: t.TempDir()}
	a := testAgents(c, t.TempDir(), "")
	return a, c
}
