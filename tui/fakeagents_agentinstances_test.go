package tui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/tui/pty"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// Copies of the agents' test fakes agentinstances_test.go declares, for the host's cells.

type modelRemovalCopy struct {
	done chan struct{}
	err  error
}

func (r *modelRemovalCopy) Done() <-chan struct{}

func (r *modelRemovalCopy) Wait(context.Context) error { <-r.done; return r.err }

func (*modelRemovalCopy) Generation() int { return 1 }

type modelPanelCopy struct {
	owner     *identityHostCopy
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
	removal     *modelRemovalCopy
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
type fakeSeamsCopy struct {
	env             procenv.Env
	serverReady     time.Duration
	offerAgentSetup bool
	lookAgentCLI    func(string) (string, error)
	vimPending      bool
}

// newFakeSeams are the program's own seams for the agents: a server's socket waited for 10 s, the
// agent setup offered, no Vim question pending.
func newFakeSeamsCopy() *fakeSeamsCopy {
	return &fakeSeamsCopy{serverReady: 10 * time.Second, offerAgentSetup: true}
}

func (s *fakeSeamsCopy) Env() procenv.Env { return s.env }

func (s *fakeSeamsCopy) ServerReady() time.Duration { return s.serverReady }

func (s *fakeSeamsCopy) OfferAgentSetup() bool { return s.offerAgentSetup }

func (s *fakeSeamsCopy) LookAgentCLI() func(string) (string, error) { return s.lookAgentCLI }

func (s *fakeSeamsCopy) VimQuestionPending(feature.PrefsView) bool { return s.vimPending }

// testAgents is newAgents for cells whose profiles name programs that don't exist ("one", "two"):
// their commands aren't looked up on a PATH (commandCheck off); a cell of that check sets it. Its
// seams are newFakeSeams'.
func testAgentsCopy(c agentHost, dir, config string) *agents {
	s := defaultSeams
	return testAgentsWithCopy(c, &s, dir, config)
}

// testAgentsWith is testAgents with the seams s (a test's tune, its environment's included).
func testAgentsWithCopy(c agentHost, s agentSeams, dir, config string) *agents {
	a := newAgents(c, s, dir, config)
	a.commandCheck = nil
	return a
}

func (p *modelPanelCopy) SameOwner(other feature.TerminalPanel) bool {
	peer, ok := other.(*modelPanelCopy)
	return ok && p == peer
}

func (p *modelPanelCopy) Shutdown() feature.TerminalCompletion {
	for i := range p.tabs {
		p.tabs[i].Running = false
	}
	if p.removal == nil {
		p.removal = &modelRemovalCopy{done: make(chan struct{})}
		close(p.removal.done)
	}
	return p.removal
}

func (p *modelPanelCopy) Add(label string, config feature.TerminalConfig) (terminal.TerminalTabID, error) {
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

func (p *modelPanelCopy) Select(id terminal.TerminalTabID) error {
	if p.selectErr != nil {
		return p.selectErr
	}
	p.active = id
	return nil
}

func (p *modelPanelCopy) Start(id terminal.TerminalTabID) error {
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
	return ErrAgentUnavailable
}

// Resume keeps the tab it was asked for.
func (p *modelPanelCopy) Resume(id terminal.TerminalTabID) error {
	p.resumed = append(p.resumed, id)
	return nil
}

// SendText records literal keys sent to a tab (the messaging base's Enter).
func (p *modelPanelCopy) SendText(id terminal.TerminalTabID, text string) error {
	if p.sendErr != nil {
		return p.sendErr
	}
	p.keys = append(p.keys, text)
	return nil
}

// SendBracketedPaste is SendPaste, recorded as bracketed, once the programs take them (unbracketed).
func (p *modelPanelCopy) SendBracketedPaste(id terminal.TerminalTabID, text string) error {
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

func (p *modelPanelCopy) SetMode(id terminal.TerminalTabID, mode terminal.TerminalMode) error {
	p.modes = append(p.modes, fmt.Sprintf("%s:%v", id, mode))
	return nil
}

func (p *modelPanelCopy) Remove(id terminal.TerminalTabID) (feature.TerminalRemoval, error) {
	if p.owner != nil && p.owner.stopErr != nil {
		return nil, p.owner.stopErr
	}
	if p.removal == nil {
		p.removal = &modelRemovalCopy{done: make(chan struct{})}
		close(p.removal.done)
	}
	return p.removal, nil
}

func (*modelPanelCopy) ForceRemove(terminal.TerminalTabID) error { return nil }

func (p *modelPanelCopy) Tabs() []terminal.TerminalTab {
	return append([]terminal.TerminalTab(nil), p.tabs...)
}

func (p *modelPanelCopy) Active() terminal.TerminalTabID { return p.active }

func (*modelPanelCopy) Actions(func(), func(terminal.TerminalTabID)) {}

func (p *modelPanelCopy) Menus(add func() []widgetcore.MenuItemModel, tab func(terminal.TerminalTabID) []widgetcore.MenuItemModel) {
	p.addMenu, p.tabMenu = add, tab
}

func (p *modelPanelCopy) exit(id terminal.TerminalTabID, code int) {
	for i := range p.tabs {
		if p.tabs[i].ID == id {
			p.tabs[i].Running = false
		}
	}
	if config, found := p.configs[id]; found && config.Exited != nil {
		config.Exited(code)
	}
}

func (c *identityHostCopy) Terminals(string) (feature.TerminalPanel, error) {
	if c.panel == nil {
		c.panel = &modelPanelCopy{owner: c}
	}
	return c.panel, nil
}

func (c *identityHostCopy) OpenDrawer(id string) { c.opened = append(c.opened, id) }

func (c *identityHostCopy) CloseDrawer(id string) { c.closed = append(c.closed, id) }

func (c *identityHostCopy) Post(fn func()) {
	c.postMu.Lock()
	defer c.postMu.Unlock()
	c.posts = append(c.posts, fn)
}

func (c *identityHostCopy) After(d time.Duration, fn func()) {
	c.delays = append(c.delays, d)
	c.afters = append(c.afters, fn)
}

// fireAfters runs the waits After scheduled, as their timers would, then what they posted.
func (c *identityHostCopy) fireAfters() {
	for len(c.afters) > 0 {
		afters := c.afters
		c.afters = nil
		for _, f := range afters {
			f()
		}
		c.drainPosts()
	}
}

func (c *identityHostCopy) Ticket(_ *uint64, _ feature.TicketScope) feature.Ticket {
	epoch := c.epoch
	return feature.NewTicket(func() bool { return c.epoch == epoch })
}

func (c *identityHostCopy) Ask(q feature.Question) feature.Asked {
	c.questions = append(c.questions, q)
	return feature.Asked{}
}

func (c *identityHostCopy) drainPosts() {
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
func (c *identityHostCopy) settle(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s never came", what)
		}
		c.drainPosts()
	}
}

func modelAgentsCopy(t *testing.T) (*agents, *identityHostCopy) {
	t.Helper()
	c := newIdentityHostCopy(`[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`)
	c.ws = wsInfo{name: "kb", root: t.TempDir()}
	a := testAgentsCopy(c, t.TempDir(), "")
	return a, c
}
