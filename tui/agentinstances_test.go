package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/tui/pty"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

type modelRemoval struct {
	done chan struct{}
	err  error
}

func (r *modelRemoval) Done() <-chan struct{}      { return r.done }
func (r *modelRemoval) Wait(context.Context) error { <-r.done; return r.err }
func (*modelRemoval) Generation() int              { return 1 }

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

// testAgents is newAgents for cells whose profiles name programs that don't exist ("one", "two"):
// their commands aren't looked up on a PATH (commandCheck off); a cell of that check sets it.
func testAgents(c agentHost, dir, config string) *agents {
	s := defaultSeams
	return testAgentsWith(c, &s, dir, config)
}

// testAgentsWith is testAgents with the seams s (seamsFor: a test's tune, its environment's
// included).
func testAgentsWith(c agentHost, s *seams, dir, config string) *agents {
	a := newAgents(c, s, dir, config)
	a.commandCheck = nil
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
	return ErrAgentUnavailable
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
func (p *modelPanel) Active() terminal.TerminalTabID             { return p.active }
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
func (c *identityHost) OpenDrawer(id string)  { c.opened = append(c.opened, id) }
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

func modelAgents(t *testing.T) (*agents, *identityHost) {
	t.Helper()
	c := newIdentityHost(`[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`)
	c.ws = wsInfo{name: "kb", root: t.TempDir()}
	a := testAgents(c, t.TempDir(), "")
	return a, c
}

func retainModelInstance(a *agents, c *identityHost, code, label string) {
	panel, _ := c.Terminals("agentView")
	id, _ := panel.Add(label, feature.TerminalConfig{})
	_ = panel.Start(id)
	p, exists := a.profile(code)
	if !exists {
		p = agentProfile{Name: label, Codename: code, Command: "orphan"}
	}
	a.instances[code] = &agentInstance{profile: p, tab: id, token: "retained-" + code, running: true, owner: panel, generation: 1, workspace: workspaceOf(c.ws)}
	a.instanceOrder = append(a.instanceOrder, code)
	a.selectedAgent, a.agentRunning, a.agentRunningLabel = code, code, label
}

func TestAgentInstancesRunTogetherAndRefsDoNotFollowRestarts(t *testing.T) {
	a, c := modelAgents(t)
	one, _ := a.profile("one")
	two, _ := a.profile("two")
	first, err := a.startInstance(one)
	if err != nil {
		t.Fatal(err)
	}
	old := feature.AgentRef{Codename: "one", Instance: first.token}
	if _, err := a.startInstance(two); err != nil {
		t.Fatal(err)
	}
	if !first.running || len(a.AgentEntries()) != 2 || !a.instances["two"].running {
		t.Fatal("starting another agent stopped the first")
	}
	c.panel.exit(first.tab, 0)
	if _, err := a.startInstance(one); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	err = a.HandTask(old, feature.TaskRef{Title: "task", Path: "/tasks/task.md"}, "work", func(error) { callbacks++ })
	if !errors.Is(err, ErrAgentStale) || callbacks != 0 || len(c.posts) != 0 {
		t.Fatal("stale ref was accepted or completed")
	}
}

func TestHandTaskAcceptedDeliverySnapshotsAndCompletesOnce(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	c.opened = nil                                              // the start opened the panel: what the delivery opens is counted from here
	a.promptsReported = func(string, bool) bool { return true } // a CLI that reports its prompts: the base sends
	done := 0
	task := feature.TaskRef{Title: "original", Path: "/tasks/one.md", ID: "one"}
	if err := a.HandTask(feature.AgentRef{Codename: "one"}, task, "inspect", func(err error) {
		if err != nil {
			t.Error(err)
		}
		done++
	}); err != nil {
		t.Fatal(err)
	}
	task.Title = "changed"
	if done != 0 {
		t.Fatal("running delivery completed outside its later loop turn")
	}
	c.drainPosts()
	tab := string(instance.tab)
	if done != 0 || !slices.Equal(c.panel.pastes, []string{tab + ":[bracketed]Task: original\nFile: /tasks/one.md\nID: one\nInstruction: inspect"}) {
		t.Fatalf("the task goes as one bracketed paste, done waiting for its Enter: done=%d pastes=%q", done, c.panel.pastes)
	}
	// the Enter that sends it follows the paste after submitDelay (agentmessaging.go), never with it,
	// and the delivery is done with its answer
	if !slices.Equal(c.delays, []time.Duration{submitDelay}) || len(c.panel.keys) != 0 {
		t.Fatalf("the Enter isn't the paste's follower: delays=%v keys=%q", c.delays, c.panel.keys)
	}
	c.fireAfters()
	if done != 1 || !slices.Equal(c.panel.keys, []string{"\r"}) {
		t.Fatalf("the Enter sends it, then done: done=%d keys=%q", done, c.panel.keys)
	}
	if !slices.Equal(c.opened, []string{"agent"}) || c.panel.active != instance.tab || !slices.Equal(c.panel.modes, []string{fmt.Sprintf("%s:%v", tab, terminal.TerminalInput)}) {
		t.Fatalf("the agent's panel is brought forward for the Enter: opened=%q active=%q modes=%q", c.opened, c.panel.active, c.panel.modes)
	}
	c.drainPosts()
	a.Stopping()
	if done != 1 {
		t.Fatal("completed delivery completed again at shutdown")
	}
}

func TestHandTaskStoppedConsentRevalidatesAndNeverSendsAfterCancellation(t *testing.T) {
	for _, ending := range []string{"no", "workspace", "profile", "shutdown", "yes"} {
		t.Run(ending, func(t *testing.T) {
			a, c := modelAgents(t)
			a.promptsReported = func(string, bool) bool { return true } // delivered is sent: the base presses Enter
			done := 0
			var outcome error
			if err := a.HandTask(feature.AgentRef{Codename: "one"}, feature.TaskRef{Title: "task", Path: "/tasks/task.md"}, "work", func(err error) { done++; outcome = err }); err != nil {
				t.Fatal(err)
			}
			if len(c.questions) != 1 || len(a.instances) != 0 {
				t.Fatal("stopped target started before consent")
			}
			q := c.questions[0]
			switch ending {
			case "no":
				q.OnCancel()
			case "workspace":
				c.epoch++
				q.OnYes()
			case "profile":
				c.p.values[prefAgentProfiles] = `[]`
				q.OnYes()
			case "shutdown":
				a.Stopping()
				q.OnCancel()
			case "yes":
				q.OnYes()
			}
			c.drainPosts()
			c.fireAfters()
			if done != 1 {
				t.Fatalf("completion count %d", done)
			}
			if ending == "yes" {
				if outcome != nil || c.panel == nil || len(c.panel.pastes) != 1 {
					t.Fatal("consented target was not started and delivered")
				}
			} else {
				if outcome == nil || len(a.instances) != 0 || (c.panel != nil && len(c.panel.pastes) > 0) {
					t.Fatal("cancelled/stale request started or sent")
				}
			}
		})
	}
}

func TestHandTaskQueuedTargetReplacementAndSendFailureAreCompletionErrors(t *testing.T) {
	for _, failure := range []string{"replacement", "write"} {
		t.Run(failure, func(t *testing.T) {
			a, c := modelAgents(t)
			p, _ := a.profile("one")
			instance, err := a.startInstance(p)
			if err != nil {
				t.Fatal(err)
			}
			var outcome error
			calls := 0
			if err := a.HandTask(feature.AgentRef{Codename: "one", Instance: instance.token}, feature.TaskRef{Title: "task", Path: "/task.md"}, "work", func(err error) { outcome = err; calls++ }); err != nil {
				t.Fatal(err)
			}
			if failure == "replacement" {
				instance.token = "replacement"
			} else {
				c.panel.sendErr = errors.New("queue full")
			}
			c.drainPosts()
			c.fireAfters()
			if outcome == nil || calls != 1 || len(c.panel.pastes) != 0 {
				t.Fatal("deferred failure sent or missed completion")
			}
		})
	}
}

// TestHandTaskWaitsForAStartedAgentToTakeBracketedPastes
// (docs/decisions/todos.md#text-reaches-an-agent-only-as-a-bracketed-paste): an agent HandTask
// starts has a program still starting, which takes no bracketed paste yet. The paste is tried again
// agentReadyPoll apart: taken once the program takes it, and never sent as keys meanwhile.
func TestHandTaskWaitsForAStartedAgentToTakeBracketedPastes(t *testing.T) {
	a, c := modelAgents(t)
	a.promptsReported = func(string, bool) bool { return true } // a CLI that reports its prompts: the base sends
	c.panel = &modelPanel{owner: c, unbracketed: 3}
	calls := 0
	var outcome error
	if err := a.HandTask(feature.AgentRef{Codename: "one"}, feature.TaskRef{Title: "task", Path: "/task.md"}, "work", func(err error) { calls++; outcome = err }); err != nil {
		t.Fatal(err)
	}
	c.questions[0].OnYes()
	c.drainPosts()
	if len(c.panel.pastes) != 0 || calls != 0 || !slices.Equal(c.delays, []time.Duration{agentReadyPoll}) {
		t.Fatalf("a starting program: pastes=%q calls=%d delays=%v", c.panel.pastes, calls, c.delays)
	}
	c.fireAfters()
	// three polls until it takes the paste, then the Enter's gap (agentmessaging.go)
	if calls != 1 || outcome != nil || len(c.panel.pastes) != 1 ||
		!slices.Equal(c.delays, []time.Duration{agentReadyPoll, agentReadyPoll, agentReadyPoll, submitDelay}) || !slices.Equal(c.panel.keys, []string{"\r"}) {
		t.Fatalf("once it takes bracketed pastes: calls=%d outcome=%v pastes=%q delays=%v keys=%q", calls, outcome, c.panel.pastes, c.delays, c.panel.keys)
	}
	if want := []string{fmt.Sprintf("%s:%v", a.instances["one"].tab, terminal.TerminalInput)}; !slices.Equal(c.panel.modes, want) {
		t.Errorf("the agent's tab was not put in input mode for the Enter: %q", c.panel.modes)
	}
}

// TestHandTaskToAnUnbracketedProgramSendsNothing
// (docs/decisions/todos.md#text-reaches-an-agent-only-as-a-bracketed-paste): a program that never
// takes bracketed pastes gets nothing, as keys or otherwise. A running one refuses at once; one
// HandTask started is given agentReadyWait. done hears widget.ErrTerminalPasteUnbracketed once, and
// no panel is brought forward.
func TestHandTaskToAnUnbracketedProgramSendsNothing(t *testing.T) {
	for _, target := range []string{"running", "started"} {
		t.Run(target, func(t *testing.T) {
			a, c := modelAgents(t)
			c.panel = &modelPanel{owner: c, unbracketed: -1}
			p, _ := a.profile("one")
			if target == "running" {
				if _, err := a.startInstance(p); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			var outcome error
			if err := a.HandTask(feature.AgentRef{Codename: "one"}, feature.TaskRef{Title: "task", Path: "/task.md"}, "work", func(err error) { calls++; outcome = err }); err != nil {
				t.Fatal(err)
			}
			if target == "started" {
				c.questions[0].OnYes()
			}
			c.drainPosts()
			c.fireAfters()
			waits := 0
			if target == "started" {
				waits = int(agentReadyWait / agentReadyPoll)
			}
			if calls != 1 || !errors.Is(outcome, terminal.ErrTerminalPasteUnbracketed) || len(c.delays) != waits {
				t.Fatalf("calls=%d outcome=%v delays=%d, want one ErrTerminalPasteUnbracketed after %d waits", calls, outcome, len(c.delays), waits)
			}
			if len(c.panel.pastes) != 0 || len(c.panel.modes) != 0 {
				t.Fatalf("sent or brought forward: pastes=%q modes=%q", c.panel.pastes, c.panel.modes)
			}
		})
	}
}

// TestHandTaskIsCheckedAgainInTheWait: a started agent's paste waiting for its program goes only
// to the same running agent, on the same parent, with the delivery still open. An agent that
// exited, restarted in its tab (a new generation), a rebuilt parent or a shutdown in the wait gets
// nothing, and done hears the reason once.
func TestHandTaskIsCheckedAgainInTheWait(t *testing.T) {
	for _, during := range []string{"exited", "restarted", "rebuilt", "shutdown"} {
		t.Run(during, func(t *testing.T) {
			a, c := modelAgents(t)
			c.panel = &modelPanel{owner: c, unbracketed: -1}
			calls := 0
			var outcome error
			if err := a.HandTask(feature.AgentRef{Codename: "one"}, feature.TaskRef{Title: "task", Path: "/task.md"}, "work", func(err error) { calls++; outcome = err }); err != nil {
				t.Fatal(err)
			}
			c.questions[0].OnYes()
			c.drainPosts()
			instance := a.instances["one"]
			if instance == nil || !instance.running || calls != 0 || len(c.afters) != 1 {
				t.Fatalf("the started agent's paste is not waiting: calls=%d afters=%d", calls, len(c.afters))
			}
			first := c.panel
			first.unbracketed = 0 // it would take the paste now
			switch during {
			case "exited":
				first.exit(instance.tab, 0)
			case "restarted":
				if err := first.Start(instance.tab); err != nil {
					t.Fatal(err)
				}
			case "rebuilt":
				c.panel = &modelPanel{owner: c, next: 100}
			case "shutdown":
				a.Stopping()
			}
			c.fireAfters()
			if len(first.pastes)+len(c.panel.pastes) != 0 {
				t.Fatalf("pasted after %s: %q %q", during, first.pastes, c.panel.pastes)
			}
			want := map[string]error{"shutdown": ErrAgentShuttingDown}[during]
			if want == nil {
				want = ErrAgentStale
			}
			if calls != 1 || !errors.Is(outcome, want) {
				t.Fatalf("done after %s: calls=%d outcome=%v", during, calls, outcome)
			}
		})
	}
}

// TestSendTextIsSentWithItsEnter: Lua's SendText, with no doorbell (no crew scope), is one
// bracketed paste and the Enter after it, and brings no panel forward.
func TestSendTextIsSentWithItsEnter(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	if _, err := a.startInstance(p); err != nil {
		t.Fatal(err)
	}
	c.opened = nil // the start opened the panel
	calls := 0
	a.promptsReported = func(string, bool) bool { return true } // a CLI that reports its prompts: the base sends
	if err := a.SendText(feature.AgentRef{Codename: "one"}, "draft", func(err error) {
		if err != nil {
			t.Error(err)
		}
		calls++
	}); err != nil {
		t.Fatal(err)
	}
	c.drainPosts()
	c.fireAfters()
	if calls != 1 || !slices.Equal(c.panel.pastes, []string{"tab-1:[bracketed]draft"}) ||
		!slices.Equal(c.delays, []time.Duration{submitDelay}) || !slices.Equal(c.panel.keys, []string{"\r"}) {
		t.Fatalf("SendText: calls=%d pastes=%q delays=%v keys=%q, want one paste and its Enter", calls, c.panel.pastes, c.delays, c.panel.keys)
	}
	if len(c.opened)+len(c.panel.modes) != 0 {
		t.Errorf("SendText brought the panel forward: opened=%q modes=%q", c.opened, c.panel.modes)
	}
}

func TestStoppedAgentDoesNotRelabelItsOldWorkspaceConfiguration(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	oldConfig := c.panel.configs[instance.tab]
	c.panel.exit(instance.tab, 3)
	c.ws = wsInfo{name: "other", root: t.TempDir()}
	c.epoch++
	done := 0
	var outcome error
	if err := a.HandTask(feature.AgentRef{Codename: "one"}, feature.TaskRef{Title: "other task", Path: "/other/task.md"}, "work", func(err error) { done++; outcome = err }); err != nil {
		t.Fatal(err)
	}
	c.questions[len(c.questions)-1].OnYes()
	if done != 1 || !errors.Is(outcome, ErrAgentStale) || instance.running || instance.workspace.Name != "kb" || len(c.panel.pastes) != 0 {
		t.Fatal("workspace B reused or relabelled workspace A process")
	}
	if c.panel.configs[instance.tab].Dir != oldConfig.Dir || c.panel.configs[instance.tab].Args[len(oldConfig.Args)-1] != oldConfig.Args[len(oldConfig.Args)-1] {
		t.Fatal("refused cross-workspace start mutated old configuration")
	}
}

func TestParentRebuildRetiresOldRefsAndCreatesCurrentOwnerInstances(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	oldRef := feature.AgentRef{Codename: "one", Instance: instance.token}
	oldOwner := c.panel
	done := 0
	var outcome error
	if err := a.HandTask(oldRef, feature.TaskRef{Title: "queued", Path: "/task.md"}, "work", func(err error) { done++; outcome = err }); err != nil {
		t.Fatal(err)
	}
	c.panel = &modelPanel{owner: c, next: 100}
	c.drainPosts()
	if done != 1 || !errors.Is(outcome, ErrAgentStale) || len(oldOwner.pastes) != 0 || len(c.panel.pastes) != 0 {
		t.Fatal("queued old-parent request sent or lost completion")
	}
	callbacks := 0
	if err := a.HandTask(oldRef, feature.TaskRef{Title: "stale", Path: "/task.md"}, "work", func(error) { callbacks++ }); !errors.Is(err, ErrAgentStale) || callbacks != 0 {
		t.Fatal("old ref survived parent retirement")
	}
	fresh, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == instance || fresh.token == oldRef.Instance || !fresh.owner.SameOwner(c.panel) || fresh.tab == instance.tab {
		t.Fatal("new instance still belongs to rebuilt old parent")
	}
	if err := a.selectInstance(fresh); err != nil {
		t.Fatal(err)
	}
	if err := a.removeInstance("one"); err != nil {
		t.Fatal(err)
	}
	if len(a.instances) != 0 {
		t.Fatal("current-owner removal used old-parent id")
	}
}

// TestUseAfterAParentRebuildNeverSelectsTheOldTab: the profiles dialog's Use, pressed after the
// terminal panel was rebuilt, retires the old parent's instance rather than selecting its tab on
// the new panel; once retired, Use starts a fresh instance on the current owner.
func TestUseAfterAParentRebuildNeverSelectsTheOldTab(t *testing.T) {
	a, c := modelAgents(t)
	p, _ := a.profile("one")
	old, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	c.panel = &modelPanel{owner: c, next: 100} // the parent rebuilt (a reload)
	a.useAgent(0)
	if c.panel.active == old.tab {
		t.Fatal("Use selected the old parent's tab on the new panel")
	}
	c.drainPosts() // the old owner's retirement completes
	a.useAgent(0)
	fresh := a.instances["one"]
	if fresh == nil || fresh == old || !fresh.running || !fresh.owner.SameOwner(c.panel) || c.panel.active != fresh.tab {
		t.Fatalf("after retirement Use did not start a current-owner instance: %+v", fresh)
	}
}
